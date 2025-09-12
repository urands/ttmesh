package registry

import (
    "encoding/json"
    "sort"
    "strconv"
    "strings"
    "sync"
    "time"

    "go.uber.org/zap"

    "ttmesh/pkg/memkv"
    ttmeshproto "ttmesh/pkg/protocol/proto"
)

// Store keeps track of registered workers and their task capabilities.
// Backed by memkv for now; can be swapped to persistent KV later.
type Store struct {
    kv *memkv.Store
    mu sync.RWMutex
    // simple index of worker ids for listings (avoids scanning KV shard maps)
    workers map[string]struct{}
}

func NewStore(kv *memkv.Store) *Store { return &Store{kv: kv, workers: make(map[string]struct{})} }

// Internal JSON document representation for a worker record.
type workerDoc struct {
    WorkerID       string                               `json:"worker_id"`
    Self           *ttmeshproto.NodeRef                 `json:"self,omitempty"`
    Endpoints      []*ttmeshproto.NetworkEndpoint       `json:"endpoints,omitempty"`
    Features       *ttmeshproto.FeatureFlags            `json:"features,omitempty"`
    Labels         map[string]string                    `json:"labels,omitempty"`
    CapacityTotal  *ttmeshproto.ResourceCapacity        `json:"capacity_total,omitempty"`
    CapacityFree   *ttmeshproto.ResourceCapacity        `json:"capacity_free,omitempty"`
    Tasks          []*ttmeshproto.TaskDescriptor        `json:"tasks,omitempty"`
    UpdatedUnixMs  int64                                `json:"updated_unix_ms"`
}

func keyWorker(id string) string { return "reg:worker:" + id }

// Register replaces or creates a worker record with provided tasks and metadata.
func (s *Store) Register(req *ttmeshproto.TaskRegister) (accepted bool, reason string, conflicts []string) {
    wid := strings.TrimSpace(req.GetWorkerId())
    if wid == "" {
        return false, "missing worker_id", nil
    }
    now := time.Now().UnixMilli()
    doc := workerDoc{
        WorkerID:      wid,
        Self:          req.GetSelf(),
        Endpoints:     append([]*ttmeshproto.NetworkEndpoint(nil), req.GetEndpoints()...),
        Features:      req.GetFeatures(),
        Labels:        mapCopy(req.GetLabels()),
        Tasks:         dedupTasks(req.GetTasks()),
        UpdatedUnixMs: now,
    }
    b, _ := json.Marshal(doc)
    s.kv.Set(keyWorker(wid), b, 0)
    s.mu.Lock(); s.workers[wid] = struct{}{}; s.mu.Unlock()
    zap.L().Info("worker registered", zap.String("worker", wid), zap.Int("tasks", len(doc.Tasks)))
    return true, "", conflicts
}

// Update applies upserts/removals and optional endpoint/label deltas.
func (s *Store) Update(req *ttmeshproto.TaskUpdate) {
    wid := strings.TrimSpace(req.GetWorkerId())
    if wid == "" { return }
    _ = s.kv.Update(keyWorker(wid), func(old []byte) []byte {
        var doc workerDoc
        _ = json.Unmarshal(old, &doc)
        doc.WorkerID = wid
        // endpoints replace when provided
        if eps := req.GetEndpoints(); len(eps) > 0 { doc.Endpoints = append([]*ttmeshproto.NetworkEndpoint(nil), eps...) }
        // labels set/del
        if doc.Labels == nil { doc.Labels = make(map[string]string) }
        for k, v := range req.GetLabelsSet() { doc.Labels[k] = v }
        for _, k := range req.GetLabelsDel() { delete(doc.Labels, k) }
        // tasks upsert/remove
        if len(req.GetAddOrUpdate()) > 0 || len(req.GetRemove()) > 0 {
            byKey := make(map[string]*ttmeshproto.TaskDescriptor)
            for _, t := range doc.Tasks { byKey[taskKeyOf(t)] = t }
            for _, t := range req.GetAddOrUpdate() { byKey[taskKeyOf(t)] = t }
            for _, k := range req.GetRemove() { delete(byKey, taskKeyStr(k)) }
            doc.Tasks = doc.Tasks[:0]
            for _, t := range byKey { doc.Tasks = append(doc.Tasks, t) }
            sort.Slice(doc.Tasks, func(i, j int) bool { return taskKeyOf(doc.Tasks[i]) < taskKeyOf(doc.Tasks[j]) })
        }
        doc.UpdatedUnixMs = time.Now().UnixMilli()
        b, _ := json.Marshal(doc)
        return b
    })
    s.mu.Lock(); s.workers[wid] = struct{}{}; s.mu.Unlock()
    zap.L().Info("worker updated", zap.String("worker", wid))
}

