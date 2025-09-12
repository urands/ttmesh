package main

import (
    "context"
    "crypto/ed25519"
    "crypto/rand"
    "encoding/json"
    "encoding/base64"
    "flag"
    "fmt"
    "time"

    "google.golang.org/protobuf/proto"

    "ttmesh/pkg/handshake"
    ttmeshproto "ttmesh/pkg/protocol/proto"
    "ttmesh/pkg/transport"
    netstack "ttmesh/pkg/core/netstack"
)

func main() {
    kind := flag.String("kind", "udp", "transport kind: udp|winpipe|mem")
    addr := flag.String("addr", ":7777", "node address to connect to")
    name := flag.String("name", "ttmesh-ctl", "logical node name for hello")
    timeout := flag.Duration("timeout", 5*time.Second, "dial/handshake timeout")
    listWorkers := flag.Bool("list-workers", false, "list registered workers")
    flag.Parse()

    ctx, cancel := context.WithTimeout(context.Background(), *timeout)
    defer cancel()

    tr, err := netstack.NewByKind(*kind)
    if err != nil { fatalf("new transport: %v", err) }

    // Ephemeral identity for ctl
    _, priv, err := ed25519.GenerateKey(rand.Reader)
    if err != nil { fatalf("gen key: %v", err) }
    hello, _, err := handshake.BuildHello(*name, priv)
    if err != nil { fatalf("build hello: %v", err) }

    sess, err := tr.Dial(ctx, *addr, transport.PeerInfo{ID: transport.PeerID("temp:ctl"), Addr: *addr})
    if err != nil { fatalf("dial: %v", err) }
    defer sess.Close()
    st, err := sess.OpenStream(ctx, transport.StreamControl)
    if err != nil { fatalf("open stream: %v", err) }

    // Send Hello (Control.Hello)
    he := &ttmeshproto.Envelope{Header: &ttmeshproto.Header{Version: 1, Type: ttmeshproto.MessageType_MT_CONTROL}, Body: &ttmeshproto.Envelope_Control{Control: &ttmeshproto.Control{Kind: &ttmeshproto.Control_Hello{Hello: &ttmeshproto.PeerHello{
        NodeName: hello.NodeName, Alg: hello.Alg, Pubkey: hello.PubKey, Nonce: hello.Nonce, TsUnixMs: hello.Timestamp, Sig: hello.Sig,
    }}}}}
    heB, _ := proto.Marshal(he)
    if err := st.SendBytes(heB); err != nil { fatalf("send hello: %v", err) }

    // Read optional HelloAck quickly (non-fatal)
    _ = tryReadAck(st)

    // Query identity first
    sendCtl(st, &ttmeshproto.Control{Kind: &ttmeshproto.Control_GetIdentity{GetIdentity: &ttmeshproto.GetIdentity{}}})
    id := waitIdentity(st, 3*time.Second)
    if id != nil {
        fmt.Printf("Connected Node: id=%s name=%s pubkey=%s\n\n", id.GetId(), id.GetNodeName(), base64.RawURLEncoding.EncodeToString(id.GetPubkey()))
    }

    if *listWorkers {
        sendCtl(st, &ttmeshproto.Control{Kind: &ttmeshproto.Control_TaskListWorkers{TaskListWorkers: &ttmeshproto.TaskListWorkers{IncludeTasks: true}}})
        lrep := waitListWorkers(st, 5*time.Second)
        if lrep == nil { fatalf("timeout waiting for list workers reply") }
        printWorkers(lrep)
        return
    }

    // Send GetRoutes and print
    sendCtl(st, &ttmeshproto.Control{Kind: &ttmeshproto.Control_GetRoutes{GetRoutes: &ttmeshproto.GetRoutes{}}})
    rep := waitRoutes(st, 5*time.Second)
    if rep == nil { fatalf("timeout waiting for routes reply") }
    printRoutes(rep)
}

func tryReadAck(st transport.Stream) error { return nil }

func sendCtl(st transport.Stream, c *ttmeshproto.Control) {
    env := &ttmeshproto.Envelope{Header: &ttmeshproto.Header{Version: 1, Type: ttmeshproto.MessageType_MT_CONTROL}, Body: &ttmeshproto.Envelope_Control{Control: c}}
    b, _ := proto.Marshal(env)
    _ = st.SendBytes(b)
}

func waitIdentity(st transport.Stream, d time.Duration) *ttmeshproto.IdentityReply {
    deadline := time.Now().Add(d)
    for i := 0; i < 5; i++ {
        if time.Now().After(deadline) { return nil }
        b, err := st.RecvBytes(); if err != nil { return nil }
        var env ttmeshproto.Envelope
        if proto.Unmarshal(b, &env) != nil { continue }
        c := env.GetControl(); if c == nil { continue }
        if rep := c.GetIdentityReply(); rep != nil { return rep }
        // absorb HelloAck if present, ignore others
    }
    return nil
}

func waitRoutes(st transport.Stream, d time.Duration) *ttmeshproto.RoutesReply {
    deadline := time.Now().Add(d)
    for {
        if time.Now().After(deadline) { return nil }
        b, err := st.RecvBytes(); if err != nil { return nil }
        var env ttmeshproto.Envelope
        if proto.Unmarshal(b, &env) != nil { continue }
        c := env.GetControl(); if c == nil { continue }
        if rep := c.GetRoutesReply(); rep != nil { return rep }
    }
}

func waitListWorkers(st transport.Stream, d time.Duration) *ttmeshproto.TaskListWorkersReply {
    deadline := time.Now().Add(d)
    for {
        if time.Now().After(deadline) { return nil }
        b, err := st.RecvBytes(); if err != nil { return nil }
        var env ttmeshproto.Envelope
        if proto.Unmarshal(b, &env) != nil { continue }
        c := env.GetControl(); if c == nil { continue }
        if rep := c.GetTaskListWorkersReply(); rep != nil { return rep }
    }
}

func printWorkers(r *ttmeshproto.TaskListWorkersReply) {
    fmt.Println("Workers:")
    for _, w := range r.GetWorkers() {
        ep := ""; if len(w.GetEndpoints())>0 { ep = w.GetEndpoints()[0].GetAddress() }
        fmt.Printf("- %s labels=%v endpoint=%s tasks=%d\n", w.GetWorkerId(), w.GetLabels(), ep, len(w.GetTasks()))
        for _, t := range w.GetTasks() {
            fmt.Printf("    * %s@%s stream[in:%v out:%v]\n", t.GetName(), t.GetVersion(), t.GetStreamInput(), t.GetStreamOutput())
        }
    }
    if n := r.GetNextPageToken(); n != "" { fmt.Println("NextPageToken:", n) }
}

func printRoutes(r *ttmeshproto.RoutesReply) {
    fmt.Println("Peers:")
    for _, p := range r.GetPeers() {
        fmt.Printf("- %s name=%s reachable=%v addrs=%v\n", p.GetId(), p.GetNodeName(), p.GetReachable(), p.GetAddresses())
    }
    fmt.Println("Adjacency:")
    for _, e := range r.GetAdjacency() {
        fmt.Printf("- %s -> %s\n", e.GetFrom(), e.GetTo())
    }
    fmt.Println("Route Targets:")
    for _, t := range r.GetTargets() { fmt.Println("-", t) }
    fmt.Println("Candidates:")
    for _, c := range r.GetCandidates() {
        jb, _ := json.Marshal(c)
        fmt.Println("-", string(jb))
    }
}

func fatalf(format string, a ...any) {
    fmt.Printf(format+"\n", a...)
}
