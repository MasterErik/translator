# AGENTS.md — Translator Project Code Requirements

## Документация (on-demand, не в постоянный context)

| Файл | Содержание |
|---|---|
| `docs/ARCHITECTURE.md` | Единый источник правды: поток, Gladia v2, LLM, Dispatcher, shutdown |
| `docs/TESTING.md` | Интеграционные тесты, SLA |
| `docs/UI.md` | Layout, окно, автоскролл |
| `docs/gladia-flow.md` | Gladia WebSocket (двухфазный коннект) |
| `docs/qa-architecture.md` | LLM QA: Candidate/Conversation, F1-F4/Esc |
| `docs/candidate-context-architecture.md` | Candidate context: fact-level retrieval (пакет `internal/context`) |
| `docs/candidate-context-format.md` | Формат candidate context: manifest.json, index.json, markers |
| `.env.example` / `config.yaml` | Шаблон окружения / несекретные параметры |

## Ключевые пакеты

```
internal/
├── pipeline/     # оркестратор New/Run/shutdown, делегаты → dispatcher
├── dispatcher/   # STT-события → UI + LLM
├── capture/      # malgo WASAPI: 80ms фреймы, 48k→16k ресемплинг
├── stt/          # GladiaProvider: двухфазный WS, writePump/readPump
├── translator/   # LLMProvider, OpenAIProvider, IsQuestion, промпты
├── ui/           # GioUI v0.10.1: 4 зоны, WS_EX_NOACTIVATE
├── logger/       # CSV-лог + MP3 аудио, VAD-светофор
├── context/      # candidate context: fact-level lexical retrieval (index/score/budget)
└── common/       # Config, STTEvent, UIEvent
```

## Язык и стиль

- Документация — на русском; код, имена переменных, функций, типов — на английском.

## Go-требования

- Go 1.22+; `gofmt` / `goimports` перед коммитом; `go vet ./...` — ноль предупреждений.
- Standard project layout (`test/`, `internal/`); один пакет — одна ответственность; нет циклических зависимостей.

## Concurrency & Safety (ОБЯЗАТЕЛЬНО)

- Shared state: `sync.Mutex` / `sync.RWMutex` или каналы.
- Каждая долгоживущая горутина принимает `context.Context`, соблюдает `ctx.Done()`.
- Graceful shutdown: SIGINT/SIGTERM → cancel context → drain channels → close resources → flush logs.
- Goroutine leaks: каждый `go` statement имеет ясный путь выхода.
- Channel discipline: отправитель закрывает канал; буферизация обоснована; только `select`.

## Обработка ошибок

- Никаких `panic()` в production-коде.
- Каждая ошибка: `fmt.Errorf("context: %w", err)`.
- Сетевые вызовы: `context.WithTimeout`; exponential backoff при реконнектах.
- Логирование: `log/slog` (structured logging); `_ = err` — только с комментарием.

## Тестирование

- Каждый пакет имеет `<name>_test.go`; покрытие минимум 90% для `internal/`.
- Моки через ручные stub'ы или gomock; table-driven tests для чистых функций.
- Интеграционные тесты: `*_integration_test.go` с билд-тегом `//go:build integration`.

## Зависимости и Git

- Точные версии в `go.mod`; `go mod tidy` перед коммитом.
- main — стабильная ветка; feat/<name> — фичи; Conventional commits.
- Pre-commit: `go fmt ./... && go vet ./... && go test ./...`.
