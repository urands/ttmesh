package peering

import (
    "context"
    "encoding/json"
    "time"
    "strings"

    "go.uber.org/zap"

    "google.golang.org/protobuf/proto"
    "ttmesh/pkg/handshake"
    "ttmesh/pkg/peers"
    ttmeshproto "ttmesh/pkg/protocol/proto"
    "ttmesh/pkg/transport"
    "ttmesh/pkg/pipeline"
)

type routerLike interface { SendBytesToPeer(context.Context, transport.PeerID, []byte) error }
type enqueuer interface { EnqueueProto(transport.PeerID, *ttmeshproto.Envelope, []byte) }

// HandleSession reads the control stream of a session, verifies Hello, updates
// peer identity and continues to log inbound messages. It is intentionally
// minimal and can be extended to full RPC/task exchange later.
func HandleSession(ctx context.Context, mgr *transport.Manager, ps *peers.Store, r routerLike, localID transport.PeerID, s transport.Session, pl interface{ EnqueueProto(transport.PeerID, *ttmeshproto.Envelope, []byte) }, nodeName string, pubKey []byte) {
    st, err := s.OpenStream(ctx, transport.StreamControl)
    if err != nil { zap.L().Warn("open stream", zap.Error(err)); return }

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
        // Prefer Control.Hello if present
        if ctrl := pe.GetControl(); ctrl != nil {
            if hello := ctrl.GetHello(); hello != nil {
                h := handshake.Hello{
                    Version:   1,
                    NodeName:  hello.GetNodeName(),
                    Alg:       hello.GetAlg(),
                    PubKey:    append([]byte(nil), hello.GetPubkey()...),
                    Nonce:     append([]byte(nil), hello.GetNonce()...),
                    Timestamp: hello.GetTsUnixMs(),
                    Sig:       append([]byte(nil), hello.GetSig()...),
                }
                if pid, err := handshake.VerifyHello(h, 0); err == nil {
                    oldID := s.Peer().ID
                    reb := mgr.RebindPeer(oldID, pid)
                    zap.L().Info("hello accepted", zap.String("old_id", string(oldID)), zap.String("new_id", string(pid)), zap.Bool("rebound", reb))
                    if ps != nil {
                        pm := peers.PeerMeta{ID: pid, NodeName: h.NodeName, Alg: h.Alg, PublicKey: h.PubKey, Reachable: true, Handshake: "verified", LastHelloTs: h.Timestamp}
                        ps.Upsert(pm)
                        ps.Touch(pid, s.RemoteAddr().String(), time.Now())
                        if string(localID) != "" {
                            ps.AddConnectedDirect(localID, pid)
                            ps.RemoveConnectedDirect(localID, oldID)
                        }
                        if strings.HasPrefix(string(oldID), "temp:") { ps.ExpirePeer(oldID, 30*time.Second) }
                    }
                    curID = pid
                    // Send HelloAck
                    ack := &ttmeshproto.PeerHelloAck{Accepted: true, PeerId: string(pid), Meta: &ttmeshproto.PeerMeta{
                        // Meta describes this server (responder)
                        Id:         string(localID),
                        NodeName:   nodeName,
                        Alg:        "ed25519",
                        Pubkey:     pubKey,
                        Reachable:  true,
                        LastHelloTs: h.Timestamp,
                        LastAckTs:   time.Now().UnixMilli(),
                        Handshake:  ttmeshproto.HandshakeStatus_HS_VERIFIED,
                    }}
                    env := &ttmeshproto.Envelope{Header: &ttmeshproto.Header{Version: 1, Type: ttmeshproto.MessageType_MT_CONTROL}, Body: &ttmeshproto.Envelope_Control{Control: &ttmeshproto.Control{Kind: &ttmeshproto.Control_HelloAck{HelloAck: ack}}}}
                    if b, err := proto.Marshal(env); err == nil {
                        _ = st.SendBytes(b)
                        if ps != nil { ps.RecordExchange(pid, 0, uint64(len(b)), 0, 1) }
                    }
                } else {
                    zap.L().Warn("hello verify failed", zap.Error(err))
                }
            }
            if ack := ctrl.GetHelloAck(); ack != nil {
                // For initiator, rebind to responder's canonical id (from Meta.Id),
                // not to our own id (ack.peer_id describes the client).
                var metaID string
                if ack.GetMeta() != nil { metaID = ack.GetMeta().GetId() }
                newID := transport.PeerID(metaID)
                if newID != "" {
                    oldID := s.Peer().ID
                    reb := mgr.RebindPeer(oldID, newID)
                    zap.L().Info("hello-ack received", zap.String("old_id", string(oldID)), zap.String("new_id", string(newID)), zap.Bool("rebound", reb))
                    if ps != nil {
                        var name, alg string
                        var pub []byte
                        if ack.GetMeta() != nil {
                            name = ack.GetMeta().GetNodeName()
                            alg = ack.GetMeta().GetAlg()
                            pub = append([]byte(nil), ack.GetMeta().GetPubkey()...)
                        }
                        pm := peers.PeerMeta{ID: newID, NodeName: name, Alg: alg, PublicKey: pub, Reachable: true, Handshake: "verified", LastAckTs: time.Now().UnixMilli()}
                        ps.Upsert(pm)
                        ps.Touch(newID, s.RemoteAddr().String(), time.Now())
                        if string(localID) != "" {
                            ps.AddConnectedDirect(localID, newID)
                            ps.RemoveConnectedDirect(localID, oldID)
                        }
                        if strings.HasPrefix(string(oldID), "temp:") { ps.ExpirePeer(oldID, 30*time.Second) }
                    }
                    curID = newID
                }
            }
        } else if raw := pe.GetRaw(); raw != nil {
            // Backward compat: JSON hello in raw body
            var h handshake.Hello
            if json.Unmarshal(raw, &h) == nil && h.Alg != "" {
                if pid, err := handshake.VerifyHello(h, 0); err == nil {
                    oldID := s.Peer().ID
                    reb := mgr.RebindPeer(oldID, pid)
                    zap.L().Info("hello accepted", zap.String("old_id", string(oldID)), zap.String("new_id", string(pid)), zap.Bool("rebound", reb))
                    if ps != nil {
                        pm := peers.PeerMeta{ID: pid, NodeName: h.NodeName, Alg: h.Alg, PublicKey: h.PubKey, Reachable: true}
                        ps.Upsert(pm)
                        ps.Touch(pid, s.RemoteAddr().String(), time.Now())
                        if string(localID) != "" { ps.AddConnectedDirect(localID, pid) }
                    }
                } else {
                    zap.L().Warn("hello verify failed", zap.Error(err))
                }
            }
        }
    } else {
        zap.L().Info("non-proto first message", zap.Int("bytes", len(first)))
    }

    // Optional pipeline is constructed externally; we detect it from context via r type assert if needed.
    var p *pipeline.Pipeline
    if v, ok := pl.(*pipeline.Pipeline); ok { p = v }
    // Continue to read and enqueue/forward messages
    for {
        buf, err := st.RecvBytes()
        if err != nil { zap.L().Warn("recv", zap.Error(err)); return }
        if ps != nil { ps.RecordExchange(s.Peer().ID, uint64(len(buf)), 0, 1, 0) }
        // try decode as proto Envelope then JSON body
        var m ttmeshproto.Envelope
        if proto.Unmarshal(buf, &m) == nil {
            // Forward if destination is set and not us
            if hdr := m.GetHeader(); hdr != nil && hdr.GetDest() != nil {
                dst := hdr.GetDest().GetId()
                if dst != "" && string(localID) != "" && dst != string(localID) && r != nil {
                    // Enqueue to pipeline if present, otherwise forward inline
                    if p != nil {
                        p.EnqueueProto(s.Peer().ID, &m, buf)
                        zap.L().Debug("enqueued for forward", zap.String("dst", dst))
                        continue
                    }
                    if err := r.SendBytesToPeer(ctx, transport.PeerID(dst), buf); err == nil {
                        zap.L().Info("forwarded", zap.String("dst", dst))
                        continue
                    } else {
                        zap.L().Warn("forward failed", zap.String("dst", dst), zap.Error(err))
                    }
                }
            }
            // If control requests
            if ctrl := m.GetControl(); ctrl != nil {
                if ctrl.GetGetRoutes() != nil {
                    // Build reply snapshot
                    rep := buildRoutesReply(ps)
                    env := &ttmeshproto.Envelope{Header: &ttmeshproto.Header{Version: 1, Type: ttmeshproto.MessageType_MT_CONTROL}, Body: &ttmeshproto.Envelope_Control{Control: &ttmeshproto.Control{Kind: &ttmeshproto.Control_RoutesReply{RoutesReply: rep}}}}
                    if b, err := proto.Marshal(env); err == nil {
                        _ = st.SendBytes(b)
                        continue
                    }
                }
                if ctrl.GetGetIdentity() != nil {
                    rep := &ttmeshproto.IdentityReply{Id: string(localID), NodeName: nodeName, Alg: "ed25519", Pubkey: pubKey}
                    env := &ttmeshproto.Envelope{Header: &ttmeshproto.Header{Version: 1, Type: ttmeshproto.MessageType_MT_CONTROL}, Body: &ttmeshproto.Envelope_Control{Control: &ttmeshproto.Control{Kind: &ttmeshproto.Control_IdentityReply{IdentityReply: rep}}}}
                    if b, err := proto.Marshal(env); err == nil {
                        _ = st.SendBytes(b)
                        continue
                    }
                }
            }
            if raw := m.GetRaw(); raw != nil {
                var anymap map[string]any
                if json.Unmarshal(raw, &anymap) == nil {
                    jb, _ := json.Marshal(anymap)
                    zap.L().Info("inbound msg", zap.String("json", string(jb)))
                    continue
                }
            }
            zap.L().Info("inbound proto msg", zap.String("type", m.GetHeader().GetType().String()))
            continue
        }
        // fallback
        var anymap map[string]any
        if json.Unmarshal(buf, &anymap) == nil {
            jb, _ := json.Marshal(anymap)
            zap.L().Info("inbound json", zap.String("json", string(jb)))
        } else {
            zap.L().Info("inbound bytes", zap.Int("len", len(buf)))
        }
    }
}

