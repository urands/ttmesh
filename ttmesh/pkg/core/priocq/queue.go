package priocq

import (
    "sync"
    "time"
)

// Class is a priority class: L0 control > L1 realtime > L2 bulk
type Class int

const (
    L0Control Class = iota
    L1Realtime
    L2Bulk
    numClasses
)

type Item struct {
    Bytes    []byte
    Dest     string // peerID string
    Size     int
    Class    Class
    Deadline int64 // unix ms, 0 if none
    Src      string
    Arrived  time.Time
}

// flow implements a DRR queue per destination
type flow struct {
    key     string
    q       []Item
    deficit int
    quantum int
}

type level struct {
    mu    sync.Mutex
    flows map[string]*flow // key -> flow
    order []string         // round robin order
    idx   int
}

// MultiLevelQueue: strict priority between levels, DRR within level.
type MultiLevelQueue struct {
    lvls [numClasses]*level
    // cond to signal availability
    mu   sync.Mutex
    cond *sync.Cond
}

func New() *MultiLevelQueue {
    mlq := &MultiLevelQueue{}
    mlq.cond = sync.NewCond(&mlq.mu)
    for i := 0; i < int(numClasses); i++ {
        mlq.lvls[i] = &level{flows: make(map[string]*flow), order: make([]string, 0, 8)}
    }
    return mlq
}

// Enqueue appends an item to the appropriate class/flow.
func (q *MultiLevelQueue) Enqueue(it Item) {
    lvl := q.lvls[it.Class]
    lvl.mu.Lock()
    f := lvl.flows[it.Dest]
    if f == nil {
        f = &flow{key: it.Dest, quantum: chooseQuantum(it.Class)}
        lvl.flows[it.Dest] = f
        lvl.order = append(lvl.order, it.Dest)
    }
    f.q = append(f.q, it)
    lvl.mu.Unlock()
    q.mu.Lock()
    q.cond.Broadcast()
    q.mu.Unlock()
}

func chooseQuantum(c Class) int {
    switch c {
    case L0Control:
        return 2048 // small packets, quick turn
    case L1Realtime:
        return 8192
    case L2Bulk:
        return 65536
    default:
        return 4096
    }
}

// Dequeue selects the next item using strict priority and DRR within a level.
// Blocks until an item is available or ctx is done (context provided via stop channel).
func (q *MultiLevelQueue) Dequeue(stop <-chan struct{}) (Item, bool) {
    for {
        // Try fast path without waiting
        if it, ok := q.tryPop(); ok {
            return it, true
        }
        // Wait for signal or stop
        q.mu.Lock()
        done := false
        go func() {
            select { case <-stop: done = true; q.cond.Broadcast(); default: }
        }()
        q.cond.Wait()
        q.mu.Unlock()
        if done {
            return Item{}, false
        }
    }
}

func (q *MultiLevelQueue) tryPop() (Item, bool) {
    for li := 0; li < int(numClasses); li++ {
        lvl := q.lvls[li]
        lvl.mu.Lock()
        // No flows
        if len(lvl.order) == 0 {
            lvl.mu.Unlock(); continue
        }
        // Iterate over flows to find eligible packet via DRR
        n := len(lvl.order)
        start := lvl.idx
        for i := 0; i < n; i++ {
            j := (start + i) % n
            k := lvl.order[j]
            f := lvl.flows[k]
            if f == nil || len(f.q) == 0 { continue }
            // Refill deficit if empty
            if f.deficit <= 0 { f.deficit += f.quantum }
            // Peek size
            sz := f.q[0].Size
            if sz <= f.deficit {
                it := f.q[0]
                // Pop front
                copy(f.q[0:], f.q[1:])
                f.q = f.q[:len(f.q)-1]
                f.deficit -= sz
                lvl.idx = (j + 1) % n
                // Garbage collect empty flows
                if len(f.q) == 0 && f.deficit >= f.quantum*4 {
                    // keep flow but shrink deficit
                    f.deficit = 0
                }
                lvl.mu.Unlock()
                return it, true
            }
            // Not enough deficit; move on
        }
        lvl.mu.Unlock()
        // If reached here, either flows empty or not enough deficit; next loop will refill deficit
    }
    return Item{}, false
}

