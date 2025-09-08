package api

import (
    "context"
    "ttmesh/pkg/protocol"
)

// Transport abstracts a bidirectional channel that exchanges protocol.Envelope frames.
type Transport interface {
    // Exchange opens a duplex stream. The implementation owns goroutines for I/O.
    Exchange(ctx context.Context) (Stream, error)
}

// Stream sends and receives envelopes. Implementations must be safe for use by
// a single reader and writer goroutine.
type Stream interface {
    Send(*protocol.Envelope) error
    Recv(*protocol.Envelope) error
    Close() error
}
