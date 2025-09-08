package priocq

import (
    "sync"
    "time"
)

// TokenBucket is a simple leaky bucket for shaping.
type TokenBucket struct {
    mu       sync.Mutex
    capacity int64
    tokens   int64
    rate     int64 // tokens per second
    last     time.Time
}

func NewTokenBucket(ratePerSec, capacity int64) *TokenBucket {
    if capacity <= 0 { capacity = ratePerSec }
    return &TokenBucket{capacity: capacity, tokens: capacity, rate: ratePerSec, last: time.Now()}
}

// Allow tries to consume n tokens; if not enough, returns duration to wait.
func (b *TokenBucket) Allow(n int64) (ok bool, wait time.Duration) {
    b.mu.Lock(); defer b.mu.Unlock()
    now := time.Now()
    if b.last.IsZero() { b.last = now }
    // Refill
    dt := now.Sub(b.last)
    if dt > 0 {
        add := (b.rate * dt.Nanoseconds()) / int64(time.Second)
        if add > 0 {
            b.tokens += add
            if b.tokens > b.capacity { b.tokens = b.capacity }
            b.last = now
        }
    }
    if b.tokens >= n {
        b.tokens -= n
        return true, 0
    }
    need := n - b.tokens
    // time to accumulate need
    nanos := (need * int64(time.Second)) / b.rate
    return false, time.Duration(nanos)
}