func buildRoutesReply(ps *peers.Store) *ttmeshproto.RoutesReply {
    out := &ttmeshproto.RoutesReply{}
    if ps == nil { return out }
    // peers
    ids := ps.ListPeerIDs()
    for _, id := range ids {
        pm, ok := ps.Get(id)
        if !ok { continue }
        out.Peers = append(out.Peers, &ttmeshproto.PeerMeta{Id: string(pm.ID), NodeName: pm.NodeName, Alg: pm.Alg, Pubkey: pm.PublicKey, Addresses: pm.Addresses, Reachable: pm.Reachable, Labels: pm.Labels})
        for _, to := range pm.ConnectedDirectIDs {
            if to == string(pm.ID) { continue }
            out.Adjacency = append(out.Adjacency, &ttmeshproto.PeerAdj{From: string(pm.ID), To: to})
        }
    }
    // route targets and candidates
    targets := ps.ListRouteTargets()
    out.Targets = append(out.Targets, targets...)
    for _, t := range targets {
        cands := ps.GetRouteCandidates(t)
        for _, c := range cands {
            out.Candidates = append(out.Candidates, &ttmeshproto.RouteCandidatePB{
                Target: t, Path: c.Path, NextHop: c.NextHop, Metric: c.Metric, Source: c.Source, LearnedFrom: c.LearnedFrom, Health: c.Health, UpdatedUnixMs: c.Updated,
            })
        }
    }
    return out
}
