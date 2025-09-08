package protocol

import (
    "bytes"
    "testing"
)

func TestHeaderRoundtrip(t *testing.T) {
    var h Header
    h.Version = 1
    h.Type = MsgTask
    h.Flags = FlagCompressed | FlagAck
    h.Priority = 7
    h.PayloadLen = 1234
    for i := 0; i < len(h.Correlation); i++ { h.Correlation[i] = byte(i) }
    h.Source = 0x1122334455667788
    h.Dest = 0x8877665544332211
    h.DagID = 0x0102030405060708
    h.DagStep = 42
    h.FragIndex = 2
    h.FragTotal = 5

    b, err := h.MarshalBinary()
    if err != nil { t.Fatalf("marshal: %v", err) }
    if len(b) != headerSize { t.Fatalf("header size = %d", len(b)) }

    var h2 Header
    if err := h2.UnmarshalBinary(b); err != nil { t.Fatalf("unmarshal: %v", err) }

    if h2.Version != h.Version || h2.Type != h.Type || h2.Flags != h.Flags ||
        h2.Priority != h.Priority || h2.PayloadLen != h.PayloadLen ||
        !bytes.Equal(h2.Correlation[:], h.Correlation[:]) ||
        h2.Source != h.Source || h2.Dest != h.Dest || h2.DagID != h.DagID ||
        h2.DagStep != h.DagStep || h2.FragIndex != h.FragIndex || h2.FragTotal != h.FragTotal {
        t.Fatalf("headers differ: %#v vs %#v", h2, h)
    }
}

