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
    "strings"
    "time"

    "go.uber.org/zap"
    "google.golang.org/protobuf/proto"

    netstack "ttmesh/pkg/core/netstack"
    "ttmesh/pkg/handshake"
    ttmeshproto "ttmesh/pkg/protocol/proto"
    "ttmesh/pkg/transport"
)

func main() {
    kind := flag.String("kind", "udp", "transport kind: udp|quic|tcp|mem")
    addr := flag.String("addr", ":7777", "address to connect to")
    name := flag.String("name", "worker", "logical worker name")
    privPath := flag.String("priv", "", "path to base64 ed25519 private key (optional)")
    cases := flag.String("cases", "basic,stream,auth", "which sample tasks to register (comma-separated)")
    timeout := flag.Duration("timeout", 5*time.Second, "dial/handshake timeout")
    flag.Parse()

    logger, _ := zap.NewDevelopment()
    zap.ReplaceGlobals(logger)
    defer logger.Sync()

    ctx, cancel := context.WithTimeout(context.Background(), *timeout)
    defer cancel()

    tr, err := netstack.NewByKind(*kind)
    if err != nil { fatalf("new transport: %v", err) }

    priv := loadOrGenKey(*privPath)
    hello, peerID, err := handshake.BuildHello(*name, priv)
    if err != nil { fatalf("build hello: %v", err) }

    // connect and open control stream
    sess, err := tr.Dial(ctx, *addr, transport.PeerInfo{ID: transport.PeerID("temp:worker"), Addr: *addr})
    if err != nil { fatalf("dial: %v", err) }
    defer sess.Close()
    st, err := sess.OpenStream(ctx, transport.StreamControl)
    if err != nil { fatalf("open stream: %v", err) }

    // send Hello
    he := &ttmeshproto.Envelope{Header: &ttmeshproto.Header{Version: 1, Type: ttmeshproto.MessageType_MT_CONTROL, Priority: 0}, Body: &ttmeshproto.Envelope_Control{Control: &ttmeshproto.Control{Kind: &ttmeshproto.Control_Hello{Hello: &ttmeshproto.PeerHello{
        NodeName: hello.NodeName, Alg: hello.Alg, Pubkey: hello.PubKey, Nonce: hello.Nonce, TsUnixMs: hello.Timestamp, Sig: hello.Sig,
    }}}}}
    heB, _ := proto.Marshal(he)
    if err := st.SendBytes(heB); err != nil { fatalf("send hello: %v", err) }
    fmt.Println("Hello sent; worker peerID:", peerID)

    // wait for HelloAck (best effort)
    _ = awaitHelloAck(st, *timeout)

    // build TaskRegister
    reg := &ttmeshproto.TaskRegister{
        WorkerId: string(peerID),
        Self:     &ttmeshproto.NodeRef{Id: string(peerID)},
        Endpoints: []*ttmeshproto.NetworkEndpoint{
            {Scheme: "grpc+tcp", Address: "127.0.0.1:50051"},
        },
        Features: &ttmeshproto.FeatureFlags{SupportsStreamInput: true, SupportsStreamOutput: true, SupportsCompression: true},
        Labels:   map[string]string{"region": "local", "tier": "dev"},
    }
    enabled := map[string]bool{}
    for _, c := range strings.Split(*cases, ",") { enabled[strings.TrimSpace(strings.ToLower(c))] = true }

    if enabled["basic"] {
        reg.Tasks = append(reg.Tasks, &ttmeshproto.TaskDescriptor{
            Name:            "ai.analyze",
            Version:         "1.0.0",
            StreamInput:     false,
            StreamOutput:    true,
            PriorityDefault: 100,
            Contract: &ttmeshproto.IOContract{
                Inputs: []*ttmeshproto.ParamDescriptor{
                    {Name: "image", MediaType: "image/jpeg"},
                    {Name: "prompt", MediaType: "text/plain"},
                },
                Outputs: []*ttmeshproto.OutputDescriptor{
                    {Name: "json", MediaType: "application/json", SchemaRef: "https://schemas.example.com/vision/detection.v1.json#/definitions/DetectionList"},
                },
            },
            Meta: map[string]string{"lang": "go"},
        })
    }
    if enabled["stream"] {
        reg.Tasks = append(reg.Tasks, &ttmeshproto.TaskDescriptor{
            Name:            "storage.write",
            Version:         "2024.09",
            StreamInput:     true,
            StreamOutput:    false,
            PriorityDefault: 120,
            Contract: &ttmeshproto.IOContract{
                Inputs: []*ttmeshproto.ParamDescriptor{
                    {Name: "blob", MediaType: "application/octet-stream", Streamed: true},
                },
                Outputs: []*ttmeshproto.OutputDescriptor{
                    {Name: "ok", MediaType: "application/json"},
                },
            },
            Meta: map[string]string{"role": "ingest"},
        })
    }
    if enabled["auth"] {
        reg.Tasks = append(reg.Tasks, &ttmeshproto.TaskDescriptor{
            Name:         "private.compute",
            Version:      "1.0",
            StreamInput:  false,
            StreamOutput: false,
            Auth: &ttmeshproto.AuthPolicy{RequireSignature: true, AllowedPeerIds: []string{"pk:ed25519:example"}},
        })
    }

    env := &ttmeshproto.Envelope{Header: &ttmeshproto.Header{Version: 1, Type: ttmeshproto.MessageType_MT_CONTROL}, Body: &ttmeshproto.Envelope_Control{Control: &ttmeshproto.Control{Kind: &ttmeshproto.Control_TaskRegister{TaskRegister: reg}}}}
    b, err := proto.Marshal(env)
    if err != nil { fatalf("marshal register: %v", err) }
    if err := st.SendBytes(b); err != nil { fatalf("send register: %v", err) }
    fmt.Println("TaskRegister sent with", len(reg.Tasks), "tasks")

    // Optionally read ack
    _ = awaitRegisterAck(st, *timeout)
}

