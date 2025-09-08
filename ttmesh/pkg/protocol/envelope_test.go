package protocol

import (
    "bytes"
    "testing"
)

func TestEnvelopeFrameEncodeDecode(t *testing.T) {
    corr, _ := NewCorrelation()
    e := Envelope{Header: Header{
        Version:  1,
        Type:     MsgControl,
        Flags:    FlagAck,
        Priority: 3,
        Source:   1,
        Dest:     2,
        DagID:    10,
        DagStep:  1,
        FragIndex: 0,
        FragTotal: 0,
        Correlation: corr,
    }}
    e.Payload = []byte("hello")

    frame, err := e.EncodeFrame()
    if err != nil { t.Fatalf("encode: %v", err) }

    var d Envelope
    if err := d.DecodeFrame(frame); err != nil { t.Fatalf("decode: %v", err) }

    if !bytes.Equal(d.Payload, e.Payload) { t.Fatalf("payload mismatch") }
    if d.Header.Type != e.Header.Type || d.Header.Flags != e.Header.Flags || d.Header.Source != e.Header.Source || d.Header.Dest != e.Header.Dest {
        t.Fatalf("header mismatch")
    }
}

func TestFragmentsAndReassemble(t *testing.T) {
    corr, _ := NewCorrelation()
    e := Envelope{Header: Header{Version:1, Type:MsgTask, Correlation: corr}}
    data := bytes.Repeat([]byte{0xAB}, 1024)
    e.Payload = data
    frags, err := e.Fragments(128)
    if err != nil { t.Fatalf("fragments: %v", err) }
    if len(frags) != 8 { t.Fatalf("want 8 frags, got %d", len(frags)) }
    for i, f := range frags {
        if f.Header.FragIndex != uint16(i) { t.Fatalf("frag index mismatch") }
        if f.Header.FragTotal != uint16(len(frags)) { t.Fatalf("frag total mismatch") }
        if i == len(frags)-1 && (f.Header.Flags&FlagLastFrag) == 0 { t.Fatalf("last flag not set") }
    }
    re, err := Reassemble(frags)
    if err != nil { t.Fatalf("reassemble: %v", err) }
    if !bytes.Equal(re.Payload, data) { t.Fatalf("reassembled payload mismatch") }
}

