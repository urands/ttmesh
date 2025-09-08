// Package grpcgw adapts internal protocol.Envelope streams to the gRPC Mesh.Exchange API.
package grpcgw

import (
    "context"
    "fmt"
    "strconv"

    "ttmesh/pkg/api"
    "ttmesh/pkg/protocol"
    "ttmesh/pkg/protocol/codec"
    ttmeshproto "ttmesh/pkg/protocol/proto"
)

// ToProtoEnvelope converts internal Envelope to gRPC proto Envelope.
// It maps header fields and puts the raw payload into the oneof body as bytes.
func ToProtoEnvelope(e *protocol.Envelope) (*ttmeshproto.Envelope, error) {
    if e == nil { return nil, nil }

    h := &ttmeshproto.Header{
        Version:       uint32(e.Header.Version),
        Type:          ttmeshproto.MessageType(e.Header.Type),
        Flags:         e.Header.Flags,
        Priority:      uint32(e.Header.Priority),
        CorrelationId: append([]byte(nil), e.Header.Correlation[:]...),
        Source:        &ttmeshproto.NodeRef{Id: u64ToStr(e.Header.Source)},
        Dest:          &ttmeshproto.NodeRef{Id: u64ToStr(e.Header.Dest)},
        ReplyTo:       &ttmeshproto.NodeRef{},
        FragIndex:     uint32(e.Header.FragIndex),
        FragTotal:     uint32(e.Header.FragTotal),
    }

    // Best-effort codec hint: derive from first payload byte when present
    if len(e.Payload) > 0 {
        switch protocol.Format(e.Payload[0]) {
        case protocol.FormatProto:
            h.Codec = ttmeshproto.Codec_CODEC_PROTOBUF
        default:
            h.Codec = ttmeshproto.Codec_CODEC_RAW
        }
    }

    pe := &ttmeshproto.Envelope{Header: h}
    // Keep routing/meta empty until internal structs exist; place payload as raw bytes
    pe.Body = &ttmeshproto.Envelope_Raw{Raw: append([]byte(nil), e.Payload...)}
    return pe, nil
}

