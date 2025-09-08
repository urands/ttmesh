package netstack

import (
    "context"
    "crypto/ed25519"
    "crypto/rand"
    "encoding/base64"
    "fmt"

    "google.golang.org/protobuf/proto"

    ttmeshproto "ttmesh/pkg/protocol/proto"
    "ttmesh/pkg/transport"
)

// sendHello builds and sends Control.Hello over a fresh stream.
func sendHello(ctx context.Context, s transport.Session, priv ed25519.PrivateKey, nodeName string) error {
    if priv == nil { return nil }
    st, err := s.OpenStream(ctx, transport.StreamControl)
    if err != nil { return err }
    pub := priv.Public().(ed25519.PublicKey)
    ts := timeNow().UnixMilli()
    nonce := make([]byte, 16)
    if _, err := rand.Read(nonce); err != nil { return err }
    // transcript (same as in handshake package) — keep minimal duplicate here to avoid deps
    b64 := base64.RawURLEncoding
    transcript := []byte("ttmesh:hello|v=1|alg=ed25519|ts=" + fmt.Sprint(ts) + "|pub=" + b64.EncodeToString(pub) + "|nonce=" + b64.EncodeToString(nonce) + "|name=" + nodeName)
    sig := ed25519.Sign(priv, transcript)
    he := &ttmeshproto.Envelope{Header: &ttmeshproto.Header{Version: 1, Type: ttmeshproto.MessageType_MT_CONTROL, Priority: 0}, Body: &ttmeshproto.Envelope_Control{Control: &ttmeshproto.Control{Kind: &ttmeshproto.Control_Hello{Hello: &ttmeshproto.PeerHello{
        NodeName: nodeName, Alg: "ed25519", Pubkey: pub, Nonce: nonce, TsUnixMs: ts, Sig: sig,
    }}}}}
    b, err := proto.Marshal(he)
    if err != nil { return err }
    return st.SendBytes(b)
}

