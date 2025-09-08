package main

import (
    "encoding/hex"
    "flag"
    "fmt"
    "log"
    "os"
    "path/filepath"
    "strings"

    "ttmesh/pkg/protocol"
    "ttmesh/pkg/protocol/codec"
)

func main() {
    outDir := flag.String("out", "testdata/frame", "output directory for binary frames")
    flag.Parse()
    if err := os.MkdirAll(*outDir, 0o755); err != nil { log.Fatal(err) }

    // Build a base header
    corr, _ := protocol.NewCorrelation()
    h := protocol.Header{Version: 1, Type: protocol.MsgResult, Priority: 2}
    h.Correlation = corr
    h.Source = 1001
    h.Dest = 2002
    h.DagID = 0xdeadbeef
    h.DagStep = 3

    reg := codec.NewRegistry()
    reg.Register(codec.JSON())

    // 1) Simple JSON payload frame
    payload, err := protocol.EncodeBody(reg, protocol.FormatJSON, map[string]any{"ok": true, "n": 42})
    if err != nil { log.Fatal(err) }
    env := protocol.Envelope{Header: h, Payload: payload}
    frame := mustFrame(&env)
    writeOut(*outDir, "frame_json.bin", frame)

    // 2) Fragmented frames (chunk = 8 bytes)
    env2 := env
    env2.Payload = make([]byte, 40)
    for i := range env2.Payload { env2.Payload[i] = byte(i) }
    frags, err := env2.Fragments(8)
    if err != nil { log.Fatal(err) }
    for i := range frags {
        b := mustFrame(&frags[i])
        writeOut(*outDir, fmt.Sprintf("frame_frag_%02d.bin", i), b)
    }

    // 3) Empty payload control frame
    h3 := h; h3.Type = protocol.MsgControl
    env3 := protocol.Envelope{Header: h3}
    writeOut(*outDir, "frame_control_empty.bin", mustFrame(&env3))

    fmt.Println("Generated internal frames in", *outDir)
}

func mustFrame(e *protocol.Envelope) []byte {
    b, err := e.EncodeFrame()
    if err != nil { log.Fatal(err) }
    return b
}

func writeOut(dir, name string, b []byte) {
    p := filepath.Join(dir, name)
    if err := os.WriteFile(p, b, 0o644); err != nil { log.Fatal(err) }
    fmt.Printf("%-24s %5d bytes  head: %s\n", name, len(b), shortHex(b, 64))
}

func shortHex(b []byte, n int) string {
    if len(b) == 0 { return "" }
    if n > len(b) { n = len(b) }
    enc := hex.EncodeToString(b[:n])
    if len(b) > n { enc += "…" }
    var out []string
    for i := 0; i < len(enc); i += 4 {
        j := i + 4
        if j > len(enc) { j = len(enc) }
        out = append(out, enc[i:j])
    }
    return strings.Join(out, " ")
}
