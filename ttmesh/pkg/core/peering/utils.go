package peering

import (
    "encoding/json"
    "time"

    "go.uber.org/zap"
    "google.golang.org/protobuf/proto"

    ttmeshproto "ttmesh/pkg/protocol/proto"
    "ttmesh/pkg/transport"
)

func nowMs() int64 { return time.Now().UnixMilli() }

// sendProto marshals and writes an Envelope to the control stream.
func sendProto(st transport.Stream, env *ttmeshproto.Envelope) bool {
    b, err := proto.Marshal(env)
    if err != nil { return false }
    if err := st.SendBytes(b); err != nil { return false }
    return true
}

// tryJSON logs JSON content if buf is JSON, returns true if handled.
func tryJSONLog(prefix string, buf []byte) bool {
    var anymap map[string]any
    if json.Unmarshal(buf, &anymap) == nil {
        jb, _ := json.Marshal(anymap)
        zap.L().Info(prefix, zap.String("json", string(jb)))
        return true
    }
    return false
}

// enqueueDirectStatusAck builds a tiny header-only envelope with direct_status
// and enqueues it to the forwarder towards the previous hop (sender).
func enqueueDirectStatusAck(fwd protoForwarder, localID transport.PeerID, prevHop transport.PeerID, orig *ttmeshproto.Envelope, status ttmeshproto.Status) {
    if fwd == nil || string(localID) == "" {
        return
    }
    // Always reply hop-by-hop to the previous transport peer to avoid routing lookups.
    destID := string(prevHop)
    if destID == "" {
        return
    }
    h := &ttmeshproto.Header{
        Version:       1,
        Type:          ttmeshproto.MessageType_MT_CONTROL,
        Source:        &ttmeshproto.NodeRef{Id: string(localID)},
        Dest:          &ttmeshproto.NodeRef{Id: destID},
        CreatedUnixMs: nowMs(),
        DirectStatus:  status.Enum(),
    }
    // Carry correlation/message ids for easy matching if present
    if orig != nil && orig.Header != nil {
        if cid := orig.Header.GetCorrelationId(); len(cid) > 0 { h.CorrelationId = cid }
        if mid := orig.Header.GetMessageId(); len(mid) > 0 { h.MessageId = mid }
        if pr := orig.Header.GetPriority(); pr != 0 { h.Priority = pr }
    }
    env := &ttmeshproto.Envelope{Header: h}
    b, err := proto.Marshal(env)
    if err != nil { return }
    // use localID as src marker for metrics
    fwd.EnqueueProto(localID, env, b)
}
