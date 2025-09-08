package memkv

import (
    "reflect"
    "testing"
    "time"
)

func TestSetGetCopyAndNoCopy(t *testing.T) {
    s := New(Options{})
    defer s.Close()

    if created := s.Set("k1", []byte("abc"), 0); !created {
        t.Fatalf("expected created=true on first Set")
    }
    v, ok := s.Get("k1")
    if !ok || string(v) != "abc" {
        t.Fatalf("Get mismatch: ok=%v v=%q", ok, v)
    }
    // изменение копии не должно влиять на хранилище
    v[0] = 'X'
    v2, ok := s.Get("k1")
    if !ok || string(v2) != "abc" {
        t.Fatalf("Get after modify copy mismatch: ok=%v v=%q", ok, v2)
    }

    // Получение без копирования — адрес должен быть стабильным
    vnc1, ok := s.GetNoCopy("k1")
    if !ok || string(vnc1) != "abc" {
        t.Fatalf("GetNoCopy mismatch: ok=%v v=%q", ok, vnc1)
    }
    if len(vnc1) == 0 {
        t.Fatalf("unexpected empty value")
    }
    // адрес массива должен совпадать в пределах одного и того же значения
    vnc2, ok := s.GetNoCopy("k1")
    if !ok {
        t.Fatalf("GetNoCopy second read failed")
    }
    p1 := reflect.ValueOf(&vnc1[0]).Pointer()
    p2 := reflect.ValueOf(&vnc2[0]).Pointer()
    if p1 != p2 {
        t.Fatalf("expected same backing array pointer for GetNoCopy reads")
    }
}

func TestGetDel(t *testing.T) {
    s := New(Options{})
    defer s.Close()

    s.Set("k2", []byte("42"), 0)
    v, ok := s.GetDel("k2")
    if !ok || string(v) != "42" {
        t.Fatalf("GetDel mismatch: ok=%v v=%q", ok, v)
    }
    if _, ok := s.Get("k2"); ok {
        t.Fatalf("expected key to be deleted after GetDel")
    }
}

func TestExpireTTL(t *testing.T) {
    s := New(Options{})
    defer s.Close()

    s.Set("k3", []byte("v"), 50*time.Millisecond)
    if _, ok := s.Get("k3"); !ok {
        t.Fatalf("expected key present before TTL")
    }
    time.Sleep(120 * time.Millisecond)
    if _, ok := s.Get("k3"); ok {
        t.Fatalf("expected key expired")
    }
    if _, ok := s.TTL("k3"); ok {
        t.Fatalf("expected TTL to report missing after expiry")
    }
    stats := s.Metrics()
    if stats.Expired == 0 {
        t.Fatalf("expected Expired > 0, got %v", stats.Expired)
    }
}

func TestExpireUpdateTTL(t *testing.T) {
    s := New(Options{})
    defer s.Close()

    s.Set("k4", []byte("v"), 0)
    if ok := s.Expire("k4", 30*time.Millisecond); !ok {
        t.Fatalf("Expire returned false")
    }
    if d, ok := s.TTL("k4"); !ok || d <= 0 {
        t.Fatalf("TTL should be >0 and ok, got %v %v", d, ok)
    }
    time.Sleep(80 * time.Millisecond)
    if _, ok := s.TTL("k4"); ok {
        t.Fatalf("expected key expired")
    }
}

func TestMetrics(t *testing.T) {
    s := New(Options{})
    defer s.Close()

    s.Set("a", []byte("123"), 0)
    s.Set("b", []byte("5"), 0)
    s.Update("a", func(old []byte) []byte { return append(append([]byte{}, old...), []byte("++")...) })
    s.Get("a")
    s.Get("missing")
    s.GetDel("b")

    st := s.Metrics()
    if st.Keys != 1 {
        t.Fatalf("Keys=1 expected, got %d", st.Keys)
    }
    if st.Sets != 2 || st.Updates != 1 {
        t.Fatalf("Sets=2 Updates=1 expected, got %d %d", st.Sets, st.Updates)
    }
    if st.Gets != 3 || st.Hits != 2 || st.Misses != 1 {
        t.Fatalf("Gets/Hits/Misses mismatch: %d/%d/%d", st.Gets, st.Hits, st.Misses)
    }
    if st.Dels != 1 {
        t.Fatalf("Dels=1 expected, got %d", st.Dels)
    }
    if st.Bytes != uint64(len("123"+"++")) {
        t.Fatalf("Bytes=%d expected, got %d", len("123"+"++"), st.Bytes)
    }
}

