package api

import "ttmesh/pkg/protocol"

// Re-export common protocol-level types that are used by API consumers.
type (
    Envelope = protocol.Envelope
    Header   = protocol.Header
)
