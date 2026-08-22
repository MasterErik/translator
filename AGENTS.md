# AGENTS.md — Translator Project Code Requirements

## Документация

Вся архитектура, диаграммы, интерфейсы, конфигурация, shutdown, Gladia API, LLM — в одном файле:

| Файл | Содержание |
|---|---|
| `docs/ARCHITECTURE.md` | **Единый источник правды:** диаграмма потока, Gladia v2, LLM, Dispatcher, конфигурация, shutdown |
| `docs/TESTING.md` | Интеграционные тесты, SLA & Performance Test Constraints |
| `docs/UI.md` | Четырёхзонный layout, параметры окна, автоскролл |
| `docs/gladia-flow.md` | Детальная схема Gladia WebSocket (двухфазный коннект) |
| `docs/qa-architecture.md` | Архитектура ответов на вопросы (LLM QA): Candidate/Conversation context, F1-F4/Esc, retrieval |
| `.env.example` | Шаблон переменных окружения |
| `config.yaml` | Несекретные параметры |

## Ключевые пакеты

```
internal/
├── pipeline/     # Оркестратор: New(), Run(), shutdown, делегаты → dispatcher
├── dispatcher/   # Маршрутизация STT-событий → UI + LLM (выделен 2026-07-31)
├── capture/      # malgo WASAPI: 80ms фреймы, 48k→16k ресемплинг
├── stt/          # GladiaProvider: двухфазный WS, writePump/readPump
├── translator/   # LLMProvider, OpenAIProvider, IsQuestion, промпты
├── ui/           # GioUI v0.10.1: 4 зоны, автоскролл, WS_EX_NOACTIVATE
├── logger/       # CSV-лог + MP3 аудио, VAD-светофор
└── common/       # Config, STTEvent, UIEvent

## Инструменты анализа кода (CodeGraph MCP)

- ✅ **ВСЕГДА первым** вызывай `codegraph_explore` — он возвращает verbatim source с номерами строк. Эквивалент `read_file` + `search_files` в одном вызове
- `read_file` — только для не-кода: документация (`.md`), конфигурация (`.yaml`, `.env`), скрипты (`.py`, `.sh`)
- `search_files` — только для поиска не-Go файлов
- CodeGraph автосинхронизирует индекс через `serve --mcp` (встроенный file watcher) — ручной `codegraph sync` не нужен

## Язык и стиль

- Документация — на русском
- Код, имена переменных, функций, типов — на английском

## Go-специфичные требования

- **Версия:** Go 1.22+
- **Форматирование:** `gofmt` / `goimports` перед коммитом
- **Линтинг:** `go vet ./...` — ноль предупреждений
- **Структура:** Standard Go project layout (`test/`, `internal/`)
- **Пакеты:** Один пакет — одна ответственность. Нет циклических зависимостей

## Concurrency & Safety (ОБЯЗАТЕЛЬНО)

- **Shared state:** `sync.Mutex` / `sync.RWMutex` или каналы
- **Context:** каждая долгоживущая горутина принимает `context.Context`; соблюдает `ctx.Done()`
- **Graceful shutdown:** SIGINT/SIGTERM → cancel context → drain channels → close resources → flush logs
- **Goroutine leaks:** каждый `go` statement имеет ясный путь выхода
- **Channel discipline:** отправитель закрывает канал; буферизация обоснована; только `select`

## Обработка ошибок

- **Никаких `panic()`** в production-коде
- **Каждая ошибка:** `fmt.Errorf("context: %w", err)`
- **Сетевые вызовы:** `context.WithTimeout`; exponential backoff при реконнектах
- **Логирование:** `log/slog` (structured logging)
- `_ = err` — только с комментарием

## Тестирование

- Каждый пакет имеет `<name>_test.go`
- Покрытие: минимум 90% для `internal/`
- Моки через ручные stub'ы (предпочтительно) или `gomock`
- Table-driven tests для чистых функций
- Интеграционные тесты: `*_integration_test.go` с билд-тегом `//go:build integration`

## Зависимости

- Точные версии в `go.mod`; `go mod tidy` перед коммитом

## Git workflow

- **main** — стабильная ветка; **feat/<name>** — фичи
- Conventional commits: `feat:`, `fix:`, `refactor:`, `test:`, `docs:`, `chore:`
- Pre-commit: `go fmt ./... && go vet ./... && go test ./...`
