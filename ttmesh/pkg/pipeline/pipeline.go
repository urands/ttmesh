package pipeline

import (
    "context"
    "time"

    "go.uber.org/zap"
    "google.golang.org/protobuf/proto"

    "ttmesh/pkg/core/priocq"
    "ttmesh/pkg/peers"
    ttmeshproto "ttmesh/pkg/protocol/proto"
    "ttmesh/pkg/transport"
)

// Pipeline wires ingress classification → multi-level queues → egress workers.
type Pipeline struct {
    q      *priocq.MultiLevelQueue
    shaper map[string]*priocq.TokenBucket // per-dest shapers
    rtr    interface{ SendBytesToPeer(context.Context, transport.PeerID, []byte) error }
    ps     *peers.Store
    stop   chan struct{}
}

func New(rtr interface{ SendBytesToPeer(context.Context, transport.PeerID, []byte) error }, ps *peers.Store) *Pipeline {
    p := &Pipeline{
        q:      priocq.New(),
        shaper: make(map[string]*priocq.TokenBucket),
        rtr:    rtr,
        ps:     ps,
        stop:   make(chan struct{}),
    }
    // start a couple of workers
    go p.worker()
    go p.worker()
    return p
}

func (p *Pipeline) Close() { close(p.stop) }

// EnqueueProto classifies and enqueues a protobuf Envelope for forwarding.
func (p *Pipeline) EnqueueProto(src transport.PeerID, env *ttmeshproto.Envelope, raw []byte) {
    if env == nil || env.Header == nil { return }
    cls := classify(env)
    dst := env.GetHeader().GetDest().GetId()
    if dst == "" { return }
    it := priocq.Item{Bytes: raw, Dest: dst, Size: len(raw), Class: cls, Src: string(src), Arrived: time.Now()}
    if dl := env.GetHeader().GetCreatedUnixMs(); dl != 0 { it.Deadline = dl }
    p.q.Enqueue(it)
}

func classify(e *ttmeshproto.Envelope) priocq.Class {
    switch e.GetHeader().GetType() {
    case ttmeshproto.MessageType_MT_CONTROL:
        return priocq.L0Control
    case ttmeshproto.MessageType_MT_TASK, ttmeshproto.MessageType_MT_RESULT, ttmeshproto.MessageType_MT_DAG_FRAGMENT:
        // Realtime vs bulk can be refined by size/flags; use payload length heuristic
        if pl := payloadLen(e); pl > 64*1024 { return priocq.L2Bulk }
        return priocq.L1Realtime
    default:
        return priocq.L1Realtime
    }
}

func payloadLen(e *ttmeshproto.Envelope) int {
    switch b := e.GetBody().(type) {
    case *ttmeshproto.Envelope_Raw:
        return len(b.Raw)
    case *ttmeshproto.Envelope_Invoke:
        // rough estimate
        return len(b.Invoke.GetInlineArgs()) * 64
    case *ttmeshproto.Envelope_Chunk:
        return len(b.Chunk.GetData())
    case *ttmeshproto.Envelope_Result:
        n := 0
        for _, o := range b.Result.GetOutputs() {
            if x := o.GetInlineData(); x != nil { n += len(x) }
        }
        return n
    case *ttmeshproto.Envelope_TinyResult:
        if x := b.TinyResult.GetInlinePayload(); x != nil { return len(x) }
        return 16
    default:
        return 0
    }
}

func (p *Pipeline) worker() {
    for {
        it, ok := p.q.Dequeue(p.stop)
        if !ok { return }
        // shaper per-dest
        tb := p.shaper[it.Dest]
        if tb == nil { tb = priocq.NewTokenBucket(1_000_000, 2_000_000); p.shaper[it.Dest] = tb } // 1MB/s default
        if ok, wait := tb.Allow(int64(it.Size)); !ok {
            // sleep a bit then re-enqueue to avoid head blocking
            time.Sleep(wait)
        }
        if err := p.rtr.SendBytesToPeer(context.Background(), transport.PeerID(it.Dest), it.Bytes); err != nil {
            zap.L().Warn("pipeline send failed", zap.String("dest", it.Dest), zap.Error(err))
            // simple retry: re-enqueue with same class
            time.Sleep(10 * time.Millisecond)
            p.q.Enqueue(it)
            continue
        }
        if p.ps != nil { p.ps.RecordExchange(transport.PeerID(it.Dest), 0, uint64(it.Size), 0, 1) }
    }
}

// Helper to ensure the raw bytes are protobuf-encoded; if only envelope is provided.
func MarshalEnvelope(e *ttmeshproto.Envelope) []byte {
    b, _ := proto.Marshal(e)
    return b
}

