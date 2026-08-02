# Архитектура Translator v4 — AI Interview / Meeting Assistant

## Обзор системы

Приложение реального времени — прозрачный overlay поверх рабочего стола Windows. Распознаёт речь собеседника (EN), получает перевод от Gladia (встроенный, EN→RU), сохраняет IT-термины, генерирует подсказки при детекции вопроса.

- **STT: Gladia Solaria-1** — turn-aware стриминг через WebSocket
- **Перевод: Gladia Translation** — встроенный в тот же WebSocket, модель `enhanced`, без отдельного LLM-перевода
- **LLM: только подсказки** — синхронный `GenerateAnswers` (GLM-4.7-Flash)
- **Двухфазный коннект** — POST `/v2/live` → `{id, url}` → WebSocket dial
- **Reconnect** — exponential backoff на тот же URL сессии

### Диаграмма потока данных

```
                        ┌───────────────────────────────────────────┐
                        │              GLADIA CLOUD                 │
                        │  ┌─────────┐  ┌──────────────┐           │
      ┌─────────┐       │  │Solaria-1│  │  Translation │           │
      │ Chrome  │──┐    │  │   STT   │  │   (enhanced) │           │
      │ /Teams  │  │    │  └────┬────┘  └──────┬───────┘           │
      └─────────┘  │    │       │              │                    │
                   ▼    │       ▼              ▼                    │
┌──────────┐  ┌─────────┐  ┌──────────────────────────────────┐    │
│ VB-Cable │  │WASAPI   │  │    WebSocket (wss://)            │    │
│ Loopback │  │Capture  │  │  ◄ BinaryMessage (PCM 80ms)      │    │
│          │  │48k→16k  │  │  ► JSON: transcript + translation │    │
└──────────┘  └────┬────┘  └────────────┬─────────────────────┘    │
                   │                    │                           │
┌──────────┐  ┌────┴────┐              │                           │
│ Микрофон │  │WASAPI   │              │                           │
│          │  │Capture  │              │                           │
│          │  │48k→16k  │              │                           │
└──────────┘  └────┬────┘              │                           │
                   │                    │                           │
        audioStream│                    │                           │
                   │                    │                           │
┌──────────────────┴────────────────────┴───────────────────────────┘
│                        PIPELINE (main.go)                          │
│                                                                    │
│  ┌──────────┐    ┌──────────────┐    ┌──────────────┐             │
│  │ runSTT   │───▶│  DISPATCHER  │───▶│    runUI     │             │
│  │ route    │    │  (отдельный  │    │  Gio overlay │             │
│  │ events   │    │   модуль)    │    │  4 зоны      │             │
│  └──────────┘    └──────┬───────┘    └──────────────┘             │
│                         │                                          │
│                   ┌─────┴──────┐                                   │
│                   │ IsQuestion?│                                   │
│                   └─────┬──────┘                                   │
│                    да   │                                          │
│                   ┌─────┴──────┐                                   │
│                   │ AnswerQueue│                                   │
│                   │  (chan,    │                                   │
│                   │   buf 16)  │                                   │
│                   └─────┬──────┘                                   │
│                         │ FIFO, 1 consumer                         │
│                   ┌─────┴──────┐                                   │
│                   │answerWorker│                                   │
│                   │ дедупликация│                                  │
│                   └─────┬──────┘                                   │
│                         │ последовательно                          │
│                   ┌─────┴──────┐                                   │
│                   │   LLM      │                                   │
│                   │ GLM-4.7    │                                   │
│                   │ Flash      │                                   │
│                   │ синхронный │                                   │
│                   └────────────┘                                   │
└────────────────────────────────────────────────────────────────────┘
```

### Поток событий

