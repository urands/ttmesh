package udp

import (
    "context"
    "errors"
    "net"
    "sync"
    "time"

    "ttmesh/pkg/transport"
)

// UDPTransport implements a datagram transport carrying single-envelope frames.
// It does not support true multiplexed streams; one logical default stream is used.
type UDPTransport struct{}

func New() *UDPTransport { return &UDPTransport{} }

func (t *UDPTransport) Kind() transport.Kind { return transport.KindUDP }

func (t *UDPTransport) Listen(ctx context.Context, address string) (transport.Listener, error) {
    laddr, err := net.ResolveUDPAddr("udp", address)
    if err != nil { return nil, err }
    c, err := net.ListenUDP("udp", laddr)
    if err != nil { return nil, err }
    ul := &udpListener{
        conn:     c,
        sessions: make(map[string]*inboundSess),
        newCh:    make(chan *udpSession, 8),
        closeCh:  make(chan struct{}),
    }
    go ul.readLoop()
    go func() { <-ctx.Done(); _ = ul.Close() }()
    return ul, nil
}

func (t *UDPTransport) Dial(ctx context.Context, address string, peer transport.PeerInfo) (transport.Session, error) {
    raddr, err := net.ResolveUDPAddr("udp", address)
    if err != nil { return nil, err }
    c, err := net.DialUDP("udp", nil, raddr)
    if err != nil { return nil, err }
    s := &udpSession{
        peer:     peer,
        kind:     transport.KindUDP,
        establishedAt: time.Now(),
        conn:     c,
        raddr:    raddr,
        outbound: true,
        rxCh:     make(chan []byte, 16),
        closeOnce: sync.Once{},
    }
    // reader for connected UDP socket
    go s.recvLoop()
    go func() { <-ctx.Done(); _ = s.Close() }()
    return s, nil
}

// ---- Listener/demux ----

type inboundSess struct {
    rxCh chan []byte
}

type udpListener struct {
    conn     *net.UDPConn
    mu       sync.Mutex
    sessions map[string]*inboundSess
    newCh    chan *udpSession
    closeCh  chan struct{}
}

func (l *udpListener) Addr() net.Addr { return l.conn.LocalAddr() }

func (l *udpListener) Accept(ctx context.Context) (transport.Session, error) {
    select {
    case <-ctx.Done():
        return nil, ctx.Err()
    case <-l.closeCh:
        return nil, errors.New("udp listener closed")
    case s := <-l.newCh:
        return s, nil
    }
}

func (l *udpListener) Close() error {
    select {
    case <-l.closeCh:
        // already closed
    default:
        close(l.closeCh)
    }
    return l.conn.Close()
}

func (l *udpListener) readLoop() {
    buf := make([]byte, 64*1024)
    for {
        n, raddr, err := l.conn.ReadFromUDP(buf)
        if err != nil {
            select {
            case <-l.closeCh:
                return
            default:
            }
            return
        }
        key := raddr.String()
        l.mu.Lock()
        ins, ok := l.sessions[key]
        if !ok {
            ins = &inboundSess{rxCh: make(chan []byte, 32)}
            l.sessions[key] = ins
            // create new session wrapper
            s := &udpSession{
                peer: transport.PeerInfo{ID: transport.TempPeerID(transport.KindUDP, raddr), Addr: key},
                kind: transport.KindUDP,
                establishedAt: time.Now(),
                conn: l.conn,
                raddr: raddr,
                inboundRx: ins.rxCh,
                rxCh: make(chan []byte, 1), // unused in inbound mode
            }
            select { case l.newCh <- s: default: }
        }
        // copy out payload and dispatch
        pkt := make([]byte, n)
        copy(pkt, buf[:n])
        // forward into per-remote queue (drop if full)
        select { case ins.rxCh <- pkt: default: }
        l.mu.Unlock()
    }
}

// ---- Session/Stream ----

type udpSession struct {
    peer          transport.PeerInfo
    kind          transport.Kind
    conn          *net.UDPConn
    raddr         *net.UDPAddr
    outbound      bool
    inboundRx     chan []byte // used when session is inbound (shared listener)
    rxCh          chan []byte // used when session owns the socket (outbound)
    closeOnce     sync.Once
    closed        chan struct{}
    establishedAt time.Time
    lastSeen      time.Time
}

func (s *udpSession) Peer() transport.PeerInfo { return s.peer }
func (s *udpSession) SetPeer(pi transport.PeerInfo) { s.peer = pi }
func (s *udpSession) TransportKind() transport.Kind { return s.kind }
func (s *udpSession) LocalAddr() net.Addr { return s.conn.LocalAddr() }
func (s *udpSession) RemoteAddr() net.Addr { return s.raddr }

func (s *udpSession) OpenStream(ctx context.Context, _ transport.StreamClass) (transport.Stream, error) {
    return &udpStream{s: s}, nil
}

func (s *udpSession) AcceptStream(ctx context.Context) (transport.Stream, error) {
    // UDP has only one logical stream
    return s.OpenStream(ctx, transport.StreamControl)
}

func (s *udpSession) Quality() transport.Quality {
    return transport.Quality{EstablishedAt: s.establishedAt, LastSeen: s.lastSeen}
}

func (s *udpSession) recvLoop() {
    buf := make([]byte, 64*1024)
    for {
        n, err := s.conn.Read(buf)
        if err != nil {
            return
        }
        pkt := make([]byte, n)
        copy(pkt, buf[:n])
        select { case s.rxCh <- pkt: default: }
    }
}

func (s *udpSession) Close() error {
    var err error
    s.closeOnce.Do(func() {
        if s.outbound {
            err = s.conn.Close()
        }
        if s.closed != nil { close(s.closed) }
    })
    return err
}

type udpStream struct { s *udpSession }

func (st *udpStream) SendBytes(b []byte) error {
    var err error
    if !st.s.outbound {
        _, err = st.s.conn.WriteToUDP(b, st.s.raddr)
    } else {
        _, err = st.s.conn.Write(b)
    }
    if err == nil { st.s.lastSeen = time.Now() }
    return err
}

func (st *udpStream) RecvBytes() ([]byte, error) {
    var pkt []byte
    if st.s.outbound {
        pkt = <-st.s.rxCh
    } else {
        pkt = <-st.s.inboundRx
    }
    if pkt == nil { return nil, errors.New("udp stream closed") }
    st.s.lastSeen = time.Now()
    return pkt, nil
}

func (st *udpStream) Close() error { return nil }
