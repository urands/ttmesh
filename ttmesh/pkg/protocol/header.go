package protocol

import (
    "encoding/binary"
    "errors"
)

// Fixed header layout (64 bytes) for fast parsing over any channel.
// All integer fields are little-endian.
//
//  0  ..1   Magic   'T''M' (0x544d)
//  2        Version u8
//  3        Type    u8
//  4  ..7   Flags   u32
//  8        Priority u8
//  9        Reserved u8
//  10 ..13  PayloadLen u32
//  14 ..29  CorrelationID [16]byte
//  30 ..37  Source u64
//  38 ..45  Dest   u64
//  46 ..53  DagID  u64
//  54 ..57  DagStep u32
//  58 ..59  FragTotal u16
//  60 ..61  FragIndex u16
//  62 ..63  Reserved2 u16
const (
    headerSize = 64
    magicWord  = uint16(0x544d) // 'T''M'
)

// Header describes metadata for an envelope.
type Header struct {
    Version      uint8
    Type         uint8
    Flags        uint32
    Priority     uint8
    PayloadLen   uint32
    Correlation  [16]byte
    Source       uint64
    Dest         uint64
    DagID        uint64
    DagStep      uint32
    FragTotal    uint16
    FragIndex    uint16
}

// MarshalBinary encodes header to 64-byte buffer.
func (h *Header) MarshalBinary() ([]byte, error) {
    buf := make([]byte, headerSize)
    binary.LittleEndian.PutUint16(buf[0:2], magicWord)
    buf[2] = h.Version
    buf[3] = h.Type
    binary.LittleEndian.PutUint32(buf[4:8], h.Flags)
    buf[8] = h.Priority
    // buf[9] reserved
    binary.LittleEndian.PutUint32(buf[10:14], h.PayloadLen)
    copy(buf[14:30], h.Correlation[:])
    binary.LittleEndian.PutUint64(buf[30:38], h.Source)
    binary.LittleEndian.PutUint64(buf[38:46], h.Dest)
    binary.LittleEndian.PutUint64(buf[46:54], h.DagID)
    binary.LittleEndian.PutUint32(buf[54:58], h.DagStep)
    binary.LittleEndian.PutUint16(buf[58:60], h.FragTotal)
    binary.LittleEndian.PutUint16(buf[60:62], h.FragIndex)
    // 62..63 reserved2 stays zero
    return buf, nil
}

// UnmarshalBinary decodes header from 64-byte buffer.
func (h *Header) UnmarshalBinary(buf []byte) error {
    if len(buf) < headerSize {
        return errors.New("short header")
    }
    if binary.LittleEndian.Uint16(buf[0:2]) != magicWord {
        return errors.New("bad magic")
    }
    h.Version = buf[2]
    h.Type = buf[3]
    h.Flags = binary.LittleEndian.Uint32(buf[4:8])
    h.Priority = buf[8]
    h.PayloadLen = binary.LittleEndian.Uint32(buf[10:14])
    copy(h.Correlation[:], buf[14:30])
    h.Source = binary.LittleEndian.Uint64(buf[30:38])
    h.Dest = binary.LittleEndian.Uint64(buf[38:46])
    h.DagID = binary.LittleEndian.Uint64(buf[46:54])
    h.DagStep = binary.LittleEndian.Uint32(buf[54:58])
    h.FragTotal = binary.LittleEndian.Uint16(buf[58:60])
    h.FragIndex = binary.LittleEndian.Uint16(buf[60:62])
    return nil
}
