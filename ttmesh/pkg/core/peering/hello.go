package peering

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	"ttmesh/pkg/handshake"
	"ttmesh/pkg/peers"
	ttmeshproto "ttmesh/pkg/protocol/proto"
	"ttmesh/pkg/transport"
)

// handleHello verifies Control.Hello, rebinds peer id, updates store, and sends HelloAck.
// Returns the new peer id if rebound.
func handleHello(ctx context.Context, mgr *transport.Manager, ps *peers.Store, localID transport.PeerID, s transport.Session, st transport.Stream, nodeName string, pubKey []byte, hello *ttmeshproto.PeerHello) (transport.PeerID, bool) {
    h := handshake.Hello{
        Version:   1,
        NodeName:  hello.GetNodeName(),
        Alg:       hello.GetAlg(),
        PubKey:    append([]byte(nil), hello.GetPubkey()...),
        Nonce:     append([]byte(nil), hello.GetNonce()...),
        Timestamp: hello.GetTsUnixMs(),
        Sig:       append([]byte(nil), hello.GetSig()...),
    }
    pid, err := handshake.VerifyHello(h, 0)
    if err != nil {
        zap.L().Warn("hello verify failed", zap.Error(err))
        return "", false
    }
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
    // Build and send HelloAck
    ack := &ttmeshproto.PeerHelloAck{Accepted: true, PeerId: string(pid), Meta: &ttmeshproto.PeerMeta{
        Id:          string(localID),
        NodeName:    nodeName,
        Alg:         "ed25519",
        Pubkey:      pubKey,
        Reachable:   true,
        LastHelloTs: h.Timestamp,
        LastAckTs:   time.Now().UnixMilli(),
        Handshake:   ttmeshproto.HandshakeStatus_HS_VERIFIED,
    }}
    env := &ttmeshproto.Envelope{Header: &ttmeshproto.Header{Version: 1, Type: ttmeshproto.MessageType_MT_CONTROL}, Body: &ttmeshproto.Envelope_Control{Control: &ttmeshproto.Control{Kind: &ttmeshproto.Control_HelloAck{HelloAck: ack}}}}
    // Signal immediate direct OK to the initiator via header flag.
    // env.Header.DirectStatus = ttmeshproto.Status_ST_OK.Enum()
    if b, err := proto.Marshal(env); err == nil {
        _ = st.SendBytes(b)
        if ps != nil { ps.RecordExchange(pid, 0, uint64(len(b)), 0, 1) }
    }
    return pid, true
}

// handleHelloAck processes Control.HelloAck for the initiator. Rebinds to
// responder's canonical id and updates the store. Returns new id if rebound.
func handleHelloAck(ctx context.Context, mgr *transport.Manager, ps *peers.Store, localID transport.PeerID, s transport.Session, ack *ttmeshproto.PeerHelloAck) (transport.PeerID, bool) {
    var metaID string
    if ack.GetMeta() != nil { metaID = ack.GetMeta().GetId() }
    newID := transport.PeerID(metaID)
    if newID == "" { return "", false }
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
    return newID, true
}

// handleRawHello handles backward-compatible raw JSON Hello.
func handleRawHello(ctx context.Context, mgr *transport.Manager, ps *peers.Store, localID transport.PeerID, s transport.Session, raw []byte) (transport.PeerID, bool) {
    var h handshake.Hello
    if json.Unmarshal(raw, &h) != nil || h.Alg == "" { return "", false }
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
        return pid, true
    }
    return "", false
}
