package netstack

import (
    "context"

    "go.uber.org/zap"

    corepeering "ttmesh/pkg/core/peering"
    "ttmesh/pkg/peers"
    ttmeshproto "ttmesh/pkg/protocol/proto"
    "ttmesh/pkg/registry"
    "ttmesh/pkg/transport"
)

func acceptLoop(ctx context.Context, mgr *transport.Manager, l transport.Listener, ps *peers.Store, reg *registry.Store, localID transport.PeerID, rtr interface{ SendBytesToPeer(context.Context, transport.PeerID, []byte) error }, pl interface{ EnqueueProto(transport.PeerID, *ttmeshproto.Envelope, []byte) }, nodeName string, pub []byte) {
    for {
        s, err := l.Accept(ctx)
        if err != nil {
            select {
            case <-ctx.Done():
                return
            default:
            }
            zap.L().Warn("accept failed", zap.String("addr", l.Addr().String()), zap.Error(err))
            return
        }
        peer := s.Peer()
        zap.L().Info("inbound session", zap.String("peer", string(peer.ID)), zap.String("kind", s.TransportKind().String()), zap.String("raddr", s.RemoteAddr().String()))
        accepted, replaced, old, _ := mgr.AddSession(ctx, s)
        if replaced && old != nil { _ = old.Close() }
        if !accepted { _ = s.Close(); continue }
        if ps != nil {
            ps.Touch(peer.ID, s.RemoteAddr().String(), timeNow())
            ps.RecordQuality(peer.ID, s.Quality())
            if localID != "" { ps.AddConnectedDirect(localID, peer.ID) }
            zap.L().Info("direct link", zap.String("local", string(localID)), zap.String("peer", string(peer.ID)))
        }
        go corepeering.HandleSession(ctx, mgr, ps, reg, rtr, localID, s, pl, nodeName, pub)
    }
}
