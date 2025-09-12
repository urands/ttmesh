package netstack

import (
    "context"
    "crypto/ed25519"
    "sync"
    "time"

    "go.uber.org/zap"

    "ttmesh/pkg/config"
    "ttmesh/pkg/peers"
    ttmeshproto "ttmesh/pkg/protocol/proto"
    "ttmesh/pkg/transport"
    "ttmesh/pkg/transport/mem"
    tquic "ttmesh/pkg/transport/quic"
    ttcp "ttmesh/pkg/transport/tcp"
    "ttmesh/pkg/transport/udp"
    "ttmesh/pkg/registry"
)

// StartFromConfig builds transports per config, starts listeners and performs initial dials.
// Returns a closer that stops listeners; background dial goroutines stop when ctx is canceled.
type Options struct {
    BackoffInitial time.Duration
    BackoffMax     time.Duration
    BackoffJitter  time.Duration
}

type Manager struct {
    activeDials     int64
    activeListeners int64
}

func (m *Manager) ActiveDials() int64     { return m.activeDials }
func (m *Manager) ActiveListeners() int64 { return m.activeListeners }

func StartFromConfig(ctx context.Context, cfg []config.TransportConfig, mgr *transport.Manager, ps *peers.Store, reg *registry.Store, localID transport.PeerID, rtr interface{ SendBytesToPeer(context.Context, transport.PeerID, []byte) error }, priv ed25519.PrivateKey, nodeName string, pl interface{ EnqueueProto(transport.PeerID, *ttmeshproto.Envelope, []byte) }, opts Options) (func(), *Manager, error) {
    var closers []func()
    var mu sync.Mutex
    addCloser := func(f func()) { mu.Lock(); defer mu.Unlock(); closers = append(closers, f) }
    nm := &Manager{}

    for _, tc := range cfg {
        tr, err := NewByKind(tc.Kind)
        if err != nil {
            zap.L().Warn("transport kind not available", zap.String("kind", tc.Kind), zap.Error(err))
            continue
        }

        // Listen endpoints
        for _, addr := range tc.Listen {
            addr := addr
            l, err := tr.Listen(ctx, addr)
            if err != nil {
                zap.L().Error("listen failed", zap.String("kind", tr.Kind().String()), zap.String("addr", addr), zap.Error(err))
                continue
            }
            zap.L().Info("listening", zap.String("kind", tr.Kind().String()), zap.String("addr", l.Addr().String()))
            addCloser(func() { _ = l.Close() })
            nm.activeListeners++
            go func() {
                defer func() { nm.activeListeners-- }()
                acceptLoop(ctx, mgr, l, ps, reg, localID, rtr, pl, nodeName, priv.Public().(ed25519.PublicKey))
            }()
        }

        // Dial endpoints with backoff
        for _, d := range tc.Dial {
            d := d
            nm.activeDials++
            go func() {
                defer func() { nm.activeDials-- }()
                dialLoop(ctx, tr, mgr, ps, reg, localID, rtr, priv, nodeName, pl, d.Address, d.PeerID, opts)
            }()
        }
    }

    return func() { mu.Lock(); for i := len(closers)-1; i>=0; i-- { closers[i]() }; mu.Unlock() }, nm, nil
}

// NewByKind constructs a Transport by string kind.
func NewByKind(kind string) (transport.Transport, error) {
    switch kind {
    case "udp":
        return udp.New(), nil
    case "tcp":
        return ttcp.New(), nil
    case "quic", "h3", "http3":
        return tquic.New(), nil
    case "mem", "inproc", "shared":
        return mem.New(), nil
    case "winpipe", "pipe":
        return newWinPipeTransport()
    default:
        return nil, ErrUnknownKind(kind)
    }
}

// Basic typed error for unknown kinds
type ErrUnknownKind string
func (e ErrUnknownKind) Error() string { return "unknown transport kind: " + string(e) }

// timeNow shadow for testability
func timeNow() time.Time { return time.Now() }
