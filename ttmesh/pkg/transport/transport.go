package transport

import (
    "context"
    "net"
    "time"
)

// Kind identifies transport/link type for policy decisions.
type Kind int

const (
    KindUnknown Kind = iota
    KindQUICDirect
    KindTCPDirect
    KindQUICHolePunch
    KindQUICRelay
    KindTCPRelay
    KindUDP
    KindWinPipe
    KindMem
)

func (k Kind) String() string {
    switch k {
    case KindQUICDirect:
        return "quic:direct"
    case KindTCPDirect:
        return "tcp:direct"
    case KindQUICHolePunch:
        return "quic:hole-punch"
    case KindQUICRelay:
        return "quic:relay"
    case KindTCPRelay:
        return "tcp:relay"
    case KindUDP:
        return "udp"
    case KindWinPipe:
        return "winpipe"
    case KindMem:
        return "mem"
    default:
        return "unknown"
    }
}

// StreamClass labels multiplexed streams within a session.
type StreamClass int

const (
    StreamControl StreamClass = iota
    StreamTask
    StreamGossip
    StreamBulk
)

// PeerID is an opaque stable peer identity (e.g., node id or public key hash).
type PeerID string

// PeerInfo bundles peer identity and addressing hints.
type PeerInfo struct {
    ID        PeerID
    Addr      string // transport-dependent address string
    Reachable bool   // best-effort reachability
}

// Quality captures link quality metrics used by the manager to rank sessions.
type Quality struct {
    RTT        time.Duration
    LossRatio  float32
    Jitter     time.Duration
    EstablishedAt time.Time
    LastSeen   time.Time
    Score      float32 // computed preference score (higher is better)
}

// Stream is a bidirectional envelope stream.
// Exactly one reader and one writer goroutine are expected.
type Stream interface {
    // SendBytes sends one message frame as opaque bytes (e.g., marshaled protobuf Envelope).
    SendBytes([]byte) error
    // RecvBytes receives the next message frame and returns its bytes.
    RecvBytes() ([]byte, error)
    Close() error
}

// Session represents a canonical connection to a peer with optional multiplexed streams.
type Session interface {
    Peer() PeerInfo
    TransportKind() Kind
    LocalAddr() net.Addr
    RemoteAddr() net.Addr

    // OpenStream opens/returns a stream of the given class. Transports without
    // multiplexing may return a single shared stream for all classes.
    OpenStream(ctx context.Context, cls StreamClass) (Stream, error)

    // AcceptStream waits for the next inbound stream (if supported). For transports
    // without native streams, this can return the default stream once and then block or err.
    AcceptStream(ctx context.Context) (Stream, error)

    // Quality snapshot for ranking/monitoring.
    Quality() Quality

    // Close closes the entire session.
    Close() error
}

// Listener accepts inbound sessions.
type Listener interface {
    // Accept blocks until an inbound session is available or ctx is done.
    Accept(ctx context.Context) (Session, error)
    // Addr returns the local listening address.
    Addr() net.Addr
    // Close stops the listener and unblocks Accept.
    Close() error
}

// Transport provides dialing/listening for a specific link kind.
type Transport interface {
    Kind() Kind
    // Listen starts accepting inbound sessions on address (transport-specific format).
    Listen(ctx context.Context, address string) (Listener, error)
    // Dial creates an outbound session to a peer/address.
    Dial(ctx context.Context, address string, peer PeerInfo) (Session, error)
}
