package codec

import (
    "testing"
    "google.golang.org/protobuf/types/known/structpb"
)

func TestJSONCodec(t *testing.T) {
    c := JSON()
    in := map[string]any{"a": 1, "b": "x"}
    b, err := c.Marshal(in)
    if err != nil { t.Fatalf("marshal: %v", err) }
    var out map[string]any
    if err := c.Unmarshal(b, &out); err != nil { t.Fatalf("unmarshal: %v", err) }
    if out["a"].(float64) != 1 || out["b"].(string) != "x" {
        t.Fatalf("roundtrip mismatch: %#v", out)
    }
}

func TestCBORCodec(t *testing.T) {
    c, err := CBOR()
    if err != nil { t.Fatalf("new cbor: %v", err) }
    in := map[string]any{"n": 42}
    b, err := c.Marshal(in)
    if err != nil { t.Fatalf("marshal: %v", err) }
    var out map[string]any
    if err := c.Unmarshal(b, &out); err != nil { t.Fatalf("unmarshal: %v", err) }
    if int(out["n"].(uint64)) != 42 && int(out["n"].(float64)) != 42 { // decoder may choose num type
        t.Fatalf("roundtrip mismatch: %#v", out)
    }
}

func TestProtoCodec(t *testing.T) {
    c := Proto()
    s, err := structpb.NewStruct(map[string]any{"k": "v"})
    if err != nil { t.Fatalf("struct: %v", err) }
    b, err := c.Marshal(s)
    if err != nil { t.Fatalf("marshal: %v", err) }
    var out structpb.Struct
    if err := c.Unmarshal(b, &out); err != nil { t.Fatalf("unmarshal: %v", err) }
    if out.Fields["k"].GetStringValue() != "v" { t.Fatalf("roundtrip mismatch") }
}

