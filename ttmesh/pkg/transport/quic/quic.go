package quic

import (
    "bufio"
    "context"
    "crypto/rand"
    "crypto/rsa"
    "crypto/tls"
    "crypto/x509"
    "encoding/binary"
    "errors"
    "io"
    "math/big"
    "net"
    "reflect"
    "sync"
    "time"

    quicgo "github.com/quic-go/quic-go"

    "ttmesh/pkg/transport"
)

// Transport implements QUIC-based sessions with length-prefixed frames per stream.
// It exposes a single default control stream (opened by the dialer; accepted by the listener).
type Transport struct {
    tlsConf  *tls.Config
    quicConf *quicgo.Config
}

func New() *Transport {
    // Generate an ephemeral self-signed certificate for server side.
    cert, _ := selfSignedCert()
    tlsConf := &tls.Config{
        Certificates: []tls.Certificate{cert},
        NextProtos:   []string{"ttmesh"},
        MinVersion:   tls.VersionTLS13,
    }
    qconf := &quicgo.Config{}
    return &Transport{tlsConf: tlsConf, quicConf: qconf}
}

func (t *Transport) Kind() transport.Kind { return transport.KindQUICDirect }

func (t *Transport) Listen(ctx context.Context, address string) (transport.Listener, error) {
    // Use the server TLS config directly (it contains the certificate)
    l, err := quicgo.ListenAddr(address, t.tlsConf, t.quicConf)
    if err != nil { return nil, err }
    // Capture Addr() now to avoid interface shape differences later
    addr := l.Addr()
    ql := &listener{l: any(l), laddr: addr, newCh: make(chan *session, 8), closeCh: make(chan struct{})}
    go ql.acceptLoop(ctx)
    go func() { <-ctx.Done(); _ = ql.Close() }()
    return ql, nil
}

func (t *Transport) Dial(ctx context.Context, address string, peer transport.PeerInfo) (transport.Session, error) {
    // Build a client TLS config; skip verification for now (app-layer Hello will rebind/verify).
    tlsClient := &tls.Config{
        InsecureSkipVerify: true, // NOTE: Identity is verified at application layer.
        NextProtos:         []string{"ttmesh"},
        MinVersion:         tls.VersionTLS13,
    }
    // Use context-aware dial (quic-go modern API)
    c, err := quicgo.DialAddr(ctx, address, tlsClient, t.quicConf)
    if err != nil { return nil, err }
    s := &session{
        peer:          peer,
        kind:          transport.KindQUICDirect,
        c:             c,
        establishedAt: time.Now(),
    }
    go func() { <-ctx.Done(); _ = s.Close() }()
    return s, nil
}

// ---- Listener ----

type listener struct {
    l       any
    laddr   net.Addr
    newCh   chan *session
    closeCh chan struct{}
}

func (l *listener) Addr() net.Addr { return l.laddr }

func (l *listener) Accept(ctx context.Context) (transport.Session, error) {
    select {
    case <-ctx.Done():
        return nil, ctx.Err()
    case <-l.closeCh:
        return nil, errors.New("quic listener closed")
    case s := <-l.newCh:
        return s, nil
    }
}

func (l *listener) Close() error {
    select { case <-l.closeCh: default: close(l.closeCh) }
    if v, ok := l.l.(interface{ Close() error }); ok { return v.Close() }
    return nil
}

func (l *listener) acceptLoop(ctx context.Context) {
    for {
        // Use reflection to support multiple quic-go versions without referencing specific types
        mv := reflect.ValueOf(l.l).MethodByName("Accept")
        if !mv.IsValid() { return }
        outs := mv.Call([]reflect.Value{reflect.ValueOf(ctx)})
        if len(outs) != 2 { return }
        var err error
        if !outs[1].IsNil() { err = outs[1].Interface().(error) }
        if err != nil { return }
        anyConn := outs[0].Interface()
        // Obtain remote address via dynamic call
        var raddr net.Addr
        if mv := reflect.ValueOf(anyConn).MethodByName("RemoteAddr"); mv.IsValid() {
            rv := mv.Call(nil)
            if len(rv) == 1 && !rv[0].IsNil() {
                if a, ok := rv[0].Interface().(net.Addr); ok { raddr = a }
            }
        }
        s := &session{
            peer:          transport.PeerInfo{ID: transport.TempPeerID(transport.KindQUICDirect, raddr), Addr: raddr.String(), Reachable: true},
            kind:          transport.KindQUICDirect,
            c:             anyConn,
            inbound:       true,
            establishedAt: time.Now(),
        }
        select { case l.newCh <- s: default: _ = s.Close() }
    }
}

