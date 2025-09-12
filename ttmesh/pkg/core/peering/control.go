package peering

import (
    "ttmesh/pkg/peers"
    ttmeshproto "ttmesh/pkg/protocol/proto"
    "ttmesh/pkg/registry"
    "ttmesh/pkg/transport"
)

// handleControlRequest processes known control requests and writes replies to the stream.
// Returns true if a reply was sent and processing can continue to next message.
func handleControlRequest(st transport.Stream, ps *peers.Store, reg *registry.Store, localID string, nodeName string, pubKey []byte, ctrl *ttmeshproto.Control) bool {
    // Routes snapshot
    if ctrl.GetGetRoutes() != nil {
        rep := buildRoutesReply(ps)
        env := &ttmeshproto.Envelope{Header: &ttmeshproto.Header{Version: 1, Type: ttmeshproto.MessageType_MT_CONTROL}, Body: &ttmeshproto.Envelope_Control{Control: &ttmeshproto.Control{Kind: &ttmeshproto.Control_RoutesReply{RoutesReply: rep}}}}
        return sendProto(st, env)
    }
    // Identity reply
    if ctrl.GetGetIdentity() != nil {
        rep := &ttmeshproto.IdentityReply{Id: localID, NodeName: nodeName, Alg: "ed25519", Pubkey: pubKey}
        env := &ttmeshproto.Envelope{Header: &ttmeshproto.Header{Version: 1, Type: ttmeshproto.MessageType_MT_CONTROL}, Body: &ttmeshproto.Envelope_Control{Control: &ttmeshproto.Control{Kind: &ttmeshproto.Control_IdentityReply{IdentityReply: rep}}}}
        return sendProto(st, env)
    }
    // Task capability registration
    if tr := ctrl.GetTaskRegister(); tr != nil && reg != nil {
        ok, reason, conflicts := reg.Register(tr)
        ack := &ttmeshproto.TaskRegisterAck{Accepted: ok, Reason: reason, Conflicts: conflicts}
        env := &ttmeshproto.Envelope{Header: &ttmeshproto.Header{Version: 1, Type: ttmeshproto.MessageType_MT_CONTROL}, Body: &ttmeshproto.Envelope_Control{Control: &ttmeshproto.Control{Kind: &ttmeshproto.Control_TaskRegisterAck{TaskRegisterAck: ack}}}}
        return sendProto(st, env)
    }
    if tu := ctrl.GetTaskUpdate(); tu != nil && reg != nil {
        reg.Update(tu)
        // fire-and-forget; no explicit ack defined
        return false
    }
    if td := ctrl.GetTaskDeregister(); td != nil && reg != nil {
        reg.Deregister(td)
        return false
    }
    if tl := ctrl.GetTaskListWorkers(); tl != nil && reg != nil {
        rep := reg.ListWorkers(tl)
        env := &ttmeshproto.Envelope{Header: &ttmeshproto.Header{Version: 1, Type: ttmeshproto.MessageType_MT_CONTROL}, Body: &ttmeshproto.Envelope_Control{Control: &ttmeshproto.Control{Kind: &ttmeshproto.Control_TaskListWorkersReply{TaskListWorkersReply: rep}}}}
        return sendProto(st, env)
    }
    return false
}