```
Audio (80ms PCM)
    │
    ▼
Gladia WS ──► transcript (is_final=false) ──► UI Interim (зона 1)
    │
    ├───────► transcript (is_final=true)  ──► UI TranscriptionHistory (зона 3, оригинал)
    │                                            │
    │                                     IsQuestion?
    │                                      ├─ да → AnswerQueue → answerWorker → LLM → UI Answer (зона 4)
    │                                      └─ нет → ничего
    │
    └───────► translation ────────────────────► UI TranslationHistory (зона 2, перевод)

Зоны 2 и 3 — независимые потоки. History (оригинал) приходит из EndOfTurn, Translation — из translation-событий.
lastOriginal сохраняется только для CSV-логирования.
```

---

## Конфигурация

**Приоритет:** `.env` > `config.yaml` (`.env` переопределяет ВСЁ).

### `.env` — секреты и переопределения

```ini
# API-ключи
GLADIA_API_KEY=xxx                # Gladia STT + Translation (обязательно)
LLM_API_KEY=yyy                   # LLM для подсказок (Z.AI/OpenAI)
LLM_BASE_URL=https://api.z.ai/api/paas/v4/

# LLM
LLM_MAX_TOKENS=1024               # макс. выходных токенов (0 = безлимит)

# UI Overlay
OVERLAY_WIDTH=800                 # ширина окна
OVERLAY_HEIGHT=650                # высота окна
OVERLAY_MAX_LINES=10              # макс. строк в истории перевода

# Аудио
LOOPBACK_DEVICE=CABLE Input (VB-Audio Virtual Cable)
# LOOPBACK_DEVICE ищет сначала среди Loopback, затем среди Capture —
# можно указать микрофон (например, "Microphone (C922 Pro Stream Webcam)")
MIC_DEVICE=Microphone (Realtek)
SAVE_AUDIO=false

# Язык
TARGET_LANG=ru
```

Полный шаблон: `.env.example` в корне проекта.

### `config.yaml` — несекретные параметры

```yaml
# Логи
log_dir: "./logs"

# CV контекст для генерации подсказок
cv_context: |
```

**CVContext** передаётся в LLM как system message (системный промпт):
`Pipeline.Config.CVContext` → `engine.SetCVContext()` → `GenerateAnswers(ctx, question, cvContext)` → system message.

Полный системный промпт настраивается в `config.yaml` в поле `cv_context`. При отсутствии используется дефолтный `SystemPromptAnswerGen`.

---

## Gladia Live API v2

Двухфазный коннект: `POST /v2/live` → `{id, url}` → WebSocket dial. PCM-фреймы 80ms (2560 байт, 16kHz mono). События: `transcript` (interim/final) + `translation` (enhanced, EN→RU). Связка через `lastOriginal` — перевод приходит после финального транскрипта. Reconnect: exponential backoff на тот же URL сессии.

**Подробнее:** `docs/gladia-flow.md`

## LLM

OpenAI-совместимый API. Синхронный `GenerateAnswers` (не стриминг). Промпт: 1 подсказка в формате `EN: <...> | RU: <...>`. Контекст: `CVContext` из `config.yaml`. Детекция вопроса: `IsQuestion()` — `?` или вопросительные слова в начале.

---

## Архитектура модулей

### Пакеты

```
internal/
├── capture/         # malgo WASAPI захват + ресемплер (48k→16k mono)
├── stt/             # GladiaProvider: двухфазный WS, writePump, readPump
├── dispatcher/      # Маршрутизация событий, детекция вопросов, генерация подсказок
├── translator/      # LLM-провайдеры, TranslationEngine, промпты, IsQuestion
├── ui/              # GioUI overlay: 4 зоны, автоскролл, HWND-стили
├── logger/          # Запись сессии: CSV-лог, аудио MP3, VAD-светофор
└── common/          # Config, STTEvent, UIEvent, общие типы
```

### Многопоточная архитектура (10 горутин)

