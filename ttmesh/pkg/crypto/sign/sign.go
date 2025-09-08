package sign

import (
    "crypto/ed25519"
)

// SignEd25519 signs data using ed25519.
func SignEd25519(priv ed25519.PrivateKey, data []byte) ([]byte, error) {
    sig := ed25519.Sign(priv, data)
    return sig, nil
}

// VerifyEd25519 verifies ed25519 signature.
func VerifyEd25519(pub ed25519.PublicKey, data, sig []byte) bool {
    return ed25519.Verify(pub, data, sig)
}

