package protocol

import (
    "crypto/rand"
    "encoding/binary"
    "fmt"
    "io"
)

// Envelope is a header + payload wrapper for a single channel transfer.
type Envelope struct {
    Header  Header
    Payload []byte
}

// NewCorrelation generates a random 16-byte id.
func NewCorrelation() (out [16]byte, err error) {
    _, err = io.ReadFull(rand.Reader, out[:])
    return
}

// HasFlag checks whether a flag is set.
func (e *Envelope) HasFlag(flag uint32) bool { return (e.Header.Flags & flag) != 0 }

// SetFlag sets/unsets a flag.
func (e *Envelope) SetFlag(flag uint32, on bool) {
    if on {
        e.Header.Flags |= flag
    } else {
        e.Header.Flags &^= flag
    }
}

// WriteTo writes header + payload to w.
func (e *Envelope) WriteTo(w io.Writer) (int64, error) {
    e.Header.PayloadLen = uint32(len(e.Payload))
    hb, err := e.Header.MarshalBinary()
    if err != nil {
        return 0, err
    }
    n1, err := w.Write(hb)
    if err != nil {
        return int64(n1), err
    }
    n2, err := w.Write(e.Payload)
    return int64(n1 + n2), err
}

// ReadFrom reads header + payload from r.
func (e *Envelope) ReadFrom(r io.Reader) (int64, error) {
    hb := make([]byte, headerSize)
    if _, err := io.ReadFull(r, hb); err != nil {
        return 0, err
    }
    if err := e.Header.UnmarshalBinary(hb); err != nil {
        return 0, err
    }
    if e.Header.PayloadLen > 0 {
        if e.Header.PayloadLen > (1<<31) { // guard against absurd sizes
            return 0, fmt.Errorf("payload too large: %d", e.Header.PayloadLen)
        }
        e.Payload = make([]byte, int(e.Header.PayloadLen))
        if _, err := io.ReadFull(r, e.Payload); err != nil {
            return 0, err
        }
    } else {
        e.Payload = nil
    }
    return int64(headerSize + int(e.Header.PayloadLen)), nil
}

// EncodeFrame returns header+payload as a single byte slice.
func (e *Envelope) EncodeFrame() ([]byte, error) {
    e.Header.PayloadLen = uint32(len(e.Payload))
    hb, err := e.Header.MarshalBinary()
    if err != nil { return nil, err }
    out := make([]byte, headerSize+len(e.Payload))
    copy(out, hb)
    copy(out[headerSize:], e.Payload)
    return out, nil
}

// DecodeFrame parses a single frame from buf.
func (e *Envelope) DecodeFrame(buf []byte) error {
    if len(buf) < headerSize {
        return io.ErrUnexpectedEOF
    }
    if err := e.Header.UnmarshalBinary(buf[:headerSize]); err != nil {
        return err
    }
    need := int(e.Header.PayloadLen)
    if headerSize+need > len(buf) {
        return io.ErrUnexpectedEOF
    }
    e.Payload = append(e.Payload[:0], buf[headerSize:headerSize+need]...)
    return nil
}

// Fragments splits the payload into chunks and yields envelopes.
func (e *Envelope) Fragments(chunk int) ([]Envelope, error) {
    if chunk <= 0 {
        return nil, fmt.Errorf("invalid chunk size")
    }
    data := e.Payload
    total := (len(data) + chunk - 1) / chunk
    if total <= 1 {
        return []Envelope{*e}, nil
    }
    out := make([]Envelope, 0, total)
    for i := 0; i < total; i++ {
        start := i * chunk
        end := start + chunk
        if end > len(data) { end = len(data) }
        ne := Envelope{Header: e.Header}
        ne.Payload = append([]byte(nil), data[start:end]...)
        ne.Header.FragIndex = uint16(i)
        ne.Header.FragTotal = uint16(total)
        ne.Header.Flags |= FlagFragment
        if i == total-1 { ne.Header.Flags |= FlagLastFrag }
        out = append(out, ne)
    }
    return out, nil
}

// Reassemble attempts to merge fragments into a single payload.
// The caller is responsible for ordering by FragIndex.
func Reassemble(frags []Envelope) (Envelope, error) {
    if len(frags) == 0 {
        return Envelope{}, fmt.Errorf("no fragments")
    }
    base := frags[0]
    var totalLen int
    for _, f := range frags {
        totalLen += int(f.Header.PayloadLen)
    }
    buf := make([]byte, 0, totalLen)
    for _, f := range frags {
        buf = append(buf, f.Payload...)
    }
    base.Payload = buf
    base.Header.Flags &^= (FlagFragment | FlagLastFrag)
    base.Header.FragIndex, base.Header.FragTotal = 0, 0
    base.Header.PayloadLen = uint32(len(buf))
    return base, nil
}

// EncodeUint64 writes u64 to b at off (for helpers/testing)
func EncodeUint64(b []byte, off int, v uint64) { binary.LittleEndian.PutUint64(b[off:off+8], v) }

