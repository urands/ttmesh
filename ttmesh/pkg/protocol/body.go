package protocol

import (
    "fmt"
    "ttmesh/pkg/protocol/codec"
)

// Format is a compact on-wire indicator of payload encoding.
// It is carried as the first byte of Envelope.Payload for MsgTask/MsgResult.
type Format uint8

const (
    FormatUnknown Format = iota
    FormatJSON
    FormatCBOR
    FormatProto
)

func (f Format) String() string {
    switch f {
    case FormatJSON:
        return ContentJSON
    case FormatCBOR:
        return ContentCBOR
    case FormatProto:
        return ContentProto
    default:
        return ContentUnknown
    }
}

// CodecFor returns a codec instance for a given format.
func CodecFor(r *codec.Registry, f Format) (codec.Codec, error) {
    switch f {
    case FormatJSON:
        if c := r.Get(ContentJSON); c != nil { return c, nil }
        return codec.JSON(), nil
    case FormatCBOR:
        if c := r.Get(ContentCBOR); c != nil { return c, nil }
        return codec.CBOR()
    case FormatProto:
        if c := r.Get(ContentProto); c != nil { return c, nil }
        return codec.Proto(), nil
    default:
        return nil, fmt.Errorf("unknown format: %d", f)
    }
}

// EncodeBody serializes v using the codec for f and prefixes the payload
// with a single format byte.
func EncodeBody(r *codec.Registry, f Format, v any) ([]byte, error) {
    c, err := CodecFor(r, f)
    if err != nil { return nil, err }
    b, err := c.Marshal(v)
    if err != nil { return nil, err }
    out := make([]byte, 1+len(b))
    out[0] = byte(f)
    copy(out[1:], b)
    return out, nil
}

// DecodeBody decodes payload produced by EncodeBody into v.
func DecodeBody(r *codec.Registry, payload []byte, v any) (Format, error) {
    if len(payload) == 0 { return FormatUnknown, fmt.Errorf("empty payload") }
    f := Format(payload[0])
    c, err := CodecFor(r, f)
    if err != nil { return f, err }
    if err := c.Unmarshal(payload[1:], v); err != nil { return f, err }
    return f, nil
}

