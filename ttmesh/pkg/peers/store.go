package peers

import (
    "encoding/json"
    "sync"
    "time"

    "go.uber.org/zap"

    "ttmesh/pkg/memkv"
    "ttmesh/pkg/transport"
)

// Store persists peer metadata/stats in the in-memory KV.
// It can be backed by an external persistent KV later.
type Store struct {
    kv *memkv.Store
    // lightweight index of known peer IDs for graph building
    idxMu     sync.RWMutex
    peerIndex map[string]struct{}
    routeIndex map[string]struct{}
}

func NewStore(kv *memkv.Store) *Store { return &Store{kv: kv, peerIndex: make(map[string]struct{}), routeIndex: make(map[string]struct{})} }

type PeerMeta struct {
    ID        transport.PeerID   `json:"id"`
    NodeName  string             `json:"node_name,omitempty"`
    Alg       string             `json:"alg,omitempty"`
    PublicKey []byte             `json:"public_key,omitempty"`
    Addresses []string           `json:"addresses,omitempty"`
    Reachable bool               `json:"reachable"`
    LastSeen  int64              `json:"last_seen_unix_ms"`
    Score     float32            `json:"score"`
    RTTms     uint32             `json:"rtt_ms"`
    LossRatio float32            `json:"loss_ratio"`
    Labels    map[string]string  `json:"labels,omitempty"`
    Routes    []RouteRef         `json:"routes,omitempty"`
    // Routing/role & handshake
    Kind      string             `json:"kind,omitempty"`            // client/node/relay/proxy/outbound_only
    Handshake string             `json:"handshake,omitempty"`       // hello_rx/verified/failed
    // Counters
    MsgsIn    uint64             `json:"msgs_in"`
    MsgsOut   uint64             `json:"msgs_out"`
    BytesIn   uint64             `json:"bytes_in"`
    BytesOut  uint64             `json:"bytes_out"`
    LastHelloTs int64            `json:"last_hello_ts_unix_ms"`
    LastAckTs   int64            `json:"last_ack_ts_unix_ms"`
    ConnectedDirectIDs []string  `json:"connected_direct_ids,omitempty"`
    // Preferred path (sequence of peer IDs) to reach this peer when not direct
    ViaPath  []string            `json:"via_path,omitempty"`
}

// RouteRef mirrors the intent of proto RouteRef, but with a lightweight JSON
// form for the in-memory store.
type RouteRef struct {
    ID      string `json:"id"`                 // route identifier (e.g., workflow/route name)
    Version uint32 `json:"version,omitempty"`  // optional version
    Hash    []byte `json:"hash,omitempty"`     // optional integrity hash
}

// RouteTo describes a learned path to a target (peer or logical route id).
// Path is a path-vector-style list of peer IDs; when we receive the route from
// peer X with path P, we store P+[X] (добавляем источник в конец списка).
type RouteTo struct {
    Target      string   `json:"target"`            // e.g., "peer:<id>" or "route:<id>"
    Path        []string `json:"path"`              // ordered list of peer IDs
    NextHop     string   `json:"next_hop"`          // immediate neighbor to forward to (usually last of Path)
    Metric      uint32   `json:"metric,omitempty"`  // optional combined metric (hops/quality)
    LearnedFrom string   `json:"learned_from"`      // peerID we learned from
    Updated     int64    `json:"updated_unix_ms"`
}

func keyPeer(id transport.PeerID) string { return "peer:" + string(id) }

func (s *Store) Upsert(meta PeerMeta) {
    b, _ := json.Marshal(meta)
    s.kv.Set(keyPeer(meta.ID), b, defaultPeerTTL)
    s.idxMu.Lock(); s.peerIndex[string(meta.ID)] = struct{}{}; s.idxMu.Unlock()
    zap.L().Debug("peer upsert", zap.String("peer", string(meta.ID)), zap.Strings("addrs", meta.Addresses))
}

func (s *Store) Get(id transport.PeerID) (PeerMeta, bool) {
    b, ok := s.kv.Get(keyPeer(id))
    if !ok { return PeerMeta{}, false }
    var pm PeerMeta
    if err := json.Unmarshal(b, &pm); err != nil { return PeerMeta{}, false }
    return pm, true
}

