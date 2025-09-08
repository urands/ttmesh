package protocol

import (
    "bytes"
    "testing"
    "ttmesh/pkg/protocol/codec"
    "google.golang.org/protobuf/types/known/structpb"
)

func TestEncodeDecodeBodyJSON(t *testing.T) {
    reg := codec.NewRegistry()
    // no registration => default JSON used
    in := map[string]any{"x": 1, "y": "z"}
    b, err := EncodeBody(reg, FormatJSON, in)
    if err != nil { t.Fatalf("encode: %v", err) }
    if b[0] != byte(FormatJSON) { t.Fatalf("format prefix mismatch") }
    var out map[string]any
    f, err := DecodeBody(reg, b, &out)
    if err != nil { t.Fatalf("decode: %v", err) }
    if f != FormatJSON { t.Fatalf("format mismatch") }
}

func TestEncodeDecodeBodyCBOR(t *testing.T) {
    reg := codec.NewRegistry()
    c, err := codec.CBOR()
    if err != nil { t.Fatalf("cbor: %v", err) }
    reg.Register(c)
    buf := bytes.Repeat([]byte{0xAA}, 16)
    in := map[string]any{"buf": buf}
    b, err := EncodeBody(reg, FormatCBOR, in)
    if err != nil { t.Fatalf("encode: %v", err) }
    var out map[string]any
    if _, err := DecodeBody(reg, b, &out); err != nil { t.Fatalf("decode: %v", err) }
}

func TestEncodeDecodeBodyProto(t *testing.T) {
    reg := codec.NewRegistry()
    reg.Register(codec.Proto())
    s, err := structpb.NewStruct(map[string]any{"k":"v"})
    if err != nil { t.Fatalf("struct: %v", err) }
    b, err := EncodeBody(reg, FormatProto, s)
    if err != nil { t.Fatalf("encode: %v", err) }
    var out structpb.Struct
    if _, err := DecodeBody(reg, b, &out); err != nil { t.Fatalf("decode: %v", err) }
    if out.Fields["k"].GetStringValue() != "v" { t.Fatalf("value mismatch") }
}

