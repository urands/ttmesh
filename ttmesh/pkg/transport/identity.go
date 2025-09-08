package transport

import (
    "encoding/base64"
    "fmt"
    "net"
    "strings"
)

// MutablePeer is an optional interface that Sessions can implement to allow
// updating the peer identity after handshake/authentication.
type MutablePeer interface {
    SetPeer(PeerInfo)
}

// TempPeerID builds a temporary peer id from transport kind and remote address.
// It is suitable to use before an authenticated handshake completes.
func TempPeerID(kind Kind, addr net.Addr) PeerID {
    if addr == nil { return PeerID(fmt.Sprintf("temp:%s:unknown", kind)) }
    return PeerID(fmt.Sprintf("temp:%s:%s", kind, addr.String()))
}

// CanonicalPeerIDFromPubKey constructs a canonical peer id from public key bytes.
// The format is: pk:<alg>:<base64url-nopad(pubkey)>
// Example: pk:ed25519:AbCd...
func CanonicalPeerIDFromPubKey(alg string, pub []byte) PeerID {
    alg = strings.ToLower(strings.TrimSpace(alg))
    enc := base64.RawURLEncoding.EncodeToString(pub)
    return PeerID("pk:" + alg + ":" + enc)
}