// Touch updates last-seen and addresses list.
func (s *Store) Touch(id transport.PeerID, addr string, when time.Time) {
    _ = s.kv.Update(keyPeer(id), func(old []byte) []byte {
        var pm PeerMeta
        _ = json.Unmarshal(old, &pm)
        pm.ID = id
        if when.IsZero() { when = time.Now() }
        pm.LastSeen = when.UnixMilli()
        if addr != "" {
            // append if missing
            found := false
            for _, a := range pm.Addresses { if a == addr { found = true; break } }
            if !found { pm.Addresses = append(pm.Addresses, addr) }
        }
        b, _ := json.Marshal(pm)
        return b
    })
    // refresh TTL
    _ = s.kv.Expire(keyPeer(id), defaultPeerTTL)
    s.idxMu.Lock(); s.peerIndex[string(id)] = struct{}{}; s.idxMu.Unlock()
    if addr != "" { zap.L().Debug("peer touch", zap.String("peer", string(id)), zap.String("addr", addr)) } else { zap.L().Debug("peer touch", zap.String("peer", string(id))) }
}

func (s *Store) RecordQuality(id transport.PeerID, q transport.Quality) {
    _ = s.kv.Update(keyPeer(id), func(old []byte) []byte {
        var pm PeerMeta
        _ = json.Unmarshal(old, &pm)
        pm.ID = id
        if q.RTT > 0 { pm.RTTms = uint32(q.RTT / time.Millisecond) }
        if q.LossRatio >= 0 { pm.LossRatio = q.LossRatio }
        pm.Score = q.Score
        if !q.LastSeen.IsZero() { pm.LastSeen = q.LastSeen.UnixMilli() }
        b, _ := json.Marshal(pm)
        return b
    })
    zap.L().Debug("peer quality", zap.String("peer", string(id)), zap.Float32("loss", q.LossRatio), zap.Duration("rtt", q.RTT), zap.Float32("score", q.Score))
}

// RecordExchange updates message/byte counters for a peer.
func (s *Store) RecordExchange(id transport.PeerID, inBytes, outBytes, inMsgs, outMsgs uint64) {
    _ = s.kv.Update(keyPeer(id), func(old []byte) []byte {
        var pm PeerMeta
        _ = json.Unmarshal(old, &pm)
        pm.ID = id
        pm.MsgsIn += inMsgs
        pm.MsgsOut += outMsgs
        pm.BytesIn += inBytes
        pm.BytesOut += outBytes
        b, _ := json.Marshal(pm)
        return b
    })
}


// AddConnectedDirect adds a direct adjacency if it's not present.
func (s *Store) AddConnectedDirect(id transport.PeerID, peerID transport.PeerID) {
    if id == peerID || string(id) == "" || string(peerID) == "" {
        return
    }
    _ = s.kv.Update(keyPeer(id), func(old []byte) []byte {
        var pm PeerMeta
        _ = json.Unmarshal(old, &pm)
        pm.ID = id
        found := false
        for _, v := range pm.ConnectedDirectIDs { if v == string(peerID) { found = true; break } }
        if !found { pm.ConnectedDirectIDs = append(pm.ConnectedDirectIDs, string(peerID)) }
        b, _ := json.Marshal(pm)
        return b
    })
    // Ensure both peers are indexed for graph building
    s.idxMu.Lock()
    s.peerIndex[string(id)] = struct{}{}
    s.peerIndex[string(peerID)] = struct{}{}
    s.idxMu.Unlock()
    zap.L().Info("adjacency added", zap.String("from", string(id)), zap.String("to", string(peerID)))
}

// RemoveConnectedDirect removes direct adjacency if present.
func (s *Store) RemoveConnectedDirect(id transport.PeerID, peerID transport.PeerID) {
    _ = s.kv.Update(keyPeer(id), func(old []byte) []byte {
        var pm PeerMeta
        _ = json.Unmarshal(old, &pm)
        if pm.ConnectedDirectIDs == nil { return old }
        out := pm.ConnectedDirectIDs[:0]
        for _, v := range pm.ConnectedDirectIDs { if v != string(peerID) { out = append(out, v) } }
        pm.ConnectedDirectIDs = out
        b, _ := json.Marshal(pm)
        return b
    })
    zap.L().Info("adjacency removed", zap.String("from", string(id)), zap.String("to", string(peerID)))
}

// DeletePeer removes peer meta and any adjacency references to it.
func (s *Store) DeletePeer(id transport.PeerID) {
    _ = s.kv.Delete(keyPeer(id))
    s.idxMu.Lock()
    delete(s.peerIndex, string(id))
    s.idxMu.Unlock()
    // remove from adjacency of all peers
    ids := s.ListPeerIDs()
    for _, pid := range ids {
        s.RemoveConnectedDirect(pid, id)
    }
    zap.L().Info("peer deleted", zap.String("peer", string(id)))
}

