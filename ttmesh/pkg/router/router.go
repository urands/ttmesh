package router

import (
    "container/heap"
    "context"
    "sync"
    "time"

    "go.uber.org/zap"
    "ttmesh/pkg/peers"
    "ttmesh/pkg/transport"
)

// Router selects best paths using a combination of path-vector candidates and
// on-demand graph search across known direct adjacencies.
type Router struct {
    ps      *peers.Store
    mgr     *transport.Manager
    localID transport.PeerID
    mu      sync.RWMutex
}

func New(ps *peers.Store, mgr *transport.Manager, local transport.PeerID) *Router {
    return &Router{ps: ps, mgr: mgr, localID: local}
}

// NextHopForPeer returns the immediate neighbor to forward towards target, preferring
// best candidate route; falls back to Dijkstra over current adjacency.
func (r *Router) NextHopForPeer(target transport.PeerID) (transport.PeerID, []transport.PeerID, bool) {
    // 1) Consult learned candidates
    if cand, ok := r.ps.BestRouteCandidate("peer:" + string(target)); ok {
        nh := transport.PeerID(cand.NextHop)
        if r.hasDirectAdjacency(nh) {
            zap.L().Debug("route via candidate", zap.String("target", string(target)), zap.String("next_hop", string(nh)), zap.Strings("path", cand.Path))
            return nh, toPeerIDs(cand.Path), true
        }
    }
    // 2) Fallback to Dijkstra on current adjacency
    path := r.findPathDijkstra(r.localID, target)
    if len(path) >= 2 {
        zap.L().Debug("route via dijkstra", zap.String("target", string(target)), zap.Any("path", path))
        return path[1], path, true
    }
    return "", nil, false
}

func (r *Router) hasDirectAdjacency(nh transport.PeerID) bool {
    pm, ok := r.ps.Get(r.localID)
    if !ok { return false }
    for _, id := range pm.ConnectedDirectIDs { if id == string(nh) { return true } }
    return false
}

// SendBytesToPeer picks a route and sends bytes to the first hop using the
// default control stream.
func (r *Router) SendBytesToPeer(ctx context.Context, target transport.PeerID, b []byte) error {
    nh, _, ok := r.NextHopForPeer(target)
    if !ok { return ErrNoRoute }
    sess := r.mgr.GetSession(nh)
    if sess == nil { return ErrNoRoute }
    st, err := sess.OpenStream(ctx, transport.StreamControl)
    if err != nil { return err }
    zap.L().Debug("send via", zap.String("target", string(target)), zap.String("next_hop", string(nh)), zap.Int("bytes", len(b)))
    return st.SendBytes(b)
}

var ErrNoRoute = &noRouteErr{}
type noRouteErr struct{}
func (e *noRouteErr) Error() string { return "no route" }

// ---- Graph + Dijkstra ----

type graph struct {
    adj map[string]map[string]float64 // from -> (to -> weight)
}

func buildGraph(ps *peers.Store, mgr *transport.Manager, local transport.PeerID) graph {
    g := graph{adj: make(map[string]map[string]float64)}
    ids := ps.ListPeerIDs()
    for _, id := range ids {
        pm, ok := ps.Get(id)
        if !ok { continue }
        from := string(id)
        for _, to := range pm.ConnectedDirectIDs {
            if g.adj[from] == nil { g.adj[from] = make(map[string]float64) }
            w := edgeWeight(ps, mgr, local, transport.PeerID(from), transport.PeerID(to))
            g.adj[from][to] = w
        }
    }
    zap.L().Debug("graph built", zap.Int("nodes", len(g.adj)))
    return g
}

func edgeWeight(ps *peers.Store, mgr *transport.Manager, local, from, to transport.PeerID) float64 {
    // If edge originates at local, use live session metrics; else use heuristic
    if from == local {
        if s := mgr.GetSession(to); s != nil {
            q := s.Quality()
            base := linkBaseCost(s.TransportKind())
            rtt := float64(q.RTT) / float64(time.Millisecond)
            loss := float64(q.LossRatio)
            return base + rtt/10.0 + loss*50.0
        }
    }
    // Heuristic default cost
    // Prefer shorter paths; assume medium quality
    return 100.0
}

func linkBaseCost(k transport.Kind) float64 {
    switch k {
    case transport.KindQUICDirect:
        return 1.0
    case transport.KindTCPDirect:
        return 2.0
    case transport.KindQUICHolePunch:
        return 3.0
    case transport.KindQUICRelay, transport.KindTCPRelay:
        return 4.0
    case transport.KindUDP:
        return 5.0
    case transport.KindWinPipe, transport.KindMem:
        return 0.5
    default:
        return 10.0
    }
}

func (r *Router) findPathDijkstra(src, dst transport.PeerID) []transport.PeerID {
    g := buildGraph(r.ps, r.mgr, r.localID)
    start, goal := string(src), string(dst)
    // dist, prev
    dist := map[string]float64{start: 0}
    prev := map[string]string{}
    pq := &nodePQ{}
    heap.Init(pq)
    heap.Push(pq, nodeItem{id: start, prio: 0})
    visited := map[string]bool{}

    for pq.Len() > 0 {
        cur := heap.Pop(pq).(nodeItem)
        if visited[cur.id] { continue }
        visited[cur.id] = true
        if cur.id == goal { break }
        for nb, w := range g.adj[cur.id] {
            nd := dist[cur.id] + w
            if old, ok := dist[nb]; !ok || nd < old {
                dist[nb] = nd
                prev[nb] = cur.id
                heap.Push(pq, nodeItem{id: nb, prio: nd})
            }
        }
    }
    if _, ok := dist[goal]; !ok { return nil }
    // reconstruct
    var rev []string
    for at := goal; at != ""; at = prev[at] {
        rev = append(rev, at)
        if at == start { break }
    }
    // reverse
    path := make([]transport.PeerID, len(rev))
    for i := 0; i < len(rev); i++ {
        path[i] = transport.PeerID(rev[len(rev)-1-i])
    }
    return path
}

type nodeItem struct { id string; prio float64 }
type nodePQ []nodeItem
func (p nodePQ) Len() int { return len(p) }
func (p nodePQ) Less(i, j int) bool { return p[i].prio < p[j].prio }
func (p nodePQ) Swap(i, j int) { p[i], p[j] = p[j], p[i] }
func (p *nodePQ) Push(x interface{}) { *p = append(*p, x.(nodeItem)) }
func (p *nodePQ) Pop() interface{} { old := *p; n := len(old); x := old[n-1]; *p = old[:n-1]; return x }

// Helper
func toPeerIDs(path []string) []transport.PeerID {
    out := make([]transport.PeerID, len(path))
    for i := range path { out[i] = transport.PeerID(path[i]) }
    return out
}
