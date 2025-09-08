package memkv

import (
    "bytes"
    "testing"
)

func TestMaxBytes_NewKeyRejectsWhenExceed(t *testing.T) {
    s := New(Options{MaxBytes: 64})
    defer s.Close()

    if !s.Set("a", bytes.Repeat([]byte{'x'}, 50), 0) {
        t.Fatalf("expected initial Set to succeed")
    }
    // Эта запись превысит лимит
    if s.Set("b", bytes.Repeat([]byte{'y'}, 20), 0) {
        t.Fatalf("expected Set to be rejected when exceeding MaxBytes")
    }
    if s.Exists("b") {
        t.Fatalf("key 'b' must not exist after rejected Set")
    }
    st := s.Metrics()
    if st.Bytes != 50 || st.Keys != 1 {
        t.Fatalf("metrics mismatch: Bytes=%d Keys=%d", st.Bytes, st.Keys)
    }
}

func TestMaxBytes_ReplaceRejectsWhenExceed(t *testing.T) {
    s := New(Options{MaxBytes: 50})
    defer s.Close()

    if !s.Set("a", bytes.Repeat([]byte{'x'}, 40), 0) {
        t.Fatalf("expected initial Set to succeed")
    }
    // Замену на 60 байт нужно отклонить (delta +20, станет 60>50)
    if s.Set("a", bytes.Repeat([]byte{'z'}, 60), 0) {
        t.Fatalf("expected replace to be rejected when exceeding MaxBytes")
    }
    v, ok := s.Get("a")
    if !ok || len(v) != 40 {
        t.Fatalf("value must remain 40 bytes after rejected replace, got %v %d", ok, len(v))
    }
    st := s.Metrics()
    if st.Bytes != 40 || st.Keys != 1 {
        t.Fatalf("metrics mismatch: Bytes=%d Keys=%d", st.Bytes, st.Keys)
    }
}

func TestMaxBytes_UpdateRejectsWhenExceed(t *testing.T) {
    s := New(Options{MaxBytes: 64})
    defer s.Close()

    s.Set("a", bytes.Repeat([]byte("a"), 40), 0)
    // Увеличение до 70 (+30) должно быть отклонено
    ok := s.Update("a", func(old []byte) []byte { return bytes.Repeat([]byte("b"), 70) })
    if ok {
        t.Fatalf("expected Update to be rejected when exceeding MaxBytes")
    }
    v, _ := s.Get("a")
    if len(v) != 40 {
        t.Fatalf("expected value length 40 after rejected Update, got %d", len(v))
    }
}

func TestBytesAccountingOnDelete(t *testing.T) {
    s := New(Options{MaxBytes: 0})
    defer s.Close()
    s.Set("a", bytes.Repeat([]byte{'x'}, 40), 0)
    s.Set("b", bytes.Repeat([]byte{'y'}, 12), 0)
    s.Delete("a")
    st := s.Metrics()
    if st.Bytes != 12 || st.Keys != 1 {
        t.Fatalf("after delete mismatch: Bytes=%d Keys=%d", st.Bytes, st.Keys)
    }
}

