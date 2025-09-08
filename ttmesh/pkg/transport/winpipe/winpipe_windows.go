//go:build windows

package winpipe

import (
    "bufio"
    "context"
    "encoding/binary"
    "errors"
    "io"
    "net"
    "sync"
    "time"

    "github.com/Microsoft/go-winio"
    "ttmesh/pkg/transport"
)

type Transport struct{}

func New() *Transport { return &Transport{} }

func (t *Transport) Kind() transport.Kind { return transport.KindWinPipe }

func (t *Transport) Listen(ctx context.Context, pipeName string) (transport.Listener, error) {
    l, err := winio.ListenPipe(pipeName, nil)
    if err != nil { return nil, err }
    wl := &listener{l: l, newCh: make(chan *session, 8), closeCh: make(chan struct{})}
    go wl.acceptLoop()
    go func() { <-ctx.Done(); _ = wl.Close() }()
    return wl, nil
}

func (t *Transport) Dial(ctx context.Context, pipeName string, peer transport.PeerInfo) (transport.Session, error) {
    conn, err := winio.DialPipeContext(ctx, pipeName)
    if err != nil { return nil, err }
    s := &session{
        peer: peer,
        kind: transport.KindWinPipe,
        c:    conn,
        establishedAt: time.Now(),
    }
    go func() { <-ctx.Done(); _ = s.Close() }()
    return s, nil
}

type listener struct {
    l       net.Listener
    newCh   chan *session
    closeCh chan struct{}
}

func (l *listener) Addr() net.Addr { return l.l.Addr() }

func (l *listener) Accept(ctx context.Context) (transport.Session, error) {
    select {
    case <-ctx.Done():
        return nil, ctx.Err()
    case <-l.closeCh:
        return nil, errors.New("winpipe listener closed")
    case s := <-l.newCh:
        return s, nil
    }
}

func (l *listener) Close() error {
    select { case <-l.closeCh: default: close(l.closeCh) }
    return l.l.Close()
}

func (l *listener) acceptLoop() {
    for {
        c, err := l.l.Accept()
        if err != nil { return }
        s := &session{
            peer: transport.PeerInfo{ID: transport.TempPeerID(transport.KindWinPipe, c.RemoteAddr()), Addr: c.RemoteAddr().String()},
            kind: transport.KindWinPipe,
            c:    c,
            establishedAt: time.Now(),
        }
        select { case l.newCh <- s: default: _ = s.Close() }
    }
}

type session struct {
    mu   sync.Mutex
    peer transport.PeerInfo
    kind transport.Kind
    c    net.Conn
    br   *bufio.Reader
    bw   *bufio.Writer
    establishedAt time.Time
    lastSeen      time.Time
}

func (s *session) Peer() transport.PeerInfo { return s.peer }
func (s *session) SetPeer(pi transport.PeerInfo) { s.peer = pi }
func (s *session) TransportKind() transport.Kind { return s.kind }
func (s *session) LocalAddr() net.Addr { return s.c.LocalAddr() }
func (s *session) RemoteAddr() net.Addr { return s.c.RemoteAddr() }

func (s *session) OpenStream(_ context.Context, _ transport.StreamClass) (transport.Stream, error) {
    if s.br == nil || s.bw == nil {
        s.br = bufio.NewReader(s.c)
        s.bw = bufio.NewWriter(s.c)
    }
    return s, nil
}
func (s *session) AcceptStream(_ context.Context) (transport.Stream, error) { return s.OpenStream(context.Background(), transport.StreamControl) }
func (s *session) Quality() transport.Quality { return transport.Quality{EstablishedAt: s.establishedAt, LastSeen: s.lastSeen} }
func (s *session) Close() error { return s.c.Close() }

// Stream methods: length-prefixed frames (u32 LE)
func (s *session) SendBytes(b []byte) error {
    s.mu.Lock(); defer s.mu.Unlock()
    if s.bw == nil { s.bw = bufio.NewWriter(s.c) }
    var lenbuf [4]byte
    binary.LittleEndian.PutUint32(lenbuf[:], uint32(len(b)))
    if _, err := s.bw.Write(lenbuf[:]); err != nil { return err }
    if _, err := s.bw.Write(b); err != nil { return err }
    if err := s.bw.Flush(); err != nil { return err }
    s.lastSeen = time.Now(); return nil
}
func (s *session) RecvBytes() ([]byte, error) {
    if s.br == nil { s.br = bufio.NewReader(s.c) }
    var lenbuf [4]byte
    if _, err := s.br.Read(lenbuf[:]); err != nil { return nil, err }
    n := int(binary.LittleEndian.Uint32(lenbuf[:]))
    if n < 0 || n > (1<<24) { return nil, errors.New("invalid frame size") }
    buf := make([]byte, n)
    if _, err := io.ReadFull(s.br, buf); err != nil { return nil, err }
    s.lastSeen = time.Now(); return buf, nil
}
