package api

import "ttmesh/pkg/protocol"

// Format is an alias to protocol.Format for API ergonomics.
type Format = protocol.Format

// TaskRequest describes an incoming task to be scheduled or executed.
// Payload is serialized according to Format.
type TaskRequest struct {
    ID       string            // client-provided id (optional)
    Name     string            // task kind or handler name
    Format   Format            // serialization format of payload
    Payload  []byte            // raw payload bytes
    Meta     map[string]string // optional metadata/headers
    Priority uint8             // 0..255
}

// TaskResult is the response produced by an executor.
type TaskResult struct {
    Correlation [16]byte       // correlation id matching request
    Format      Format         // serialization format of payload
    Payload     []byte         // raw result payload
    Meta        map[string]string
    Err         string         // optional textual error
    Final       bool           // true if this is the last chunk (for streams)
}

// Message is a typed payload a developer may use before serialization.
// Use protocol/codec to marshal/unmarshal into TaskRequest/TaskResult Payload.
type Message any
