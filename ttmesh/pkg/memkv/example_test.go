package memkv_test

import (
    "fmt"
    "time"

    "ttmesh/pkg/memkv"
)

func Example_basic() {
    s := memkv.New(memkv.Options{})
    defer s.Close()

    s.Set("user:1", []byte("alice"), 500*time.Millisecond)

    // Обычное чтение (безопасное, с копированием)
    v, _ := s.Get("user:1")
    fmt.Println(string(v))

    // Нулевое копирование
    vnc, _ := s.GetNoCopy("user:1")
    fmt.Println(string(vnc))

    // Атомарно получить и удалить
    v2, _ := s.GetAndDelete("user:1")
    fmt.Println(string(v2))

    // Метрики
    st := s.Metrics()
    fmt.Println(st.Keys > 0 || st.Dels > 0)

    // Output:
    // alice
    // alice
    // alice
    // true
}