// ---- Session/Streams ----

type session struct {
    peer transport.PeerInfo
    kind transport.Kind
    c    any

    inbound  bool
    establishedAt time.Time
    lastSeen      time.Time

    mu       sync.Mutex
    ctrl     *qstream
}

func (s *session) Peer() transport.PeerInfo { return s.peer }
func (s *session) SetPeer(pi transport.PeerInfo) { s.peer = pi }
func (s *session) TransportKind() transport.Kind { return s.kind }
func (s *session) LocalAddr() net.Addr {
    if v, ok := s.c.(interface{ LocalAddr() net.Addr }); ok { return v.LocalAddr() }
    return nil
}
func (s *session) RemoteAddr() net.Addr {
    if v, ok := s.c.(interface{ RemoteAddr() net.Addr }); ok { return v.RemoteAddr() }
    return nil
}

func (s *session) OpenStream(ctx context.Context, _ transport.StreamClass) (transport.Stream, error) {
    s.mu.Lock()
    if s.ctrl != nil {
        st := s.ctrl
        s.mu.Unlock()
        return st, nil
    }
    s.mu.Unlock()

    // Reflective call to AcceptStream/OpenStreamSync/OpenStream depending on direction
    var mv reflect.Value
    if s.inbound {
        mv = reflect.ValueOf(s.c).MethodByName("AcceptStream")
    } else {
        mv = reflect.ValueOf(s.c).MethodByName("OpenStreamSync")
        if !mv.IsValid() { mv = reflect.ValueOf(s.c).MethodByName("OpenStream") }
    }
    if !mv.IsValid() { return nil, errors.New("quic: stream open method not found") }
    outs := mv.Call([]reflect.Value{reflect.ValueOf(ctx)})
    if len(outs) != 2 { return nil, errors.New("quic: unexpected stream open signature") }
    var err error
    if !outs[1].IsNil() { err = outs[1].Interface().(error) }
    if err != nil { return nil, err }
    qs, _ := outs[0].Interface().(quicgo.Stream)
    st, err := wrapStream(qs, s)
    if err != nil { return nil, err }
    s.mu.Lock(); s.ctrl = st; s.mu.Unlock()
    return st, nil
}

func (s *session) AcceptStream(ctx context.Context) (transport.Stream, error) {
    mv := reflect.ValueOf(s.c).MethodByName("AcceptStream")
    if !mv.IsValid() { return nil, errors.New("quic: connection lacks AcceptStream") }
    outs := mv.Call([]reflect.Value{reflect.ValueOf(ctx)})
    if len(outs) != 2 { return nil, errors.New("quic: unexpected AcceptStream signature") }
    var err error
    if !outs[1].IsNil() { err = outs[1].Interface().(error) }
    if err != nil { return nil, err }
    qs, _ := outs[0].Interface().(quicgo.Stream)
    return wrapStream(qs, s)
}

func (s *session) Quality() transport.Quality {
    return transport.Quality{EstablishedAt: s.establishedAt, LastSeen: s.lastSeen}
}

func (s *session) Close() error {
    // Try multiple Close signatures across quic-go versions
    if v, ok := s.c.(interface{ CloseWithError(code uint64, msg string) error }); ok {
        return v.CloseWithError(0, "")
    }
    if v, ok := s.c.(interface{ CloseWithError(code int, msg string) error }); ok {
        return v.CloseWithError(0, "")
    }
    if v, ok := s.c.(interface{ Close(error) error }); ok {
        return v.Close(nil)
    }
    if v, ok := s.c.(interface{ Close() error }); ok {
        return v.Close()
    }
    return nil
}

