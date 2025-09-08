// Package memkv предоставляет высокопроизводительное потокобезопасное in-memory
// хранилище с базовым функционалом Redis: Set/Get, GETDEL, TTL/Expire,
// обновление значений, метрики и возможность получать значение без копирования.
//
// Основные свойства:
//   - Шардированная карта с RW-мьютексами (по умолчанию 256 шардов)
//   - TTL и фоновая горутина, удаляющая просроченные ключи
//   - Возврат значения с копированием (безопасно) или без копирования (нулевые аллокации)
//   - Быстрые метрики на атомиках без влияния на производительность
//   - Минимум аллокаций в горячем пути
//   - Опциональный лимит по общему объёму данных (Options.MaxBytes)
package memkv

// Запуск тестов и бенчмарков
//
// Обычные тесты и бенчмарки:
//   go test -v ./pkg/memkv
//   go test -bench=BenchmarkSetGet_Parallel -benchmem ./pkg/memkv
//
// Тяжёлые перф‑тесты (миллионы ключей/гигабайты) включаются build‑тегом memkv_huge
// и настраиваются переменными окружения. Примеры запуска:
//
// Bash (Linux/macOS/Git Bash/WSL):
//   MEMKV_ITEMS=2000000 MEMKV_VALUE_BYTES=1024 \
//     go test -tags memkv_huge -run TestHugeInsert -timeout 0 -v ./pkg/memkv
//   MEMKV_GB=3 MEMKV_VALUE_BYTES=1024 \
//     go test -tags memkv_huge -run TestHugeInsert -timeout 0 -v ./pkg/memkv
//   MEMKV_GB=2 MEMKV_VALUE_BYTES=1024 \
//     go test -tags memkv_huge -run TestMaxBytesHuge -timeout 0 -v ./pkg/memkv
//
// PowerShell (Windows):
//   $env:MEMKV_ITEMS=2000000; $env:MEMKV_VALUE_BYTES=1024; `
//     go test -tags memkv_huge -run TestHugeInsert -timeout 0 -v ./pkg/memkv
//   $env:MEMKV_GB=3; $env:MEMKV_VALUE_BYTES=1024; `
//     go test -tags memkv_huge -run TestHugeInsert -timeout 0 -v ./pkg/memkv
//   $env:MEMKV_GB=2; $env:MEMKV_VALUE_BYTES=1024; `
//     go test -tags memkv_huge -run TestMaxBytesHuge -timeout 0 -v ./pkg/memkv
//
// cmd.exe (Windows):
//   cmd /c "set MEMKV_ITEMS=2000000 && set MEMKV_VALUE_BYTES=1024 && ^
//            go test -tags memkv_huge -run TestHugeInsert -timeout 0 -v .\\pkg\\memkv"
//   cmd /c "set MEMKV_GB=3 && set MEMKV_VALUE_BYTES=1024 && ^
//            go test -tags memkv_huge -run TestHugeInsert -timeout 0 -v .\\pkg\\memkv"
//   cmd /c "set MEMKV_GB=2 && set MEMKV_VALUE_BYTES=1024 && ^
//            go test -tags memkv_huge -run TestMaxBytesHuge -timeout 0 -v .\\pkg\\memkv"
//
// Параметры окружения для heavy‑тестов:
//   MEMKV_ITEMS        — число записей (если задано, перекрывает MEMKV_GB)
//   MEMKV_VALUE_BYTES  — размер значения (в байтах)
//   MEMKV_GB           — целевой общий объём данных (в гигабайтах)
//
// Внимание: heavy‑тесты потребляют много памяти. Начинайте с меньших значений,
// чтобы избежать OOM, и при необходимости увеличивайте постепенно. Для отключения
// таймаута используется флаг `-timeout 0`.
