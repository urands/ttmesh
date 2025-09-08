package protocol

import "ttmesh/pkg/protocol/codec"

// NewEnvelopeWithBody encodes v according to format and returns an Envelope
// with header set to h and payload set to encoded body (with format prefix).
func NewEnvelopeWithBody(h Header, format Format, v any, reg *codec.Registry) (Envelope, error) {
    b, err := EncodeBody(reg, format, v)
    if err != nil { return Envelope{}, err }
    e := Envelope{Header: h, Payload: b}
    e.Header.PayloadLen = uint32(len(b))
    return e, nil
}

// DecodeEnvelopeBody decodes the payload of e into v using the embedded format
// marker. Returns the detected format.
func DecodeEnvelopeBody(e *Envelope, v any, reg *codec.Registry) (Format, error) {
    return DecodeBody(reg, e.Payload, v)
}

