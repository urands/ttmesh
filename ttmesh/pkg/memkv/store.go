package memkv

import (
    "container/heap"
    "sync"
    "sync/atomic"
    "time"
)

// ========================= Опции =========================

type Options struct {
    Shards       int           // количество шардов (стандарт: 256)
    CopyOnSet    bool          // копировать ли []byte при Set (безопасно по умолчанию)
    CopyOnGet    bool          // копировать ли []byte при Get (безопасно по умолчанию)
    ExpireJitter time.Duration // можно добавить джиттер к TTL (0 = выкл)
    MaxBytes     uint64        // жёсткий лимит суммарного объёма значений (0 = без лимита)
}

func (o *Options) withDefaults() Options {
    res := *o
    if res.Shards <= 0 {
        res.Shards = 256
    }
    // Безопасные значения по умолчанию
    if !res.CopyOnSet {
        res.CopyOnSet = true
    }
    if !res.CopyOnGet {
        res.CopyOnGet = true
    }
    return res
}

// ========================= Store =========================

type Store struct {
    opts    Options
    shards  []shard
    expq    *expQueue
    closeCh chan struct{}
    wg      sync.WaitGroup

    nowFn    func() time.Time
    itemPool sync.Pool // для expItem

    // Метрики
    mKeys    atomic.Uint64
    mBytes   atomic.Uint64
    mSets    atomic.Uint64
    mGets    atomic.Uint64
    mHits    atomic.Uint64
    mMisses  atomic.Uint64
    mDels    atomic.Uint64
    mExpired atomic.Uint64
    mUpdates atomic.Uint64
}

type shard struct {
    mu sync.RWMutex
    m  map[string]*entry
}

type entry struct {
    val      []byte
    expireAt int64 // unix nano; 0 = без истечения
}

func New(opts Options) *Store {
    opts = opts.withDefaults()
    s := &Store{
        opts:    opts,
        shards:  make([]shard, opts.Shards),
        expq:    &expQueue{},
        closeCh: make(chan struct{}),
        nowFn:   time.Now,
        itemPool: sync.Pool{New: func() any { return &expItem{} }},
    }
    for i := range s.shards {
        s.shards[i].m = make(map[string]*entry, 1024)
    }
    heap.Init(s.expq)
    s.wg.Add(1)
    go s.expirer()
    return s
}

func (s *Store) Close() {
    // Сигнализируем горутине завершиться и разбудим её, если она ждёт cond.Wait().
    close(s.closeCh)
    // Разбудить expirer, если он ждёт на пустой очереди
    if s.expq != nil {
        s.expq.Lock()
        if s.expq.cond != nil {
            s.expq.cond.Broadcast()
        }
        s.expq.Unlock()
    }
    s.wg.Wait()
}

// ========================= Хэш и шард =========================

func (s *Store) shardFor(key string) *shard {
    // Быстрый FNV-1a 64 (упрощённый)
    var h uint64 = 1469598103934665603
    for i := 0; i < len(key); i++ {
        h ^= uint64(key[i])
        h *= 1099511628211
    }
    return &s.shards[int(h%uint64(len(s.shards)))]
}

// ========================= Вспомогалки для копий =========================

func (s *Store) copyIfNeeded(b []byte, doCopy bool) []byte {
    if !doCopy {
        return b
    }
    out := make([]byte, len(b))
    copy(out, b)
    return out
}

// ========================= учёт байтов =========================

// tryAddBytes пытается зарезервировать положительный дельта-объём.
// Возвращает true, если учёт произведён и лимит не нарушен.
func (s *Store) tryAddBytes(delta uint64) bool {
    if s.opts.MaxBytes == 0 {
        s.mBytes.Add(delta)
        return true
    }
    for {
        cur := s.mBytes.Load()
        next := cur + delta
        if next > s.opts.MaxBytes {
            return false
        }
        if s.mBytes.CompareAndSwap(cur, next) {
            return true
        }
    }
}

// addBytesDelta меняет счётчик байтов на отрицательную или положительную дельту.
// delta может быть < 0 (уменьшение) или > 0 (увеличение без проверки лимита).
func (s *Store) addBytesDelta(delta int64) {
    if delta == 0 {
        return
    }
    for {
        cur := s.mBytes.Load()
        var next uint64
        if delta > 0 {
            next = cur + uint64(delta)
        } else {
            sub := uint64(-delta)
            if sub > cur {
                next = 0
            } else {
                next = cur - sub
            }
        }
        if s.mBytes.CompareAndSwap(cur, next) {
            return
        }
    }
}

