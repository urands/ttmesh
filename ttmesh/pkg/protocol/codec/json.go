package codec

import (
    "encoding/json"
)

type jsonCodec struct{}

// JSON returns a JSON codec (RFC 8259). Content-Type: application/json
func JSON() Codec { return jsonCodec{} }

func (jsonCodec) ContentType() string { return "application/json" }
func (jsonCodec) Marshal(v any) ([]byte, error) { return json.Marshal(v) }
func (jsonCodec) Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

