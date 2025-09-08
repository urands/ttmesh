package protocol

// Message types (fits in uint8, mirrored in proto type field if used)
const (
    MsgUnknown uint8 = iota
    MsgTask           // task submission
    MsgResult         // task result/response
    MsgControl        // control/management
    MsgHeartbeat      // liveness ping
    MsgRoute          // routing/tunnel control info
    MsgDagFragment    // DAG node payload fragment
)

// Flags bitmask (uint32)
const (
    FlagCompressed uint32 = 1 << 0 // payload compressed
    FlagEncrypted  uint32 = 1 << 1 // payload encrypted
    FlagAck        uint32 = 1 << 2 // ack requested
    FlagStream     uint32 = 1 << 3 // streaming payload
    FlagFragment   uint32 = 1 << 4 // this envelope is a fragment
    FlagLastFrag   uint32 = 1 << 5 // last fragment
    FlagTunnel     uint32 = 1 << 6 // requires/through tunnel
)

// ContentType is optional hint for payload decoding.
// Kept as constants to avoid coupling; not serialized in header.
const (
    ContentUnknown = "application/octet-stream"
    ContentCBOR    = "application/cbor"
    ContentJSON    = "application/json"
    ContentProto   = "application/x-protobuf"
)

