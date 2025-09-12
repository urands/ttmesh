package peering

import (
    "context"
    "time"

    "go.uber.org/zap"

    "ttmesh/pkg/core/priocq"
    "ttmesh/pkg/pipeline"
    "ttmesh/pkg/peers"
    ttmeshproto "ttmesh/pkg/protocol/proto"
    "ttmesh/pkg/transport"
)

// protoForwarder provides prioritized forwarding of serialized proto envelopes.
type protoForwarder interface {
    EnqueueProto(transport.PeerID, *ttmeshproto.Envelope, []byte)
    Close()
}

// newForwarder returns a forwarder backed by the global pipeline when present,
// otherwise a lightweight local prioritized queue.
func newForwarder(_ context.Context, r routerLike, ps *peers.Store, pl interface{}) protoForwarder {
    if p, ok := pl.(*pipeline.Pipeline); ok && p != nil {
        return &pipeFwd{p: p}
    }
    lf := &localFwd{
        q:    priocq.New(),
        r:    r,
        ps:   ps,
        stop: make(chan struct{}),
    }
    go lf.worker()
    return lf
}

type pipeFwd struct{ p *pipeline.Pipeline }

func (f *pipeFwd) EnqueueProto(src transport.PeerID, env *ttmeshproto.Envelope, raw []byte) {
    f.p.EnqueueProto(src, env, raw)
}
func (f *pipeFwd) Close() {}

type localFwd struct {
    q    *priocq.MultiLevelQueue
    r    routerLike
    ps   *peers.Store
    stop chan struct{}
}

func (f *localFwd) Close() { close(f.stop) }

func (f *localFwd) EnqueueProto(src transport.PeerID, env *ttmeshproto.Envelope, raw []byte) {
    if env == nil || env.Header == nil { return }
    dst := env.GetHeader().GetDest().GetId()
    if dst == "" { return }
    cls := classifyLocal(env)
    it := priocq.Item{Bytes: raw, Dest: dst, Size: len(raw), Class: cls, Src: string(src), Arrived: time.Now()}
    if dl := env.GetHeader().GetCreatedUnixMs(); dl != 0 { it.Deadline = dl }
    f.q.Enqueue(it)
}

func (f *localFwd) worker() {
    for {
        it, ok := f.q.Dequeue(f.stop)
        if !ok { return }
        if err := f.r.SendBytesToPeer(context.Background(), transport.PeerID(it.Dest), it.Bytes); err != nil {
            zap.L().Warn("local fwd send failed", zap.String("dest", it.Dest), zap.Error(err))
            time.Sleep(10 * time.Millisecond)
            f.q.Enqueue(it)
            continue
        }
        if f.ps != nil { f.ps.RecordExchange(transport.PeerID(it.Dest), 0, uint64(it.Size), 0, 1) }
    }
}

// classifyLocal prioritizes control > realtime > bulk, with raw as bulk.
func classifyLocal(e *ttmeshproto.Envelope) priocq.Class {
    if e.GetControl() != nil { return priocq.L0Control }
    if e.GetChunk() != nil { return priocq.L2Bulk }
    if e.GetResult() != nil { return priocq.L1Realtime }
    if e.GetTinyResult() != nil { return priocq.L1Realtime }
    if e.GetRaw() != nil { return priocq.L2Bulk }
    return priocq.L1Realtime
}

