package identity

import (
    "crypto/ed25519"
    "crypto/rand"
    "encoding/base64"
    "os"
    "strings"

    "go.uber.org/zap"

    "ttmesh/pkg/config"
    "ttmesh/pkg/transport"
)

// LoadOrGenEd25519 loads an ed25519 private key from config or generates a new one.
// Returns the private key and the canonical peer id (pk:ed25519:<b64(pub)>).
func LoadOrGenEd25519(c config.IdentityConfig) (ed25519.PrivateKey, transport.PeerID, error) {
    var pk ed25519.PrivateKey
    // From base64
    if s := strings.TrimSpace(c.PrivateKey); s != "" {
        if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
            pk = ed25519.PrivateKey(b)
        } else {
            zap.L().Warn("failed to decode identity.private_key", zap.Error(err))
        }
    }
    // From file
    if pk == nil && strings.TrimSpace(c.PrivateKeyFile) != "" {
        if b, err := os.ReadFile(c.PrivateKeyFile); err == nil {
            txt := strings.TrimSpace(string(b))
            if db, err := base64.RawURLEncoding.DecodeString(txt); err == nil {
                pk = ed25519.PrivateKey(db)
            } else {
                // assume raw bytes
                pk = ed25519.PrivateKey(b)
            }
        } else {
            zap.L().Warn("failed to read identity.private_key_file", zap.Error(err))
        }
    }
    // Generate
    if pk == nil {
        _, gen, err := ed25519.GenerateKey(rand.Reader)
        if err != nil { return nil, "", err }
        pk = gen
        zap.L().Info("generated new ed25519 identity (persist to config.identity.private_key)",
            zap.String("pub_b64", base64.RawURLEncoding.EncodeToString(gen.Public().(ed25519.PublicKey))))
    }
    pub := pk.Public().(ed25519.PublicKey)
    pid := transport.CanonicalPeerIDFromPubKey("ed25519", pub)
    return pk, pid, nil
}

