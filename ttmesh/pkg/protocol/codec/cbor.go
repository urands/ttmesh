package codec

import (
    cbor "github.com/fxamacker/cbor/v2"
)

type cborCodec struct{ enc cbor.EncMode; dec cbor.DecMode }

// CBOR returns a deterministic CBOR codec (RFC 7049/8949) with core profile.
func CBOR() (Codec, error) {
    em, err := cbor.CanonicalEncOptions().EncMode()
    if err != nil { return nil, err }
    dm, err := cbor.DecOptions{}.DecMode()
    if err != nil { return nil, err }
    return cborCodec{enc: em, dec: dm}, nil
}

func (c cborCodec) ContentType() string { return "application/cbor" }
func (c cborCodec) Marshal(v any) ([]byte, error) { return c.enc.Marshal(v) }
func (c cborCodec) Unmarshal(data []byte, v any) error { return c.dec.Unmarshal(data, v) }
