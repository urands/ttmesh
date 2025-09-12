package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"time"

	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	netstack "ttmesh/pkg/core/netstack"
	"ttmesh/pkg/handshake"
	ttmeshproto "ttmesh/pkg/protocol/proto"
	"ttmesh/pkg/transport"
)

func main() {
	kind := flag.String("kind", "udp", "transport kind: udp|winpipe|mem")
	addr := flag.String("addr", ":7777", "address to connect to")
	name := flag.String("name", "client", "logical node name")
	dest := flag.String("dest", "", "destination node id for test message")
	src := flag.String("src", "client1", "source id for test message header")
	privPath := flag.String("priv", "", "path to base64 ed25519 private key (optional)")
	msg := flag.String("message", "hello ttmesh", "test message to send after handshake")
	timeout := flag.Duration("timeout", 5*time.Second, "dial/handshake timeout")
	flag.Parse()

	logger, _ := zap.NewDevelopment()
	zap.ReplaceGlobals(logger)
	defer logger.Sync()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	tr, err := netstack.NewByKind(*kind)
	if err != nil {
		fatalf("new transport: %v", err)
	}

	priv := loadOrGenKey(*privPath)
	hello, peerID, err := handshake.BuildHello(*name, priv)
	if err != nil {
		fatalf("build hello: %v", err)
	}

	sess, err := tr.Dial(ctx, *addr, transport.PeerInfo{ID: transport.PeerID("temp:client"), Addr: *addr})
	if err != nil {
		fatalf("dial: %v", err)
	}
	defer sess.Close()

	st, err := sess.OpenStream(ctx, transport.StreamControl)
	if err != nil {
		fatalf("open stream: %v", err)
	}

	// Send Hello as protobuf Control.Hello
	he := &ttmeshproto.Envelope{
		Header: &ttmeshproto.Header{Version: 1, Type: ttmeshproto.MessageType_MT_CONTROL, Priority: 0},
		Body: &ttmeshproto.Envelope_Control{Control: &ttmeshproto.Control{Kind: &ttmeshproto.Control_Hello{Hello: &ttmeshproto.PeerHello{
			NodeName: hello.NodeName,
			Alg:      hello.Alg,
			Pubkey:   hello.PubKey,
			Nonce:    hello.Nonce,
			TsUnixMs: hello.Timestamp,
			Sig:      hello.Sig,
		}}}},
	}
	heB, err := proto.Marshal(he)
	if err != nil {
		fatalf("marshal hello env: %v", err)
	}
	if err := st.SendBytes(heB); err != nil {
		fatalf("send hello: %v", err)
	}
	fmt.Println("Hello sent; peerID:", peerID)

	// add awaiting for HelloAck with timeout and status check
	// Await HelloAck with timeout and status check
	ackCh := make(chan *ttmeshproto.PeerHelloAck, 1)
	hdrCh := make(chan *ttmeshproto.Header, 1)
	errCh := make(chan error, 1)
	go func() {
		b, err := st.RecvBytes()
		if err != nil {
			errCh <- err
			return
		}
		var ackEnv ttmeshproto.Envelope
		if err := proto.Unmarshal(b, &ackEnv); err != nil {
			errCh <- err
			return
		}
		if ackEnv.GetHeader() != nil {
			hdrCh <- ackEnv.GetHeader()
		}
		if ctrl := ackEnv.GetControl(); ctrl != nil {
			if ack := ctrl.GetHelloAck(); ack != nil {
				ackCh <- ack
				return
			}
		}
		errCh <- fmt.Errorf("unexpected first response: not a HelloAck")
	}()
	select {
	case ack := <-ackCh:
		fmt.Println("HelloAck:", ack.GetAccepted(), ack.GetPeerId())
		select {
		case h := <-hdrCh:
			if h.DirectStatus != nil {
				fmt.Println("DirectStatus:", h.GetDirectStatus().String())
			}
		default:
		}
		if !ack.GetAccepted() {
			fatalf("hello rejected: %s", ack.GetReason())
		}
	case err := <-errCh:
		fatalf("hello/ack error: %v", err)
	case <-time.After(*timeout):
		fatalf("hello/ack timeout after %s", timeout.String())
	}

	// Send test message
	tm := map[string]any{"kind": "test", "text": *msg, "ts": time.Now().UnixMilli()}
	tb, _ := json.Marshal(tm)
	te := &ttmeshproto.Envelope{Header: &ttmeshproto.Header{Version: 1, Type: ttmeshproto.MessageType_MT_CONTROL, Priority: 1}, Body: &ttmeshproto.Envelope_Raw{Raw: tb}}
	// add header source/dest for router forwarding
	te.Header.Source = &ttmeshproto.NodeRef{Id: *src}
	if *dest != "" {
		te.Header.Dest = &ttmeshproto.NodeRef{Id: *dest}
	}
	teB, err := proto.Marshal(te)
	if err != nil {
		fatalf("marshal test env: %v", err)
	}
	if err := st.SendBytes(teB); err != nil {
		fatalf("send test: %v", err)
	}
	fmt.Println("Test message sent")

    // sleep a bit to allow any async acks/logs to arrive
    time.Sleep(1000 * time.Millisecond)
    
}

func loadOrGenKey(path string) ed25519.PrivateKey {
	if path == "" {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			fatalf("gen key: %v", err)
		}
		fmt.Println("Generated new ed25519 key; pub:")
		fmt.Println(base64.RawURLEncoding.EncodeToString(priv.Public().(ed25519.PublicKey)))
		return priv
	}
	b, err := ioutil.ReadFile(path)
	if err != nil {
		fatalf("read priv: %v", err)
	}
	// allow JSON, or raw base64, or raw bytes
	// try JSON first {"priv":"..."}
	var v struct {
		Priv string `json:"priv"`
	}
	if json.Unmarshal(b, &v) == nil && v.Priv != "" {
		db, err := base64.RawURLEncoding.DecodeString(v.Priv)
		if err != nil {
			fatalf("decode priv b64(json): %v", err)
		}
		return ed25519.PrivateKey(db)
	}
	// try plain base64
	if db, err := base64.RawURLEncoding.DecodeString(string(bytesTrimSpace(b))); err == nil {
		return ed25519.PrivateKey(db)
	}
	// fallback raw bytes
	return ed25519.PrivateKey(bytesTrimSpace(b))
}

func bytesTrimSpace(b []byte) []byte {
	i, j := 0, len(b)
	for i < j && (b[i] == ' ' || b[i] == '\n' || b[i] == '\r' || b[i] == '\t') {
		i++
	}
	for j > i && (b[j-1] == ' ' || b[j-1] == '\n' || b[j-1] == '\r' || b[j-1] == '\t') {
		j--
	}
	return b[i:j]
}

func fatalf(format string, a ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}
