package memkv

import (
    "fmt"
    "math/rand/v2"
    "sync/atomic"
    "testing"
)

func BenchmarkSetGet_Parallel(b *testing.B) {
    s := New(Options{})
    defer s.Close()
    val := make([]byte, 256)
    var cnt atomic.Uint64
    b.ReportAllocs()
    b.SetBytes(int64(len(val)))
    b.RunParallel(func(pb *testing.PB) {
        for pb.Next() {
            id := cnt.Add(1)
            k := fmt.Sprintf("k%016x", id)
            s.Set(k, val, 0)
            // случайный один из N предыдущих ключей
            rid := id - 1 - uint64(rand.IntN(8))
            if rid > 0 {
                s.Get(fmt.Sprintf("k%016x", rid))
            }
        }
    })
}

