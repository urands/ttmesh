package peering

import (
    "context"

    "go.uber.org/zap"
    "google.golang.org/protobuf/proto"

    "ttmesh/pkg/peers"
    ttmeshproto "ttmesh/pkg/protocol/proto"
	"ttmesh/pkg/transport"
    "ttmesh/pkg/registry"
)

// HandleSession reads the control stream of a session, verifies Hello/HelloAck,
// updates peer identity, and processes subsequent control/data messages.
// Forwarding uses either a router or an async pipeline when available.
func HandleSession(
    ctx context.Context,
    mgr *transport.Manager,
    ps *peers.Store,
    reg *registry.Store,
    r interface{ SendBytesToPeer(context.Context, transport.PeerID, []byte) error },
    localID transport.PeerID,
    s transport.Session,
    pl interface{ EnqueueProto(transport.PeerID, *ttmeshproto.Envelope, []byte) },
    nodeName string,
    pubKey []byte,
) {
    st, err := s.OpenStream(ctx, transport.StreamControl)
    if err != nil {
        zap.L().Warn("open stream", zap.Error(err))
        return
    }

    // Track current peer id (may change after rebind); used for cleanup on close
    curID := s.Peer().ID
    defer func() {
        if ps != nil && string(localID) != "" && string(curID) != "" {
            ps.RemoveConnectedDirect(localID, curID)
            // Immediately delete peer record on close
            ps.DeletePeer(curID)
        }
    }()

    // Try to read first message as Hello (protobuf Envelope)
    first, err := st.RecvBytes()
    if err != nil {
        zap.L().Warn("recv first", zap.Error(err))
        return
    }

    var pe ttmeshproto.Envelope
    if err := proto.Unmarshal(first, &pe); err == nil {
        if ctrl := pe.GetControl(); ctrl != nil {
            if hello := ctrl.GetHello(); hello != nil {
                if pid, ok := handleHello(ctx, mgr, ps, localID, s, st, nodeName, pubKey, hello); ok {
                    curID = pid
                }
            }
            if ack := ctrl.GetHelloAck(); ack != nil {
                if newID, ok := handleHelloAck(ctx, mgr, ps, localID, s, ack); ok {
                    curID = newID
                }
            }
        } else if raw := pe.GetRaw(); raw != nil {
            if pid, ok := handleRawHello(ctx, mgr, ps, localID, s, raw); ok {
                curID = pid
            }
        }
    } else {
        zap.L().Info("non-proto first message", zap.Int("bytes", len(first)))
    }

    // Forwarder uses global pipeline when available, otherwise local queue.
    fwd := newForwarder(ctx, r, ps, pl)
    defer fwd.Close()

    // Continue to read and enqueue/forward messages
    for {
        buf, err := st.RecvBytes()
        if err != nil {
            zap.L().Warn("recv", zap.Error(err))
            return
        }
        if ps != nil {
            ps.RecordExchange(s.Peer().ID, uint64(len(buf)), 0, 1, 0)
        }
        // try decode as proto Envelope then JSON body
        var m ttmeshproto.Envelope
        if proto.Unmarshal(buf, &m) == nil {
            // Forward if destination is set and not us
            if hdr := m.GetHeader(); hdr != nil && hdr.GetDest() != nil {
                dst := hdr.GetDest().GetId()
                if dst != "" && string(localID) != "" && dst != string(localID) && r != nil {
                    fwd.EnqueueProto(s.Peer().ID, &m, buf)
                    zap.L().Debug("enqueued for forward", zap.String("dst", dst))
                    // Send a tiny direct-status ack back to sender (one-hop), unless this is itself such a reply.
                    if m.Header != nil && m.Header.DirectStatus == nil {
                        enqueueDirectStatusAck(fwd, localID, s.Peer().ID, &m, ttmeshproto.Status_ST_OK)
                    }
                    continue
                }
            }
            // If control requests
            if ctrl := m.GetControl(); ctrl != nil {
                if handleControlRequest(st, ps, reg, string(localID), nodeName, pubKey, ctrl) {
                    if m.Header != nil && m.Header.DirectStatus == nil {
                        enqueueDirectStatusAck(fwd, localID, s.Peer().ID, &m, ttmeshproto.Status_ST_OK)
                    }
                    continue
                }
            }
            if raw := m.GetRaw(); raw != nil {
                if tryJSONLog("inbound msg", raw) {
                    if m.Header != nil && m.Header.DirectStatus == nil {
                        enqueueDirectStatusAck(fwd, localID, s.Peer().ID, &m, ttmeshproto.Status_ST_OK)
                    }
                    continue
                }
            }
            zap.L().Info("inbound proto msg", zap.String("type", m.GetHeader().GetType().String()))
            if m.Header != nil && m.Header.DirectStatus == nil {
                enqueueDirectStatusAck(fwd, localID, s.Peer().ID, &m, ttmeshproto.Status_ST_OK)
            }
            continue
        }
        // fallback
        if tryJSONLog("inbound json", buf) {
            // handled
        } else {
            zap.L().Info("inbound bytes", zap.Int("len", len(buf)))
        }
    }
}
