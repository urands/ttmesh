// Package transport defines the canonical transport interfaces for ttmesh and
// provides basic implementations (udp, winpipe, mem) plus a session manager
// that enforces a single canonical session per peer.
//
// Key concepts:
// - Transport: dials/listens for Sessions of a specific Kind (QUIC/TCP/UDP/etc.)
// - Session: a bidirectional connection to a peer; may support multiplexed streams
// - Stream: a Send/Recv channel of protocol.Envelope frames
// - Manager: deduplicates concurrent inbound/outbound links and selects a
//   canonical session per peer based on policy and link quality
package transport

