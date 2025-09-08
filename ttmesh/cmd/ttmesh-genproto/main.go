package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"hash/fnv"
	"log"
	"os"
	"path/filepath"
	"strings"

	ttmeshproto "ttmesh/pkg/protocol/proto"

	"google.golang.org/protobuf/proto"
)

func main() {
    outDir := flag.String("out", "testdata/proto", "output directory for binary frames")
    flag.Parse()

    if err := os.MkdirAll(*outDir, 0o755); err != nil {
        log.Fatalf("mkdir %s: %v", *outDir, err)
    }

    // Common fields
    corr := make([]byte, 16)
    if _, err := rand.Read(corr); err != nil { log.Fatal(err) }
    workflowID := "wf_abc123"
    stepID := "analyze"
    shardHash := fnv32(workflowID)

    // Base header and bus header
    baseHeader := &ttmeshproto.Header{
        Version:       1,
        Type:          ttmeshproto.MessageType_MT_TASK,
        Flags:         0,
        Priority:      2,
        CorrelationId: corr,
        Source:        &ttmeshproto.NodeRef{Id: "ingress"},
        Dest:          &ttmeshproto.NodeRef{Id: "workerA"},
        ReplyTo:       &ttmeshproto.NodeRef{Id: "nodeIngress"},
        FragIndex:     0,
        FragTotal:     0,
        MessageId:     randomBytes(16),
        TraceId:       randomBytes(16),
        CreatedUnixMs: 0,
        Tags:          map[string]string{"tenant": "demo"},
        Codec:         ttmeshproto.Codec_CODEC_PROTOBUF,
        Compression:   ttmeshproto.Compression_COMP_NONE,
    }
    bus := &ttmeshproto.BusHeader{
        Version:         1,
        Kind:            ttmeshproto.BusKind_BK_INVOKE,
        Priority:        2,
        Flags:           0,
        ShardHash:       uint32(shardHash),
        WorkflowId:      workflowID,
        StepId:          stepID,
        NodeScope:       "workerA",
        RegionScope:     "eu",
        CapabilityScope: "ai.analyze",
    }

    // 1) Envelope with raw body
    envRaw := &ttmeshproto.Envelope{Header: cloneHeader(baseHeader), Bus: bus}
    envRaw.Header.Type = ttmeshproto.MessageType_MT_RESULT
    envRaw.Body = &ttmeshproto.Envelope_Raw{Raw: []byte("hello-raw")}
    mustDump(*outDir, "envelope_raw.bin", envRaw)

    // 2) Envelope with Invoke (inline args)
    inv := &ttmeshproto.Invoke{
        Params: []*ttmeshproto.ParamDescriptor{
            {Name: "image", MediaType: "image/jpeg", Streamed: false, TotalSize: 1024},
            {Name: "prompt", MediaType: "text/plain", Streamed: false},
        },
        InlineArgs: map[string][]byte{
            "prompt": []byte("find cats"),
        },
    }
    envInvoke := &ttmeshproto.Envelope{Header: cloneHeader(baseHeader), Bus: cloneBus(bus), Body: &ttmeshproto.Envelope_Invoke{Invoke: inv}}
    envInvoke.Header.Type = ttmeshproto.MessageType_MT_TASK
    mustDump(*outDir, "envelope_invoke.bin", envInvoke)

    // 3) Envelope with Result (inline output)
    res := &ttmeshproto.Result{
        Status:   ttmeshproto.Status_ST_OK,
        Outputs:  []*ttmeshproto.Output{{Name: "json", Payload: &ttmeshproto.Output_InlineData{InlineData: []byte("{\"ok\":true}")}}},
        Logs:     []*ttmeshproto.LogLine{{Level: "INFO", Text: "done"}},
        Metrics:  &ttmeshproto.Metrics{CpuMs: 5, WallMs: 7},
        Final:    true,
        Progress: 1.0,
    }
    envResult := &ttmeshproto.Envelope{Header: cloneHeader(baseHeader), Bus: cloneBus(bus), Body: &ttmeshproto.Envelope_Result{Result: res}}
    envResult.Header.Type = ttmeshproto.MessageType_MT_RESULT
    mustDump(*outDir, "envelope_result_inline.bin", envResult)

    // 4) Envelope with Result (blob ref)
    resBlob := proto.Clone(res).(*ttmeshproto.Result)
    resBlob.Outputs = []*ttmeshproto.Output{{
        Name:    "blob",
        Payload: &ttmeshproto.Output_Blob{Blob: &ttmeshproto.BlobRef{Id: "sha256:deadbeef", Size: 4096, MediaType: "application/octet-stream"}},
    }}
    envResultBlob := &ttmeshproto.Envelope{Header: cloneHeader(baseHeader), Bus: cloneBus(bus), Body: &ttmeshproto.Envelope_Result{Result: resBlob}}
    mustDump(*outDir, "envelope_result_blob.bin", envResultBlob)

    // 5) Session init to cache baseline
    dict := []string{"json", "blob"}
    sess := &ttmeshproto.SessionInit{Scid: 1, WorkflowId: workflowID, StepId: stepID, ReplyTo: &ttmeshproto.NodeRef{Id: "nodeIngress"}, OutputNames: dict, DefaultCodec: ttmeshproto.Codec_CODEC_PROTOBUF, DefaultCompression: ttmeshproto.Compression_COMP_NONE}
    envSess := &ttmeshproto.Envelope{Header: cloneHeader(baseHeader), Bus: cloneBus(bus), Body: &ttmeshproto.Envelope_SessionInit{SessionInit: sess}}
    envSess.Header.Type = ttmeshproto.MessageType_MT_CONTROL
    mustDump(*outDir, "envelope_session_init.bin", envSess)

    // 6) Envelope with TinyResult (inline)
    tiny := &ttmeshproto.TinyResult{Scid: 1, Seq: 1, Final: true, Status: ttmeshproto.Status_ST_OK, Payload: &ttmeshproto.TinyResult_InlinePayload{InlinePayload: []byte{0xde, 0xad, 0xbe, 0xef}}}
    envTiny := &ttmeshproto.Envelope{Header: cloneHeader(baseHeader), Bus: cloneBus(bus), Body: &ttmeshproto.Envelope_TinyResult{TinyResult: tiny}}
    envTiny.Header.Type = ttmeshproto.MessageType_MT_RESULT
    mustDump(*outDir, "envelope_tiny_inline.bin", envTiny)

    // 7) Envelope with TinyResult (short blob id + metrics)
    tiny2 := &ttmeshproto.TinyResult{Scid: 1, Seq: 2, Final: true, Status: ttmeshproto.Status_ST_OK, Payload: &ttmeshproto.TinyResult_BlobIdShort{BlobIdShort: []byte{1,2,3,4,5,6,7,8}}}
    tiny2.Metrics = &ttmeshproto.Metrics{CpuMs: 3, WallMs: 5}
    envTiny2 := &ttmeshproto.Envelope{Header: cloneHeader(baseHeader), Bus: cloneBus(bus), Body: &ttmeshproto.Envelope_TinyResult{TinyResult: tiny2}}
    mustDump(*outDir, "envelope_tiny_blobid.bin", envTiny2)

    // 7) Envelope with Control (flow)
    flow := &ttmeshproto.Control{Kind: &ttmeshproto.Control_Flow{Flow: &ttmeshproto.FlowControl{Scope: ttmeshproto.FlowScope_FLOW_STEP, Credit: 32}}}
    envCtrl := &ttmeshproto.Envelope{Header: cloneHeader(baseHeader), Bus: cloneBus(bus), Body: &ttmeshproto.Envelope_Control{Control: flow}}
    envCtrl.Header.Type = ttmeshproto.MessageType_MT_CONTROL
    envCtrl.Header.Priority = 0
    envCtrl.Bus.Priority = 0
    mustDump(*outDir, "envelope_control_flow.bin", envCtrl)

    // 8) Envelope with Ack
    ack := &ttmeshproto.Ack{ MessageId: envInvoke.Header.MessageId,  }
    envAck := &ttmeshproto.Envelope{Body: &ttmeshproto.Envelope_Ack{Ack: ack}}
    // envAck.Header.Type = ttmeshproto.MessageType_MT_CONTROL
    // envAck.Header.Priority = 0
    // envAck.Bus.Priority = 0
    mustDump(*outDir, "envelope_ack_ok.bin", envAck)

    // 8) Envelope with Lease request
    lreq := &ttmeshproto.LeaseRequest{WorkflowId: workflowID, StepId: stepID, TimeoutMs: 5000}
    envLeaseReq := &ttmeshproto.Envelope{Header: cloneHeader(baseHeader), Bus: cloneBus(bus), Body: &ttmeshproto.Envelope_LeaseReq{LeaseReq: lreq}}
    envLeaseReq.Header.Type = ttmeshproto.MessageType_MT_CONTROL
    mustDump(*outDir, "envelope_lease_req.bin", envLeaseReq)

    // 9) Envelope with Lease reply (denied)
    lrep := &ttmeshproto.LeaseReply{Granted: false, LeaseId: "", LeaseDeadlineMs: 0}
    envLeaseRep := &ttmeshproto.Envelope{Header: cloneHeader(baseHeader), Bus: cloneBus(bus), Body: &ttmeshproto.Envelope_LeaseRep{LeaseRep: lrep}}
    envLeaseRep.Header.Type = ttmeshproto.MessageType_MT_CONTROL
    mustDump(*outDir, "envelope_lease_rep.bin", envLeaseRep)

    // 10) Session close
    sclose := &ttmeshproto.SessionClose{Scid: 1}
    envSessClose := &ttmeshproto.Envelope{Header: cloneHeader(baseHeader), Bus: cloneBus(bus), Body: &ttmeshproto.Envelope_SessionClose{SessionClose: sclose}}
    envSessClose.Header.Type = ttmeshproto.MessageType_MT_CONTROL
    mustDump(*outDir, "envelope_session_close.bin", envSessClose)

    fmt.Println("Generated binary proto envelopes in", *outDir)
}

