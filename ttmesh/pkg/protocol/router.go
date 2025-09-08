package protocol

// Simple routing/tunnel hints. These are metadata-only and can
// be extended with a real router later.

type TunnelID uint64

// RouteHint describes desired pathing across the mesh.
// Not serialized in fixed header; embed into payload of MsgRoute/MsgControl as needed.
type RouteHint struct {
    NextHop uint64   // immediate next hop node id
    Tunnel  TunnelID // zero if none
    Hops    []uint64 // optional planned hops
}