// Deregister removes specific tasks or the whole worker.
func (s *Store) Deregister(req *ttmeshproto.TaskDeregister) {
    wid := strings.TrimSpace(req.GetWorkerId())
    if wid == "" { return }
    if len(req.GetTasks()) == 0 {
        s.kv.Delete(keyWorker(wid))
        s.mu.Lock(); delete(s.workers, wid); s.mu.Unlock()
        zap.L().Info("worker deregistered", zap.String("worker", wid))
        return
    }
    // remove select tasks
    _ = s.kv.Update(keyWorker(wid), func(old []byte) []byte {
        var doc workerDoc
        if err := json.Unmarshal(old, &doc); err != nil { return old }
        want := make(map[string]struct{})
        for _, k := range req.GetTasks() { want[taskKeyStr(k)] = struct{}{} }
        out := doc.Tasks[:0]
        for _, t := range doc.Tasks { if _, del := want[taskKeyOf(t)]; !del { out = append(out, t) } }
        doc.Tasks = out
        doc.UpdatedUnixMs = time.Now().UnixMilli()
        b, _ := json.Marshal(doc)
        return b
    })
    zap.L().Info("worker tasks deregistered", zap.String("worker", wid), zap.Int("count", len(req.GetTasks())))
}

// ListWorkers returns a snapshot matching filters and pagination.
func (s *Store) ListWorkers(req *ttmeshproto.TaskListWorkers) *ttmeshproto.TaskListWorkersReply {
    labelFilter := req.GetLabelFilter()
    fn := strings.TrimSpace(req.GetFunctionName())
    ver := strings.TrimSpace(req.GetVersion())
    includeTasks := req.GetIncludeTasks()
    pageSize := int(req.GetPageSize())
    if pageSize <= 0 { pageSize = 100 }
    start := 0
    if tok := strings.TrimSpace(req.GetPageToken()); tok != "" {
        if n, err := strconv.Atoi(tok); err == nil && n >= 0 { start = n }
    }

    // Collect and sort ids for stable ordering
    s.mu.RLock()
    ids := make([]string, 0, len(s.workers))
    for id := range s.workers { ids = append(ids, id) }
    s.mu.RUnlock()
    sort.Strings(ids)

    var out []*ttmeshproto.WorkerRecord
    matched := 0
    for _, id := range ids {
        if matched < start { matched++; continue }
        var doc workerDoc
        if b, ok := s.kv.Get(keyWorker(id)); ok {
            _ = json.Unmarshal(b, &doc)
        } else {
            continue
        }
        if !labelsMatch(doc.Labels, labelFilter) { continue }
        if fn != "" && !hasTask(doc.Tasks, fn, ver) { continue }
        rec := &ttmeshproto.WorkerRecord{
            WorkerId:      doc.WorkerID,
            Self:          doc.Self,
            Endpoints:     doc.Endpoints,
            Features:      doc.Features,
            Labels:        doc.Labels,
            CapacityTotal: doc.CapacityTotal,
            CapacityFree:  doc.CapacityFree,
        }
        if includeTasks { rec.Tasks = doc.Tasks }
        out = append(out, rec)
        matched++
        if len(out) >= pageSize { break }
    }

    rep := &ttmeshproto.TaskListWorkersReply{Workers: out}
    if matched < len(ids) {
        rep.NextPageToken = strconv.Itoa(matched)
    }
    return rep
}

// ---------- helpers ----------

func labelsMatch(have, need map[string]string) bool {
    if len(need) == 0 { return true }
    for k, v := range need {
        if hv, ok := have[k]; !ok || hv != v { return false }
    }
    return true
}

func hasTask(tasks []*ttmeshproto.TaskDescriptor, name, version string) bool {
    for _, t := range tasks {
        if t.GetName() != name { continue }
        if version == "" || t.GetVersion() == version { return true }
    }
    return false
}

func taskKeyOf(t *ttmeshproto.TaskDescriptor) string {
    if t == nil { return "" }
    if sid := strings.TrimSpace(t.GetStableId()); sid != "" { return "sid:" + sid }
    return strings.TrimSpace(t.GetName()) + "@" + strings.TrimSpace(t.GetVersion())
}

func taskKeyStr(k *ttmeshproto.TaskKey) string {
    if k == nil { return "" }
    if sid := strings.TrimSpace(k.GetStableId()); sid != "" { return "sid:" + sid }
    return strings.TrimSpace(k.GetName()) + "@" + strings.TrimSpace(k.GetVersion())
}

func dedupTasks(in []*ttmeshproto.TaskDescriptor) []*ttmeshproto.TaskDescriptor {
    if len(in) == 0 { return nil }
    m := make(map[string]*ttmeshproto.TaskDescriptor)
    for _, t := range in { m[taskKeyOf(t)] = t }
    out := make([]*ttmeshproto.TaskDescriptor, 0, len(m))
    for _, t := range m { out = append(out, t) }
    sort.Slice(out, func(i, j int) bool { return taskKeyOf(out[i]) < taskKeyOf(out[j]) })
    return out
}

func mapCopy[K comparable, V any](in map[K]V) map[K]V {
    if in == nil { return nil }
    out := make(map[K]V, len(in))
    for k, v := range in { out[k] = v }
    return out
}