```
┌──────────────────────────────────────────────────────────────────────┐
│                        MAIN GOROUTINE                                │
│  sigCtx → runCtx → запуск горутин → select {сигнал, закрытие окна}  │
└──────────────────────────────────────────────────────────────────────┘
        │          │           │            │            │
        ▼          ▼           ▼            ▼            ▼
│┌───────────┐ ┌─────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐
││ CAPTURE   │ │ CAPTURE  │ │ GLADIA   │ │DISPATCHER│ │ UI       │
││ Loopback  │ │ Mic      │ │ writePump│ │          │ │ GioUI    │
││ 80ms PCM  │ │ 80ms PCM │ │ readPump │ │ select { │ │ event    │
││ → audio-  │ │ → fan-out│ │ (2 гор.) │ │  interim │ │ loop     │
││   Stream  │ │   STT +  │ │          │ │  → UI    │ │          │
││           │ │   logger │ │          │ │  final   │ │          │
││           │ │          │ │          │ │  → UI    │ │          │
││           │ │          │ │          │ │  transl. │ │          │
││           │ │          │ │          │ │  → UI    │ │          │
││           │ │          │ │          │ │  ? → ans │ │          │
││           │ │          │ │          │ │    │     │ │          │
││           │ │          │ │          │ │ ┌──┴───┐ │ │          │
││           │ │          │ │          │ │ │answer│ │ │          │
││           │ │          │ │          │ │ │Worker│ │ │          │
││           │ │          │ │          │ │ │1 гор.│ │ │          │
││           │ │          │ │          │ │ └──┬───┘ │ │          │
││           │ │          │ │          │ │    │ LLM │ │          │
│└───────────┘ └─────────┘ └────┬─────┘ └────┬─────┘ └──────────┘
│                               │            │
│                        textStream (chan STTEvent, buf 64)
│                               │            │
│                               │    answerCh (chan string, buf 16)
```

### Список горутин

| # | Горутина | Пакет | Завершение |
|---|----------|-------|------------|
| 1 | `main` | `main.go` | sigCtx.Done() или закрытие окна |
| 2 | `capture·loopback` | `internal/capture` | runCtx.Done() |
| 3 | `capture·mic` | `internal/capture` | runCtx.Done() |
| 4 | `capture·mic·logger` | `internal/pipeline` | runCtx.Done() |
| 5 | `stt·gladia·writePump` | `internal/stt` | runCtx.Done() / conn close |
| 6 | `stt·gladia·readPump` | `internal/stt` | runCtx.Done() / conn close |
| 7 | `route·stt` (runSTT) | `internal/pipeline` | runCtx.Done() |
| 8 | `dispatcher` | `internal/dispatcher` | runCtx.Done() / textStream close |
| 9 | `dispatcher·answerWorker` | `internal/dispatcher` | answerCh close / ctx.Done() + drain |
| 10 | `ui·gioui` | `internal/ui` | DestroyEvent |

### Каналы

| Канал | Тип | Буфер | Путь |
|---|---|---|---|
| `audioCh` (GladiaProvider) | `chan []byte` | 64 | runCapture → writePump |
| `textCh` (GladiaProvider) | `chan STTEvent` | 32 | readPump → runSTT |
| `textStream` (Pipeline) | `chan STTEvent` | 64 | runSTT → dispatcher |
| `answerCh` (Dispatcher) | `chan string` | 16 | route → answerWorker |
| `dispatchDone` | `chan struct{}` | 1 | dispatcher → shutdown |
| `logCh` (mic fan-out) | `chan []byte` | 32 | mic capture → logger |

### Dispatcher (`internal/dispatcher/`)
Главный организатор логики работы приложения и каналов 

**Логика маршрутизации:** раздельные потоки — без `historySeen` и связки через `lastOriginal`.

```
event.ChannelID == "translation"
    → UI TranslationHistory (зона 2, перевод)

event.Event == EventUpdate
    → UI Interim (зона 1)

event.Event == EventEndOfTurn && ChannelID != "translation"
    → UI TranscriptionHistory (зона 3, оригинал)
    → IsQuestion(event.Text)?
         да → enqueueQuestion(question)
         нет → ничего
```

Зоны 2 и 3 питаются из разных типов сообщений: Translation и History.

**Очередь подсказок (answerQueue):**