// ========================= Публичный API =========================

// Set устанавливает значение. Возвращает true, если ключ был создан (а не перезаписан).
func (s *Store) Set(key string, val []byte, ttl time.Duration) bool {
    now := s.nowFn()
    expAt := int64(0)
    if ttl > 0 {
        if s.opts.ExpireJitter > 0 {
            ttl += time.Duration(int64(s.opts.ExpireJitter) * (int64(now.UnixNano()) % 3 - 1)) // простой "джиттер"
            if ttl < 0 {
                ttl = 0
            }
        }
        expAt = now.Add(ttl).UnixNano()
    }
    v := s.copyIfNeeded(val, s.opts.CopyOnSet)

    sh := s.shardFor(key)
    sh.mu.Lock()
    prev, existed := sh.m[key]
    oldLen := 0
    if existed {
        oldLen = len(prev.val)
    }
    newLen := len(v)
    delta := newLen - oldLen
    if delta > 0 {
        if !s.tryAddBytes(uint64(delta)) {
            // превышение лимита — не записывать
            sh.mu.Unlock()
            return false
        }
    }
    sh.m[key] = &entry{val: v, expireAt: expAt}
    if !existed {
        s.mKeys.Add(1)
    } else if delta < 0 {
        s.addBytesDelta(int64(delta))
    }
    s.mSets.Add(1)

    if expAt != 0 {
        s.enqueueExpire(key, expAt)
    }
    sh.mu.Unlock()
    return !existed
}

// Get возвращает значение и наличие.
// Если opts.CopyOnGet = true — вернёт копию, иначе — прямую ссылку (unsafe).
func (s *Store) Get(key string) ([]byte, bool) {
    sh := s.shardFor(key)
    sh.mu.RLock()
    e, ok := sh.m[key]
    if !ok {
        sh.mu.RUnlock()
        s.mGets.Add(1)
        s.mMisses.Add(1)
        return nil, false
    }
    // проверка TTL без блокировки записи:
    exp := e.expireAt
    val := e.val
    sh.mu.RUnlock()

    if exp != 0 && exp <= s.nowFn().UnixNano() {
        // Ленивое удаление с перерасчётом метрик как истечение
        sh.mu.Lock()
        if e2, ok2 := sh.m[key]; ok2 && e2.expireAt != 0 && e2.expireAt <= s.nowFn().UnixNano() {
            delete(sh.m, key)
            s.mExpired.Add(1)
            s.mKeys.Add(^uint64(0))
            if e2.val != nil {
                s.addBytesDelta(int64(-len(e2.val)))
            }
        }
        sh.mu.Unlock()
        s.mGets.Add(1)
        s.mMisses.Add(1)
        return nil, false
    }
    s.mGets.Add(1)
    s.mHits.Add(1)
    if s.opts.CopyOnGet {
        out := make([]byte, len(val))
        copy(out, val)
        return out, true
    }
    return val, true
}

// GetNoCopy возвращает внутреннюю ссылку на значение без копирования.
// ВАЖНО: не изменяйте возвращаемый срез.
func (s *Store) GetNoCopy(key string) ([]byte, bool) {
    sh := s.shardFor(key)
    sh.mu.RLock()
    e, ok := sh.m[key]
    if !ok {
        sh.mu.RUnlock()
        s.mGets.Add(1)
        s.mMisses.Add(1)
        return nil, false
    }
    exp := e.expireAt
    val := e.val
    sh.mu.RUnlock()
    if exp != 0 && exp <= s.nowFn().UnixNano() {
        // ленивое удаление + корректные метрики истечения
        sh := s.shardFor(key)
        sh.mu.Lock()
        if e2, ok2 := sh.m[key]; ok2 && e2.expireAt != 0 && e2.expireAt <= s.nowFn().UnixNano() {
            delete(sh.m, key)
            s.mExpired.Add(1)
            s.mKeys.Add(^uint64(0))
            if e2.val != nil {
                s.addBytesDelta(int64(-len(e2.val)))
            }
        }
        sh.mu.Unlock()
        s.mGets.Add(1)
        s.mMisses.Add(1)
        return nil, false
    }
    s.mGets.Add(1)
    s.mHits.Add(1)
    return val, true
}

