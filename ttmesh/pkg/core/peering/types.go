package peering

import (
    "context"
    ttmeshproto "ttmesh/pkg/protocol/proto"
    "ttmesh/pkg/transport"
)

// routerLike abstracts the router used to forward bytes to a specific peer.
type routerLike interface {
    SendBytesToPeer(context.Context, transport.PeerID, []byte) error
}

// enqueuer abstracts an async pipeline that can enqueue a serialized proto.
type enqueuer interface {
    EnqueueProto(transport.PeerID, *ttmeshproto.Envelope, []byte)
}