func mustDump(dir, name string, m proto.Message) {
    b, err := proto.Marshal(m)
    if err != nil { log.Fatalf("marshal %s: %v", name, err) }
    p := filepath.Join(dir, name)
    if err := os.WriteFile(p, b, 0o644); err != nil { log.Fatalf("write %s: %v", p, err) }
    fmt.Printf("%-28s %5d bytes  head: %s\n", name, len(b), shortHex(b, 48))
}

func cloneHeader(h *ttmeshproto.Header) *ttmeshproto.Header { return proto.Clone(h).(*ttmeshproto.Header) }
func cloneBus(b *ttmeshproto.BusHeader) *ttmeshproto.BusHeader { return proto.Clone(b).(*ttmeshproto.BusHeader) }

func randomBytes(n int) []byte { b := make([]byte, n); _, _ = rand.Read(b); return b }

func fnv32(s string) uint32 { h := fnv.New32a(); _, _ = h.Write([]byte(s)); return h.Sum32() }

func shortHex(b []byte, n int) string {
    if len(b) == 0 { return "" }
    if n > len(b) { n = len(b) }
    enc := hex.EncodeToString(b[:n])
    if len(b) > n { enc = enc + "…" }
    // add spaces every 2 bytes for readability
    var out []string
    for i := 0; i < len(enc); i += 4 {
        j := i + 4
        if j > len(enc) { j = len(enc) }
        out = append(out, enc[i:j])
    }
    return strings.Join(out, " ")
}