// GetDel — получить и удалить ключ атомарно.
func (s *Store) GetDel(key string) ([]byte, bool) {
    sh := s.shardFor(key)
    sh.mu.Lock()
    e, ok := sh.m[key]
    if !ok {
        sh.mu.Unlock()
        s.mGets.Add(1)
        s.mMisses.Add(1)
        return nil, false
    }
    // проверяем TTL до возврата
    if e.expireAt != 0 && e.expireAt <= s.nowFn().UnixNano() {
        delete(sh.m, key)
        sh.mu.Unlock()
        s.mExpired.Add(1)
        s.mKeys.Add(^uint64(0))
        if e.val != nil {
            s.addBytesDelta(int64(-len(e.val)))
        }
        s.mGets.Add(1)
        s.mMisses.Add(1)
        return nil, false
    }
    val := e.val
    delete(sh.m, key)
    sh.mu.Unlock()
    s.mDels.Add(1)
    s.mGets.Add(1)
    s.mHits.Add(1)
    s.mKeys.Add(^uint64(0))
    s.addBytesDelta(int64(-len(val)))

    if s.opts.CopyOnGet {
        out := make([]byte, len(val))
        copy(out, val)
        return out, true
    }
    return val, true
}

// GetAndDelete — синоним Redis GETDEL.
func (s *Store) GetAndDelete(key string) ([]byte, bool) { return s.GetDel(key) }

// Update применяет функцию-модификатор к значению, если ключ существует и не истёк.
// Возвращает true, если обновление произошло.
func (s *Store) Update(key string, fn func(old []byte) []byte) bool {
    sh := s.shardFor(key)
    now := s.nowFn().UnixNano()
    sh.mu.Lock()
    defer sh.mu.Unlock()
    e, ok := sh.m[key]
    if !ok {
        return false
    }
    if e.expireAt != 0 && e.expireAt <= now {
        delete(sh.m, key)
        return false
    }
    oldLen := len(e.val)
    newVal := fn(e.val)
    newLen := len(newVal)
    delta := newLen - oldLen
    if delta > 0 {
        if !s.tryAddBytes(uint64(delta)) {
            return false
        }
    }
    if s.opts.CopyOnSet { // для симметрии поведения
        buf := make([]byte, len(newVal))
        copy(buf, newVal)
        e.val = buf
    } else {
        e.val = newVal
    }
    if delta < 0 {
        s.addBytesDelta(int64(delta))
    }
    s.mUpdates.Add(1)
    return true
}

func (s *Store) Exists(key string) bool {
    _, ok := s.Get(key)
    return ok
}

func (s *Store) Delete(key string) bool {
    sh := s.shardFor(key)
    sh.mu.Lock()
    e, ok := sh.m[key]
    if ok {
        delete(sh.m, key)
    }
    sh.mu.Unlock()
    if ok {
        s.mDels.Add(1)
        s.mKeys.Add(^uint64(0))
        if e != nil {
            s.addBytesDelta(int64(-len(e.val)))
        }
    }
    return ok
}

// Expire задаёт TTL. Возвращает false, если ключа нет/истёк.
func (s *Store) Expire(key string, ttl time.Duration) bool {
    if ttl <= 0 {
        return s.Delete(key)
    }
    exp := s.nowFn().Add(ttl).UnixNano()

    sh := s.shardFor(key)
    sh.mu.Lock()
    defer sh.mu.Unlock()
    e, ok := sh.m[key]
    if !ok {
        return false
    }
    if e.expireAt != 0 && e.expireAt <= s.nowFn().UnixNano() {
        delete(sh.m, key)
        s.mExpired.Add(1)
        s.mKeys.Add(^uint64(0))
        s.addBytesDelta(int64(-len(e.val)))
        return false
    }
    e.expireAt = exp
    s.enqueueExpire(key, exp)
    return true
}

// TTL возвращает оставшееся время жизни и признак наличия.
// Если TTL не установлен — duration=0 и ok=true.
func (s *Store) TTL(key string) (time.Duration, bool) {
    sh := s.shardFor(key)
    sh.mu.RLock()
    e, ok := sh.m[key]
    if !ok {
        sh.mu.RUnlock()
        return 0, false
    }
    exp := e.expireAt
    sh.mu.RUnlock()

    if exp == 0 {
        return 0, true
    }
    now := s.nowFn().UnixNano()
    if exp <= now {
        // ленивое удаление
        s.Delete(key)
        return 0, false
    }
    return time.Duration(exp-now) * time.Nanosecond, true
}