func awaitHelloAck(st transport.Stream, to time.Duration) error {
    type res struct{ ack *ttmeshproto.PeerHelloAck; err error }
    ch := make(chan res, 1)
    go func() {
        b, err := st.RecvBytes()
        if err != nil { ch <- res{nil, err}; return }
        var env ttmeshproto.Envelope
        if err := proto.Unmarshal(b, &env); err != nil { ch <- res{nil, err}; return }
        if ctrl := env.GetControl(); ctrl != nil { ch <- res{ctrl.GetHelloAck(), nil}; return }
        ch <- res{nil, fmt.Errorf("not control")}
    }()
    select {
    case r := <-ch:
        if r.err != nil { return r.err }
        if r.ack != nil && r.ack.GetAccepted() { fmt.Println("HelloAck accepted") }
        return nil
    case <-time.After(to):
        return fmt.Errorf("hello-ack timeout")
    }
}

func awaitRegisterAck(st transport.Stream, to time.Duration) error {
    type res struct{ ack *ttmeshproto.TaskRegisterAck; err error }
    ch := make(chan res, 1)
    go func() {
        b, err := st.RecvBytes()
        if err != nil { ch <- res{nil, err}; return }
        var env ttmeshproto.Envelope
        if err := proto.Unmarshal(b, &env); err != nil { ch <- res{nil, err}; return }
        if ctrl := env.GetControl(); ctrl != nil { ch <- res{ctrl.GetTaskRegisterAck(), nil}; return }
        ch <- res{nil, fmt.Errorf("not control")}
    }()
    select {
    case r := <-ch:
        if r.err != nil { return r.err }
        if r.ack != nil { fmt.Println("RegisterAck:", r.ack.GetAccepted(), r.ack.GetReason()) }
        return nil
    case <-time.After(to):
        return fmt.Errorf("register-ack timeout")
    }
}

func loadOrGenKey(path string) ed25519.PrivateKey {
    if path == "" {
        _, priv, err := ed25519.GenerateKey(rand.Reader)
        if err != nil { fatalf("gen key: %v", err) }
        fmt.Println("Generated new ed25519 key; pub:")
        fmt.Println(base64.RawURLEncoding.EncodeToString(priv.Public().(ed25519.PublicKey)))
        return priv
    }
    b, err := ioutil.ReadFile(path)
    if err != nil { fatalf("read priv: %v", err) }
    var v struct{ Priv string `json:"priv"` }
    if json.Unmarshal(b, &v) == nil && v.Priv != "" {
        db, err := base64.RawURLEncoding.DecodeString(v.Priv)
        if err != nil { fatalf("decode priv b64(json): %v", err) }
        return ed25519.PrivateKey(db)
    }
    if db, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(string(b))); err == nil {
        return ed25519.PrivateKey(db)
    }
    return ed25519.PrivateKey(strings.TrimSpace(string(b)))
}

func fatalf(format string, a ...any) {
    _, _ = fmt.Fprintf(os.Stderr, format+"\n", a...)
    os.Exit(1)
}

