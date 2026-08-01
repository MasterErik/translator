# План: Tail Latency & Outlier Detection Test — ВЫПОЛНЕН

> **Цель:** Многопоточный нагрузочный тест распределения задержек STT + LLM с детекцией выбросов.

**Ключевые решения:**
- Эмулятор Gladia (не реальный API) — детерминированно, бесплатно, контролируемо
- LLM — строго последовательные вызовы (единая очередь, один consumer)
- Теоретический расчёт задержек в горутинах в `latency_calc.go`

---

## Шаг 1 — `generate_test_wav.py` ✅

Добавлена генерация `test_speech.json` с известным текстом, переводом и `is_question: true`.

## Шаг 2 — `internal/stt/emulator.go` ✅

`GladiaEmulator` реализует `stt.STTProvider`:
- Настраиваемые задержки с нормальным распределением (± jitter)
- Connect: 300ms±100ms, Interim: 500ms±200ms, Final: 1000ms±300ms, Translation: 150ms±100ms
- `writePump` / `readPump` — эмуляция реального поведения Gladia
- Защита от гонок: textCh закрывается после завершения sendEvents
- `Stop()` идемпотентен

## Шаг 2b — `internal/stt/emulator_test.go` ✅

11 юнит-тестов: Start/Stop, двойной старт, отмена контекста, идемпотентность, последовательность событий, закрытие каналов, jitter, интерфейс, конкурентная безопасность.

Покрытие emulator: 54.4% (весь пакет STT), Start/Stop/AudioStream/TextStream/jitter = 100%.

## Шаг 3 — `test/tail_latency/` ✅

| Файл | Назначение |
|---|---|
| `main.go` | Точка входа, флаги, setup LLM, запуск воркеров |
| `worker.go` | Логика воркера + `llmWorker` (последовательный consumer) |
| `metrics.go` | `MetricsCollector` — потокобезопасный сбор, p50/p95/p99 |
| `outlier.go` | IQR (1.5×) + Z-score (3σ) детекция выбросов |
| `report.go` | stdout таблица + CSV + JSON вывод |
| `latency_calc.go` | Теоретический расчёт задержек горутин |

## Шаг 4 — Валидация ✅

```bash
go vet ./internal/stt/... ./test/tail_latency/...  → OK
go test -v -run TestGladiaEmulator ./internal/stt/...  → 11/11 PASS
go run ./test/tail_latency --workers=4 --iterations=5  → OK, 100 семплов
```

Результат прогона:
```
Wall-clock: 12.5s
Stage          p50   p95   p99   min   max   outliers
connect        309   362   368   200   370   0
first_interim  799   921   954   684   962   2
final          2315  2422  2465  2179  2475  0
translation    2478  2546  2586  2311  2596  0
total          2478  2546  2586  2311  2596  0
Critical outliers: 0, Warnings: 0
```

## Созданные файлы
- `internal/stt/emulator.go`
- `internal/stt/emulator_test.go`
- `test/tail_latency/main.go`
- `test/tail_latency/worker.go`
- `test/tail_latency/metrics.go`
- `test/tail_latency/outlier.go`
- `test/tail_latency/report.go`
- `test/tail_latency/latency_calc.go`

## Изменённые файлы
- `generate_test_wav.py`
