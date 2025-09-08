package codec

import (
    "fmt"

    "google.golang.org/protobuf/proto"
)

type protoCodec struct {
    mo proto.MarshalOptions
    uo proto.UnmarshalOptions
}

// Proto returns a Protocol Buffers codec with deterministic marshaling.
// Content-Type: application/x-protobuf
func Proto() Codec {
    return protoCodec{
        mo: proto.MarshalOptions{Deterministic: true},
        uo: proto.UnmarshalOptions{},
    }
}

// Protobuf returns a deterministic Protocol Buffers codec.
func Protobuf() (Codec, error) {
    return protoCodec{
        mo: proto.MarshalOptions{Deterministic: true},
        uo: proto.UnmarshalOptions{},
    }, nil
}

func (p protoCodec) ContentType() string { return "application/x-protobuf" }

func (p protoCodec) Marshal(v any) ([]byte, error) {
    msg, ok := v.(proto.Message)
    if !ok {
        return nil, fmt.Errorf("protobuf: value does not implement proto.Message: %T", v)
    }
    return p.mo.Marshal(msg)
}

func (p protoCodec) Unmarshal(data []byte, v any) error {
    msg, ok := v.(proto.Message)
    if !ok {
        return fmt.Errorf("protobuf: target does not implement proto.Message: %T", v)
    }
    return p.uo.Unmarshal(data, msg)
}