// ExpirePeer sets a custom TTL for a peer entry; useful for retiring temp:* peers
// shortly after successful hello without immediate deletion.
func (s *Store) ExpirePeer(id transport.PeerID, ttl time.Duration) {
    _ = s.kv.Expire(keyPeer(id), ttl)
    zap.L().Debug("peer expire set", zap.String("peer", string(id)), zap.Duration("ttl", ttl))
}

// LearnPath stores/updates a path-vector route to a generic target string.
// If an existing route is present, it is replaced only if the new metric is
// better (smaller), or equal metric but shorter path.
func (s *Store) LearnPath(target string, learnedFrom transport.PeerID, prevPath []string, metric uint32) (RouteTo, bool) {
    now := time.Now().UnixMilli()
    key := "routepath:" + target
    var out RouteTo
    updated := false
    _ = s.kv.Update(key, func(old []byte) []byte {
        var cur RouteTo
        _ = json.Unmarshal(old, &cur)
        // Build new path: prevPath + learnedFrom
        np := make([]string, 0, len(prevPath)+1)
        np = append(np, prevPath...)
        np = append(np, string(learnedFrom))
        new := RouteTo{
            Target:      target,
            Path:        np,
            NextHop:     string(learnedFrom),
            Metric:      metric,
            LearnedFrom: string(learnedFrom),
            Updated:     now,
        }
        // Decide replacement
        replace := false
        if cur.Target == "" {
            replace = true
        } else if metric < cur.Metric {
            replace = true
        } else if metric == cur.Metric && len(np) < len(cur.Path) {
            replace = true
        }
        if replace {
            out = new
            updated = true
            b, _ := json.Marshal(new)
            return b
        }
        out = cur
        return old
    })
    if updated {
        zap.L().Info("route learned", zap.String("target", target), zap.Strings("path", out.Path), zap.Uint32("metric", out.Metric), zap.String("nexthop", out.NextHop))
    } else {
        zap.L().Debug("route ignored (worse)", zap.String("target", target), zap.Uint32("metric", out.Metric))
    }
    return out, updated
}

// LearnPeerPath convenience wrapper for target peer.
func (s *Store) LearnPeerPath(target transport.PeerID, learnedFrom transport.PeerID, prevPath []transport.PeerID, metric uint32) (RouteTo, bool) {
    pp := make([]string, len(prevPath))
    for i := range prevPath { pp[i] = string(prevPath[i]) }
    return s.LearnPath("peer:"+string(target), learnedFrom, pp, metric)
}

// GetPath returns a learned route for target, if any.
func (s *Store) GetPath(target string) (RouteTo, bool) {
    b, ok := s.kv.Get("routepath:" + target)
    if !ok { return RouteTo{}, false }
    var r RouteTo
    if err := json.Unmarshal(b, &r); err != nil { return RouteTo{}, false }
    return r, true
}

// SetViaPathForPeer sets preferred ViaPath on peer meta (best path to reach peer).
func (s *Store) SetViaPathForPeer(id transport.PeerID, path []transport.PeerID) {
    _ = s.kv.Update(keyPeer(id), func(old []byte) []byte {
        var pm PeerMeta
        _ = json.Unmarshal(old, &pm)
        pm.ID = id
        pm.ViaPath = pm.ViaPath[:0]
        for _, p := range path { pm.ViaPath = append(pm.ViaPath, string(p)) }
        b, _ := json.Marshal(pm)
        return b
    })
}

// AdvertiseRoutes replaces the peer's advertised/known routes with the given list.
func (s *Store) AdvertiseRoutes(id transport.PeerID, routes []RouteRef) {
    _ = s.kv.Update(keyPeer(id), func(old []byte) []byte {
        var pm PeerMeta
        _ = json.Unmarshal(old, &pm)
        pm.ID = id
        pm.Routes = append([]RouteRef(nil), routes...)
        b, _ := json.Marshal(pm)
        return b
    })
    zap.L().Info("routes advertised", zap.String("peer", string(id)), zap.Int("count", len(routes)))
}

// ListPeerIDs returns a snapshot of known peer IDs.
func (s *Store) ListPeerIDs() []transport.PeerID {
    s.idxMu.RLock(); defer s.idxMu.RUnlock()
    out := make([]transport.PeerID, 0, len(s.peerIndex))
    for id := range s.peerIndex { out = append(out, transport.PeerID(id)) }
    return out
}

// -------- Multi-source route candidates with TTL & health --------