- Буферизованный канал `answerCh` (16) и одна горутина `answerWorker`. Зачем:
- Предотвращает спам LLM-запросов → **rate-limit 429** больше не возникает
- Последовательные запросы (FIFO) — сервер не перегружается
- Неблокирующая отправка: если очередь переполнена — дроп с debug-логом
- Graceful shutdown: worker дренирует оставшиеся вопросы перед выходом

При `AnswerQueueSize = -1` в Config — возврат к старому поведению (горутина на каждый вопрос).

### Логирование

Operational-логи (запуск/остановка компонентов, ошибки, отладка) пишутся в **CSV сессии** через `sessLog.LogDebug()`, а не в `slog`. `slog` используется только для фатальных ошибок в `main.go` (stderr) и при создании/закрытии логгера. В Windows GUI-режиме (`-H windowsgui`) консольный вывод отключён (`io.Discard`).

Формат CSV: `timestamp, channel, event, text, translation, answers`. Debug-записи имеют `event=DEBUG`. Все компоненты (Pipeline, Dispatcher, GladiaProvider, Overlay) получают `sessLog` через конструктор и пишут в него.

### Rate-limited buffer monitoring (Dispatcher)

При дропе фрейма — запись в `sessLog.LogDebug()` не чаще 1 раза в 10 сек. Счётчик дропов для отладки.

### Ключевые интерфейсы

```go
type STTProvider interface {
    Start(ctx context.Context) error
    Stop() error
    AudioStream() chan<- []byte       // 16kHz mono PCM
    TextStream() <-chan STTEvent
}

type LLMProvider interface {
    GenerateAnswers(ctx context.Context, question string, cvContext string) ([]string, error)
}

type SessionLogger interface {
    LogText(event STTEvent) error
    LogTranslation(event STTEvent, translation string, answers []string) error
    SaveAudioChunk(channelID string, pcm []byte) error
    Close() error
}
```

---

## Graceful Shutdown

Два сценария завершения: **SIGINT/SIGTERM** и **закрытие UI-окна**.

### Контекст с двойным уровнем

```
sigCtx (signal.NotifyContext: SIGINT/SIGTERM)
  └─ runCtx (context.WithCancel) ← runCancel() при закрытии окна
       ├─ runCapture  → ctx.Done() → остановка устройств
       ├─ runSTT      → ctx.Done() → close(textStream)
       ├─ dispatcher  → ctx.Done() → answerWorker.drainQueue() → answerDone → send(dispatchDone)
       └─ runUI       → DestroyEvent → close(shutdown) → overlayDone
```

### Порядок shutdown (одинаков для обоих сценариев)

1. SIGINT/SIGTERM **или** закрытие окна → `runCancel()` отменяет `runCtx`
2. `runSTT` получает `ctx.Done()` → `defer close(p.textStream)` → dispatcher завершается
3. Dispatcher: `ctx.Done()` → `answerWorker` дренирует очередь (`drainQueue`) → `answerDone`
4. Gladia.Stop() → WebSocket close
5. dispatchDone → все стриминг-горутины завершены
6. UI: WaitShutdown() (уже закрыт при сценарии с окном)
7. Logger: Close() → flush CSV
8. os.Exit(0)

---

## Стек технологий

| Слой | Библиотека | Назначение |
|---|---|---|
| Audio Capture | `malgo` (CGo) | WASAPI Loopback + Mic, 48k→16k ресемплинг |
| STT + Перевод | Gladia Solaria-1 `/v2/live` | Turn-aware streaming + встроенный перевод `enhanced` |
| WebSocket | `gorilla/websocket` | Gladia WS клиент + exponential backoff reconnect |
| LLM | `go-openai` → GLM-4.7-Flash | Синхронная генерация подсказок |
| UI | `gioui.org` v0.10.1 | Прозрачный четырёхзонный overlay |
| Win32 | `golang.org/x/sys/windows` | WS_EX_NOACTIVATE |
| Config | `joho/godotenv` + `gopkg.in/yaml.v3` | .env + config.yaml |
