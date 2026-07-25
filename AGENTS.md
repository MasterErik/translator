# AGENTS.md — Translator Project Code Requirements

## Язык и стиль

- Код (имена переменных, функций, типов) — на английском (idiomatic Go: camelCase/PascalCase)
- Комментарии к публичным API — на русском, в формате godoc
- Сообщения коммитов — на английском, conventional commits (`feat:`, `fix:`, `refactor:`)
- Документация (README, ARCHITECTURE, план) — на русском
- **Диалог и рассуждения с агентом — на русском. Все ответы, пояснения, планы, отчёты — русский язык.**

## Go-специфичные требования

- **Версия:** Go 1.22+ (модуль объявлен с `go 1.22`)
- **Форматирование:** `gofmt` / `goimports` — перед каждым коммитом
- **Линтинг:** `go vet ./...` — ноль предупреждений; `staticcheck` приветствуется
- **Структура:** Standard Go project layout (`test/`, `internal/`)
- **Пакеты:** Один пакет — одна ответственность. Нет циклических зависимостей

## Concurrency & Safety (ОБЯЗАТЕЛЬНО)

- **Race detector:** `go test -race ./...` проходит без data race
- **Shared state:** ВСЕГДА защищён `sync.Mutex` / `sync.RWMutex`, либо используется идиоматичная синхронизация через каналы
- **Context:** Каждая долгоживущая горутина принимает `context.Context`; соблюдает `ctx.Done()`
- **Graceful shutdown:** Обработка `os.Signal` (SIGINT, SIGTERM) → cancel context → drain channels → close resources → flush logs
- **Goroutine leaks:** Каждый `go` statement имеет ясный путь выхода; проверка через `runtime/pprof` или `goleak`
- **Channel discipline:** Отправитель закрывает канал; буферизация обоснована; нет busy-wait'ов — только `select`

## Interface-Driven Design

- **STTProvider:** `Start(ctx) error`, `Stop() error`, `AudioStream() chan<- []byte`, `TextStream() <-chan STTEvent`
- **LLMProvider:** `GenerateAnswers(ctx, question, cvCtx) ([]string, error)`, `GenerateAnswersStream(ctx, question, cvCtx) (<-chan string, error)`
- **SessionLogger:** `LogText(event) error`, `SaveAudioChunk(channelID, pcm) error`, `Close() error`
- **Все модули зависят от интерфейсов, не от конкретных реализаций**
- Подмена провайдера (Gladia → альтернативный STT) — ТОЛЬКО в `main.go`/`pipeline.New()`, без изменений в `internal/translator`, `internal/ui`, `internal/logger`

## Обработка ошибок

- **Никаких `panic()`** в production-коде (только в `main()` при фатальных ошибках инициализации)
- **Каждая ошибка:** обёрнута через `fmt.Errorf("context: %w", err)` с сохранением цепочки
- **Сетевые вызовы:** таймауты через `context.WithTimeout`; exponential backoff при реконнектах
- **Логирование ошибок:** через стандартный `log/slog` (structured logging)
- **Не игнорировать ошибки:** `_ = err` допустимо только с комментарием почему

## Тестирование

- **Каждый пакет:** имеет `<name>_test.go`
- **Покрытие:** минимум 70% для `internal/` пакетов
- **Интерфейсы:** mock'и через `gomock` или ручные stub'ы для тестирования без real I/O
- **Concurrency-тесты:** с флагом `-race`; не менее одного теста на многопоточный сценарий
- **Table-driven tests:** для чистых функций (resampler, prompts, translation)
- **Интеграционные тесты:** опциональны, вынесены в `*_integration_test.go` с build tag `//go:build integration`

## Зависимости (go.mod)

- **Версионирование:** точные версии; `go mod tidy` перед коммитом

## Конфигурация

- **API-ключи:** через переменные окружения (`GLADIA_API_KEY`, `OPENAI_API_KEY`) — НИКОГДА не в коде, не в коммитах
- **config.yaml:** для несекретных параметров (выбор модели, язык, пути логов, UI-настройки)
- **Значения по умолчанию:** fallback на разумные дефолты если переменная не задана

## Performance (опционально, цель)

- Аудио-буферы: 20ms фреймы (320 сэмплов при 16kHz)
- STT-задержка: < 500ms от речи до текста (с Gladia)
- UI: 30+ FPS; без блокировок event loop'а
- Перевод: < 2s от получения final-транскрипта (с учётом сети)

## Git workflow

- **main** — стабильная ветка
- **feat/<name>** — фичи
- **Conventional commits:** `feat:`, `fix:`, `refactor:`, `test:`, `docs:`, `chore:`
- **Pre-commit:** `go fmt ./... && go vet ./... && go test -race ./...`
