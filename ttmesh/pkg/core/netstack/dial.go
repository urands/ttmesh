package netstack

import (
    "context"
    "crypto/ed25519"
    "time"

    "go.uber.org/zap"

	"ttmesh/pkg/node/peering"
	"ttmesh/pkg/peers"
	ttmeshproto "ttmesh/pkg/protocol/proto"
	"ttmesh/pkg/transport"
)

func dialLoop(ctx context.Context, tr transport.Transport, mgr *transport.Manager, ps *peers.Store, localID transport.PeerID, rtr interface{ SendBytesToPeer(context.Context, transport.PeerID, []byte) error }, priv ed25519.PrivateKey, nodeName string, pl interface{ EnqueueProto(transport.PeerID, *ttmeshproto.Envelope, []byte) }, address, peerID string, opts Options) {
    pid := transport.PeerID(peerID)
    if pid == "" { pid = transport.PeerID("temp:" + tr.Kind().String() + ":" + address) }
    peer := transport.PeerInfo{ID: pid, Addr: address}

    backoff := opts.BackoffInitial
    if backoff <= 0 { backoff = 500 * time.Millisecond }
    maxBackoff := opts.BackoffMax
    if maxBackoff <= 0 { maxBackoff = 30 * time.Second }

    for {
        select {
        case <-ctx.Done():
            return
        default:
        }
        sess, err := tr.Dial(ctx, address, peer)
        if err != nil {
            zap.L().Warn("dial failed", zap.String("kind", tr.Kind().String()), zap.String("addr", address), zap.Error(err))
            time.Sleep(withJitter(backoff, opts.BackoffJitter))
            if backoff < maxBackoff { backoff *= 2; if backoff > maxBackoff { backoff = maxBackoff } }
            continue
        }
        backoff = opts.BackoffInitial
        if backoff <= 0 { backoff = 500 * time.Millisecond }

        accepted, replaced, old, _ := mgr.AddSession(ctx, sess)
        zap.L().Info("dialed", zap.String("kind", tr.Kind().String()), zap.String("addr", address), zap.Bool("accepted", accepted), zap.Bool("replaced", replaced))
        if old != nil { _ = old.Close() }
        if ps != nil {
            ps.Touch(peer.ID, sess.RemoteAddr().String(), timeNow())
            ps.RecordQuality(peer.ID, sess.Quality())
            if localID != "" { ps.AddConnectedDirect(localID, peer.ID) }
            zap.L().Info("direct link", zap.String("local", string(localID)), zap.String("peer", string(peer.ID)))
        }
        if err := sendHello(ctx, sess, priv, nodeName); err != nil {
            zap.L().Warn("send hello on dial failed", zap.Error(err))
        }
        if accepted {
            peering.HandleSession(ctx, mgr, ps, rtr, localID, sess, pl, nodeName, priv.Public().(ed25519.PublicKey))
            continue
        }
        _ = sess.Close()
        time.Sleep(withJitter(backoff, opts.BackoffJitter))
        if backoff < maxBackoff { backoff *= 2; if backoff > maxBackoff { backoff = maxBackoff } }
    }
}

func withJitter(d, jitter time.Duration) time.Duration {
    if jitter <= 0 { return d }
    // add random 0..jitter
    n := time.Now().UnixNano()
    j := time.Duration(n%int64(jitter))
    return d + j
}