// FromProtoEnvelope converts gRPC proto Envelope to internal Envelope.
// It prefers raw body when present; otherwise it encodes known oneof bodies using Proto codec.
func FromProtoEnvelope(pe *ttmeshproto.Envelope) (protocol.Envelope, error) {
    if pe == nil { return protocol.Envelope{}, nil }
    var corr [16]byte
    if len(pe.GetHeader().GetCorrelationId()) > 0 {
        copy(corr[:], pe.GetHeader().GetCorrelationId())
    }
    h := protocol.Header{
        Version:     uint8(pe.GetHeader().GetVersion()),
        Type:        uint8(pe.GetHeader().GetType()),
        Flags:       pe.GetHeader().GetFlags(),
        Priority:    uint8(pe.GetHeader().GetPriority()),
        Correlation: corr,
        Source:      strToU64(pe.GetHeader().GetSource().GetId()),
        Dest:        strToU64(pe.GetHeader().GetDest().GetId()),
        FragTotal:   uint16(pe.GetHeader().GetFragTotal()),
        FragIndex:   uint16(pe.GetHeader().GetFragIndex()),
    }

    // Choose payload
    var payload []byte
    switch b := pe.GetBody().(type) {
    case *ttmeshproto.Envelope_Raw:
        payload = append([]byte(nil), b.Raw...)
    case *ttmeshproto.Envelope_Invoke:
        // Encode proto message with format marker
        reg := codec.NewRegistry(); reg.Register(codec.Proto())
        eb, err := protocol.EncodeBody(reg, protocol.FormatProto, b.Invoke)
        if err != nil { return protocol.Envelope{}, err }
        payload = eb
        h.Type = uint8(ttmeshproto.MessageType_MT_TASK)
    case *ttmeshproto.Envelope_Chunk:
        reg := codec.NewRegistry(); reg.Register(codec.Proto())
        eb, err := protocol.EncodeBody(reg, protocol.FormatProto, b.Chunk)
        if err != nil { return protocol.Envelope{}, err }
        payload = eb
        h.Type = uint8(ttmeshproto.MessageType_MT_DAG_FRAGMENT)
    case *ttmeshproto.Envelope_Result:
        reg := codec.NewRegistry(); reg.Register(codec.Proto())
        eb, err := protocol.EncodeBody(reg, protocol.FormatProto, b.Result)
        if err != nil { return protocol.Envelope{}, err }
        payload = eb
        h.Type = uint8(ttmeshproto.MessageType_MT_RESULT)
    case *ttmeshproto.Envelope_TinyResult:
        reg := codec.NewRegistry(); reg.Register(codec.Proto())
        eb, err := protocol.EncodeBody(reg, protocol.FormatProto, b.TinyResult)
        if err != nil { return protocol.Envelope{}, err }
        payload = eb
        h.Type = uint8(ttmeshproto.MessageType_MT_RESULT)
    case *ttmeshproto.Envelope_Ack:
        reg := codec.NewRegistry(); reg.Register(codec.Proto())
        eb, err := protocol.EncodeBody(reg, protocol.FormatProto, b.Ack)
        if err != nil { return protocol.Envelope{}, err }
        payload = eb
        h.Type = uint8(ttmeshproto.MessageType_MT_CONTROL)
    case *ttmeshproto.Envelope_Control:
        reg := codec.NewRegistry(); reg.Register(codec.Proto())
        eb, err := protocol.EncodeBody(reg, protocol.FormatProto, b.Control)
        if err != nil { return protocol.Envelope{}, err }
        payload = eb
        h.Type = uint8(ttmeshproto.MessageType_MT_CONTROL)
    case *ttmeshproto.Envelope_LeaseReq, *ttmeshproto.Envelope_LeaseRep:
        // Treat lease messages as control
        reg := codec.NewRegistry(); reg.Register(codec.Proto())
        var v any
        switch x := b.(type) {
        case *ttmeshproto.Envelope_LeaseReq:
            v = x.LeaseReq
        case *ttmeshproto.Envelope_LeaseRep:
            v = x.LeaseRep
        default:
            return protocol.Envelope{}, fmt.Errorf("unsupported lease body type")
        }
        eb, err := protocol.EncodeBody(reg, protocol.FormatProto, v)
        if err != nil { return protocol.Envelope{}, err }
        payload = eb
        h.Type = uint8(ttmeshproto.MessageType_MT_CONTROL)
    case *ttmeshproto.Envelope_SessionInit:
        reg := codec.NewRegistry(); reg.Register(codec.Proto())
        eb, err := protocol.EncodeBody(reg, protocol.FormatProto, b.SessionInit)
        if err != nil { return protocol.Envelope{}, err }
        payload = eb
        h.Type = uint8(ttmeshproto.MessageType_MT_CONTROL)
    case *ttmeshproto.Envelope_SessionClose:
        reg := codec.NewRegistry(); reg.Register(codec.Proto())
        eb, err := protocol.EncodeBody(reg, protocol.FormatProto, b.SessionClose)
        if err != nil { return protocol.Envelope{}, err }
        payload = eb
        h.Type = uint8(ttmeshproto.MessageType_MT_CONTROL)
    default:
        // Unknown/empty: no payload
    }

    e := protocol.Envelope{Header: h, Payload: payload}
    e.Header.PayloadLen = uint32(len(payload))
    return e, nil
}

// ClientTransport implements api.Transport over a gRPC Mesh client.
type ClientTransport struct {
    client ttmeshproto.MeshClient
}

func NewClientTransport(client ttmeshproto.MeshClient) *ClientTransport {
    return &ClientTransport{client: client}
}

func (t *ClientTransport) Exchange(ctx context.Context) (api.Stream, error) {
    st, err := t.client.Exchange(ctx)
    if err != nil { return nil, err }
    return &clientStream{st: st}, nil
}

// clientStream wraps Mesh_ExchangeClient to satisfy api.Stream.
type clientStream struct {
    st ttmeshproto.Mesh_ExchangeClient
}

func (s *clientStream) Send(e *protocol.Envelope) error {
    pe, err := ToProtoEnvelope(e)
    if err != nil { return err }
    return s.st.Send(pe)
}

func (s *clientStream) Recv(e *protocol.Envelope) error {
    pe, err := s.st.Recv()
    if err != nil { return err }
    ne, err := FromProtoEnvelope(pe)
    if err != nil { return err }
    *e = ne
    return nil
}

func (s *clientStream) Close() error { return s.st.CloseSend() }

// Helpers
func u64ToStr(v uint64) string { return strconv.FormatUint(v, 10) }
func strToU64(s string) uint64 {
    if s == "" { return 0 }
    v, _ := strconv.ParseUint(s, 10, 64)
    return v
}
