package handshake

import (
    "crypto/ed25519"
    "crypto/rand"
    "errors"
    "fmt"
    "time"

    "ttmesh/pkg/crypto/sign"
    "ttmesh/pkg/transport"
)

// Hello is a minimal signed identity message sent by a client/peer
// on a newly established session. It binds a public key to a logical
// node name and a fresh nonce with a timestamp.
type Hello struct {
    Version   uint32 `json:"ver,omitempty"`
    NodeName  string `json:"node_name,omitempty"`
    Alg       string `json:"alg"`
    PubKey    []byte `json:"pubkey"`
    Nonce     []byte `json:"nonce"`
    Timestamp int64  `json:"ts_unix_ms"`
    Sig       []byte `json:"sig"`
}

// BuildHello constructs a Hello payload and signs it with the provided ed25519 private key.
func BuildHello(nodeName string, priv ed25519.PrivateKey) (Hello, transport.PeerID, error) {
    pub := priv.Public().(ed25519.PublicKey)
    nonce := make([]byte, 16)
    if _, err := rand.Read(nonce); err != nil { return Hello{}, "", err }
    h := Hello{
        Version:   1,
        NodeName:  nodeName,
        Alg:       "ed25519",
        PubKey:    append([]byte(nil), pub...),
        Nonce:     nonce,
        Timestamp: time.Now().UnixMilli(),
    }
    msg := sign.HelloTranscript(h.Alg, h.PubKey, h.Nonce, h.Timestamp, h.NodeName)
    sig, _ := sign.SignEd25519(priv, msg)
    h.Sig = sig
    pid := transport.CanonicalPeerIDFromPubKey("ed25519", pub)
    return h, pid, nil
}

// VerifyHello verifies signature and basic freshness of Hello. Returns canonical PeerID.
func VerifyHello(h Hello, maxSkew time.Duration) (transport.PeerID, error) {
    if h.Alg != "ed25519" { return "", fmt.Errorf("unsupported alg: %s", h.Alg) }
    if len(h.PubKey) != ed25519.PublicKeySize { return "", errors.New("bad pubkey length") }
    if len(h.Sig) != ed25519.SignatureSize { return "", errors.New("bad signature length") }
    if maxSkew <= 0 { maxSkew = 5 * time.Minute }
    now := time.Now().UnixMilli()
    if dt := now - h.Timestamp; dt > int64(maxSkew/time.Millisecond) || dt < -int64(maxSkew/time.Millisecond) {
        return "", errors.New("hello timestamp out of bounds")
    }
    if !sign.VerifyEd25519(ed25519.PublicKey(h.PubKey), sign.HelloTranscript(h.Alg, h.PubKey, h.Nonce, h.Timestamp, h.NodeName), h.Sig) {
        return "", errors.New("hello signature invalid")
    }
    pid := transport.CanonicalPeerIDFromPubKey("ed25519", h.PubKey)
    return pid, nil
}

// transcript moved to pkg/crypto/sign.HelloTranscript
