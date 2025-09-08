package mem

import (
    "bufio"
    "context"
    "encoding/binary"
    "errors"
    "io"
    "net"
    "sync"
    "time"

    "ttmesh/pkg/transport"
)

// Transport is an in-process transport using net.Pipe. Useful for tests and
// as a stand-in for shared memory style transport.
type Transport struct {
    mu        sync.Mutex
    listeners map[string]*listener
}

func New() *Transport { return &Transport{listeners: make(map[string]*listener)} }

func (t *Transport) Kind() transport.Kind { return transport.KindMem }

func (t *Transport) Listen(ctx context.Context, name string) (transport.Listener, error) {
    t.mu.Lock(); defer t.mu.Unlock()
    if _, ok := t.listeners[name]; ok {
        return nil, errors.New("mem: listener already exists")
    }
    l := &listener{name: name, newCh: make(chan *session, 8), closeCh: make(chan struct{})}
    t.listeners[name] = l
    go func() { <-ctx.Done(); _ = l.Close(); t.mu.Lock(); delete(t.listeners, name); t.mu.Unlock() }()
    return l, nil
}

func (t *Transport) Dial(ctx context.Context, name string, peer transport.PeerInfo) (transport.Session, error) {
    t.mu.Lock(); l := t.listeners[name]; t.mu.Unlock()
    if l == nil { return nil, errors.New("mem: no such listener") }
    c1, c2 := net.Pipe()
    // server side session goes to listener
    srv := &session{peer: transport.PeerInfo{ID: peer.ID, Addr: name}, kind: transport.KindMem, c: c1, establishedAt: time.Now()}
    cli := &session{peer: peer, kind: transport.KindMem, c: c2, establishedAt: time.Now()}
    select { case l.newCh <- srv: default: _ = srv.Close() }
    go func() { <-ctx.Done(); _ = cli.Close() }()
    return cli, nil
}

type listener struct {
    name    string
    newCh   chan *session
    closeCh chan struct{}
}

func (l *listener) Addr() net.Addr { return memAddr(l.name) }

func (l *listener) Accept(ctx context.Context) (transport.Session, error) {
    select {
    case <-ctx.Done():
        return nil, ctx.Err()
    case <-l.closeCh:
        return nil, errors.New("mem listener closed")
    case s := <-l.newCh:
        return s, nil
    }
}

func (l *listener) Close() error {
    select { case <-l.closeCh: default: close(l.closeCh) }
    return nil
}

type memAddr string
func (a memAddr) Network() string { return "mem" }
func (a memAddr) String() string  { return string(a) }

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
