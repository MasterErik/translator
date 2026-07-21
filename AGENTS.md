# AGENTS.md — Translator Project Code Requirements

## Язык и стиль

- Весь код, комментарии и документация — на английском
- Имена переменных, функций, типов — idiomatic Go (camelCase/PascalCase)
- Комментарии к публичным API — в формате godoc (`// FunctionName does X.`)
- Сообщения коммитов — на английском, conventional commits (`feat:`, `fix:`, `refactor:`)

## Go-специфичные требования

- **Версия:** Go 1.22+ (модуль объявлен с `go 1.22`)
- **Форматирование:** `gofmt` / `goimports` — перед каждым коммитом
- **Линтинг:** `go vet ./...` — ноль предупреждений; `staticcheck` приветствуется
- **Структура:** Standard Go project layout (`cmd/`, `internal/`)
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
- **LLMProvider:** `Translate(ctx, text, history) (string, error)`, `GenerateAnswers(ctx, question, cvCtx) ([]string, error)`
- **SessionLogger:** `LogText(event) error`, `SaveAudioChunk(channelID, pcm) error`, `Close() error`
- **Все модули зависят от интерфейсов, не от конкретных реализаций**
- Подмена провайдера (Deepgram → Sherpa-onnx) — ТОЛЬКО в `main.go`, без изменений в `internal/translator`, `internal/ui`, `internal/logger`

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

- **Минимум внешних зависимостей:** любая новая — обоснована в PR
- **Cgo:** избегать где возможно. `malgo` — единственное допустимое исключение (звук). Sherpa-onnx идёт как будущая опция
- **Версионирование:** точные версии; `go mod tidy` перед коммитом
- **Лицензионная совместимость:** только MIT, Apache-2.0, BSD

## Конфигурация

- **API-ключи:** через переменные окружения (`DEEPGRAM_API_KEY`, `OPENAI_API_KEY`) — НИКОГДА не в коде, не в коммитах
- **config.yaml:** для несекретных параметров (выбор модели, язык, пути логов, UI-настройки)
- **Значения по умолчанию:** fallback на разумные дефолты если переменная не задана

## Performance (опционально, цель)

- Аудио-буферы: 20ms фреймы (320 сэмплов при 16kHz)
- STT-задержка: < 500ms от речи до текста (с Deepgram)
- UI: 30+ FPS; без блокировок event loop'а
- Перевод: < 2s от получения final-транскрипта (с учётом сети)

## Git workflow

- **main** — стабильная ветка
- **feat/<name>** — фичи
- **Conventional commits:** `feat:`, `fix:`, `refactor:`, `test:`, `docs:`, `chore:`
- **Pre-commit:** `go fmt ./... && go vet ./... && go test -race ./...`