// ========================= Метрики =========================

// Stats — снэпшот метрик. Получение не блокирует операции хранилища.
type Stats struct {
    Keys    uint64
    Bytes   uint64
    Sets    uint64
    Gets    uint64
    Hits    uint64
    Misses  uint64
    Dels    uint64
    Expired uint64
    Updates uint64
}

// Metrics возвращает мгновенный срез метрик без заметного влияния.
func (s *Store) Metrics() Stats {
    return Stats{
        Keys:    s.mKeys.Load(),
        Bytes:   s.mBytes.Load(),
        Sets:    s.mSets.Load(),
        Gets:    s.mGets.Load(),
        Hits:    s.mHits.Load(),
        Misses:  s.mMisses.Load(),
        Dels:    s.mDels.Load(),
        Expired: s.mExpired.Load(),
        Updates: s.mUpdates.Load(),
    }
}

// ========================= Очередь истечений =========================

type expItem struct {
    when int64
    key  string
    // индекс в куче для heap.Interface (не обязателен, но полезен при расширении)
    index int
}

type expQueue struct {
    sync.Mutex
    cond *sync.Cond
    expQueueInternal []*expItem
}

// Реализация heap.Interface на вложенном типе:
func (q expQueue) Len() int           { return len(q.expQueueInternal) }
func (q expQueue) Less(i, j int) bool { return q.expQueueInternal[i].when < q.expQueueInternal[j].when }
func (q expQueue) Swap(i, j int)      { q.expQueueInternal[i], q.expQueueInternal[j] = q.expQueueInternal[j], q.expQueueInternal[i]; q.expQueueInternal[i].index = i; q.expQueueInternal[j].index = j }
func (q *expQueue) Push(x any)        { it := x.(*expItem); it.index = len(q.expQueueInternal); q.expQueueInternal = append(q.expQueueInternal, it) }
func (q *expQueue) Pop() any          { old := q.expQueueInternal; n := len(old); it := old[n-1]; old[n-1] = nil; it.index = -1; q.expQueueInternal = old[:n-1]; return it }

func (s *Store) enqueueExpire(key string, when int64) {
    it := s.itemPool.Get().(*expItem)
    it.key = key
    it.when = when
    it.index = -1
    // Куча защищается собственной мьютексом
    s.expq.Lock()
    if s.expq.cond == nil {
        s.expq.cond = sync.NewCond(s.expq)
    }
    heap.Push(s.expq, it)
    s.expq.cond.Broadcast()
    s.expq.Unlock()
}

func (s *Store) expirer() {
    defer s.wg.Done()
    for {
        s.expq.Lock()
        for s.expq.Len() == 0 {
            if s.expq.cond == nil {
                s.expq.cond = sync.NewCond(s.expq)
            }
            // ждём сигналов или закрытия
            if s.isClosed() {
                s.expq.Unlock()
                return
            }
            s.expq.cond.Wait()
            if s.isClosed() {
                s.expq.Unlock()
                return
            }
        }
        // есть элементы
        it := s.expq.expQueueInternal[0]
        now := s.nowFn().UnixNano()
        if it.when > now {
            // уснуть до ближайшего дедлайна или пробуждения
            d := time.Duration(it.when-now) * time.Nanosecond
            timer := time.NewTimer(d)
            s.expq.Unlock()

            select {
            case <-timer.C:
            case <-s.closeCh:
                timer.Stop()
                return
            }
            // и повторить цикл
            continue
        }
        // просрочено — вытащить из кучи
        heap.Pop(s.expq)
        s.expq.Unlock()

        // Проверить и удалить (tombstone-совместимость)
        sh := s.shardFor(it.key)
        nowN := s.nowFn().UnixNano()
        sh.mu.Lock()
        e := sh.m[it.key]
        if e != nil && e.expireAt != 0 && e.expireAt <= nowN {
            delete(sh.m, it.key)
            s.mExpired.Add(1)
            s.mKeys.Add(^uint64(0))
            s.addBytesDelta(int64(-len(e.val)))
        }
        sh.mu.Unlock()

        // вернуть item в pool
        it.key = ""
        it.when = 0
        it.index = -1
        s.itemPool.Put(it)
    }
}

func (s *Store) isClosed() bool {
    select {
    case <-s.closeCh:
        return true
    default:
        return false
    }
}