// qstream implements transport.Stream over a QUIC bidirectional stream with u32 LE framing.
type qstream struct {
    mu     sync.Mutex
    r      io.Reader
    w      io.Writer
    closef func() error
    br     *bufio.Reader
    bw     *bufio.Writer
    parent *session
}

func (st *qstream) SendBytes(b []byte) error {
    st.mu.Lock(); defer st.mu.Unlock()
    var lenbuf [4]byte
    binary.LittleEndian.PutUint32(lenbuf[:], uint32(len(b)))
    if _, err := st.bw.Write(lenbuf[:]); err != nil { return err }
    if _, err := st.bw.Write(b); err != nil { return err }
    if err := st.bw.Flush(); err != nil { return err }
    st.parent.lastSeen = time.Now()
    return nil
}

func (st *qstream) RecvBytes() ([]byte, error) {
    var lenbuf [4]byte
    if _, err := io.ReadFull(st.br, lenbuf[:]); err != nil { return nil, err }
    n := int(binary.LittleEndian.Uint32(lenbuf[:]))
    if n < 0 || n > (1<<24) { return nil, errors.New("invalid frame size") }
    buf := make([]byte, n)
    if _, err := io.ReadFull(st.br, buf); err != nil { return nil, err }
    st.parent.lastSeen = time.Now()
    return buf, nil
}

func (st *qstream) Close() error { if st.closef != nil { return st.closef() }; return nil }

// wrapStream normalizes quic-go Stream (struct or interface) to our qstream using io.Reader/Writer.
func wrapStream(qs quicgo.Stream, parent *session) (*qstream, error) {
    // Try direct use as ReadWriter
    if rw, ok := any(qs).(interface{ io.Reader; io.Writer; Close() error }); ok {
        br := bufio.NewReader(rw)
        bw := bufio.NewWriter(rw)
        return &qstream{r: rw, w: rw, closef: rw.Close, br: br, bw: bw, parent: parent}, nil
    }
    // Try pointer to stream (older versions where Stream is a struct with pointer-receiver methods)
    if rw, ok := any(&qs).(interface{ io.Reader; io.Writer; Close() error }); ok {
        br := bufio.NewReader(rw)
        bw := bufio.NewWriter(rw)
        return &qstream{r: rw, w: rw, closef: rw.Close, br: br, bw: bw, parent: parent}, nil
    }
    // Fallback: separate interfaces
    var r io.Reader
    var w io.Writer
    var closeFn func() error
    if rr, ok := any(qs).(interface{ Read([]byte) (int, error) }); ok { r = rr }
    if rr, ok := any(&qs).(interface{ Read([]byte) (int, error) }); ok && r == nil { r = rr }
    if ww, ok := any(qs).(interface{ Write([]byte) (int, error) }); ok { w = ww }
    if ww, ok := any(&qs).(interface{ Write([]byte) (int, error) }); ok && w == nil { w = ww }
    if cl, ok := any(qs).(interface{ Close() error }); ok { closeFn = cl.Close }
    if cl, ok := any(&qs).(interface{ Close() error }); ok && closeFn == nil { closeFn = cl.Close }
    if r == nil || w == nil || closeFn == nil { return nil, errors.New("quic: stream does not expose io.Reader/Writer") }
    br := bufio.NewReader(r)
    bw := bufio.NewWriter(w)
    return &qstream{r: r, w: w, closef: closeFn, br: br, bw: bw, parent: parent}, nil
}

// ---- Helpers ----

// selfSignedCert generates a short-lived self-signed TLS certificate for local QUIC use.
func selfSignedCert() (tls.Certificate, error) {
    priv, err := rsa.GenerateKey(rand.Reader, 2048)
    if err != nil { return tls.Certificate{}, err }
    tmpl := x509.Certificate{
        SerialNumber: big.NewInt(time.Now().UnixNano()),
        NotBefore:    time.Now().Add(-time.Minute),
        NotAfter:     time.Now().Add(24 * time.Hour),
        KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
        ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
        BasicConstraintsValid: true,
        DNSNames:     []string{"localhost"},
    }
    der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
    if err != nil { return tls.Certificate{}, err }
    cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}
    return cert, nil
}
