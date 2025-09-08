//go:build memkv_huge

package memkv

import (
    "bytes"
    "fmt"
    "os"
    "strconv"
    "testing"
    "time"
)

// TestHugeInsert — тяжёлый тест производительности и памяти.
// Запускать вручную:
//   MEMKV_ITEMS=2000000 MEMKV_VALUE_BYTES=1024 go test -tags memkv_huge -run TestHugeInsert -timeout 0 -v ./pkg/memkv
// или ограничить суммарный объём в гигабайтах:
//   MEMKV_GB=3 go test -tags memkv_huge -run TestHugeInsert -timeout 0 -v ./pkg/memkv
func TestHugeInsert(t *testing.T) {
    items := getenvInt("MEMKV_ITEMS", 0)
    valBytes := getenvInt("MEMKV_VALUE_BYTES", 1024)
    gb := getenvInt("MEMKV_GB", 0)

    if gb > 0 {
        // подсчёт по суммарному объёму
        total := uint64(gb) << 30
        if valBytes <= 0 {
            valBytes = 1024
        }
        items = int(total / uint64(valBytes))
        if items <= 0 {
            t.Skip("parameters produce zero items")
        }
    }
    if items <= 0 {
        t.Skip("set MEMKV_ITEMS or MEMKV_GB to run heavy test")
    }

    s := New(Options{})
    defer s.Close()

    v := bytes.Repeat([]byte{'A'}, valBytes)
    start := time.Now()
    for i := 0; i < items; i++ {
        if i%1_000_000 == 0 && i > 0 {
            t.Logf("progress: %d/%d (%.1f%%)", i, items, float64(i)*100/float64(items))
        }
        k := fmt.Sprintf("k%09d", i)
        if !s.Set(k, v, 0) {
            t.Fatalf("unexpected Set failure at i=%d", i)
        }
    }
    dur := time.Since(start)
    st := s.Metrics()
    t.Logf("inserted: %d keys, bytes=%d, took=%s", st.Keys, st.Bytes, dur)
}

// TestMaxBytesHuge проверяет жёсткий лимит на объём данных.
// Пример запуска:
//   MEMKV_GB=2 go test -tags memkv_huge -run TestMaxBytesHuge -timeout 0 -v ./pkg/memkv
func TestMaxBytesHuge(t *testing.T) {
    gb := getenvInt("MEMKV_GB", 1)
    valBytes := getenvInt("MEMKV_VALUE_BYTES", 1024)
    total := uint64(gb) << 30
    items := int(total / uint64(valBytes))
    if items <= 0 {
        t.Skip("invalid params for huge test")
    }

    s := New(Options{MaxBytes: total})
    defer s.Close()
    v := bytes.Repeat([]byte{'B'}, valBytes)

    // заполняем до лимита
    filled := 0
    for i := 0; i < items; i++ {
        if !s.Set(fmt.Sprintf("x%09d", i), v, 0) {
            t.Fatalf("unexpected reject before reaching MaxBytes")
        }
        filled++
    }
    // следующая запись должна быть отклонена
    if s.Set("overflow", v, 0) {
        t.Fatalf("expected reject on exceeding MaxBytes")
    }
    st := s.Metrics()
    if st.Bytes > s.opts.MaxBytes || st.Keys != uint64(filled) {
        t.Fatalf("metrics mismatch: bytes=%d keys=%d filled=%d", st.Bytes, st.Keys, filled)
    }
}

func getenvInt(name string, def int) int {
    s := os.Getenv(name)
    if s == "" {
        return def
    }
    x, err := strconv.ParseInt(s, 10, 64)
    if err != nil {
        return def
    }
    return int(x)
}