type RouteCandidate struct {
    Target      string   `json:"target"`
    Path        []string `json:"path"`
    NextHop     string   `json:"next_hop"`
    Metric      uint32   `json:"metric"`
    Source      string   `json:"source"`       // external source tag (gossip/manual/peer)
    LearnedFrom string   `json:"learned_from"` // peer id we learned from
    Health      string   `json:"health"`       // "up"/"down"/"unknown"
    Updated     int64    `json:"updated_unix_ms"`
    Expires     int64    `json:"expires_unix_ms"`
}

func keyRouteCands(target string) string { return "routecands:" + target }

func (s *Store) UpsertRouteCandidate(rc RouteCandidate, ttl time.Duration) {
    now := time.Now().UnixMilli()
    if ttl <= 0 { ttl = 60 * time.Second }
    rc.Updated = now
    rc.Expires = now + ttl.Milliseconds()
    key := keyRouteCands(rc.Target)
    _ = s.kv.Update(key, func(old []byte) []byte {
        var arr []RouteCandidate
        _ = json.Unmarshal(old, &arr)
        // prune expired and replace same (by LearnedFrom)
        keep := arr[:0]
        replaced := false
        for _, c := range arr {
            if c.Expires <= now { continue }
            if c.LearnedFrom == rc.LearnedFrom && c.Source == rc.Source {
                if !replaced { keep = append(keep, rc); replaced = true }
                continue
            }
            keep = append(keep, c)
        }
        if !replaced { keep = append(keep, rc) }
        b, _ := json.Marshal(keep)
        return b
    })
    zap.L().Debug("route cand upsert", zap.String("target", rc.Target), zap.Strings("path", rc.Path), zap.Uint32("metric", rc.Metric), zap.String("from", rc.LearnedFrom), zap.String("source", rc.Source))
    s.idxMu.Lock(); s.routeIndex[rc.Target] = struct{}{}; s.idxMu.Unlock()
}

func (s *Store) ConfirmRoute(target string, learnedFrom transport.PeerID, source string, up bool, ttl time.Duration) {
    now := time.Now().UnixMilli()
    key := keyRouteCands(target)
    _ = s.kv.Update(key, func(old []byte) []byte {
        var arr []RouteCandidate
        _ = json.Unmarshal(old, &arr)
        for i := range arr {
            if arr[i].LearnedFrom == string(learnedFrom) && arr[i].Source == source {
                if up { arr[i].Health = "up" } else { arr[i].Health = "down" }
                if ttl > 0 { arr[i].Expires = now + ttl.Milliseconds() }
                arr[i].Updated = now
            }
        }
        b, _ := json.Marshal(arr)
        return b
    })
    zap.L().Debug("route confirm", zap.String("target", target), zap.String("from", string(learnedFrom)), zap.String("source", source), zap.Bool("up", up))
}

func (s *Store) GetRouteCandidates(target string) []RouteCandidate {
    now := time.Now().UnixMilli()
    b, ok := s.kv.Get(keyRouteCands(target))
    if !ok { return nil }
    var arr []RouteCandidate
    _ = json.Unmarshal(b, &arr)
    out := arr[:0]
    for _, c := range arr { if c.Expires > now { out = append(out, c) } }
    return append([]RouteCandidate(nil), out...)
}

func (s *Store) BestRouteCandidate(target string) (RouteCandidate, bool) {
    cands := s.GetRouteCandidates(target)
    if len(cands) == 0 { return RouteCandidate{}, false }
    // pick best by score
    bestIdx := 0
    bestScore := routeScore(cands[0])
    for i := 1; i < len(cands); i++ {
        sc := routeScore(cands[i])
        if sc > bestScore { bestIdx, bestScore = i, sc }
    }
    return cands[bestIdx], true
}

// ListRouteTargets returns targets for which we have candidates.
func (s *Store) ListRouteTargets() []string {
    s.idxMu.RLock(); defer s.idxMu.RUnlock()
    out := make([]string, 0, len(s.routeIndex))
    for t := range s.routeIndex { out = append(out, t) }
    return out
}

func routeScore(c RouteCandidate) float64 {
    var h float64
    switch c.Health {
    case "up": h = 1
    case "unknown": h = 0.5
    default: h = 0
    }
    metric := float64(c.Metric)
    if metric <= 0 { metric = 1 }
    plen := float64(len(c.Path))
    fresh := float64(c.Updated) / 1e12
    return h*10 + (1000/(metric+plen)) + fresh
}

// defaultPeerTTL defines inactivity TTL for peer metadata before it is expired.
const defaultPeerTTL = 5 * time.Minute
