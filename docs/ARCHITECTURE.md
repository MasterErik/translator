# Архитектура Translator v4 — AI Interview / Meeting Assistant

## Обзор системы

Приложение реального времени — прозрачный overlay поверх рабочего стола Windows. Распознаёт речь собеседника (EN), получает перевод от Gladia (встроенный, EN→RU), сохраняет IT-термины, генерирует подсказки при детекции вопроса.

- **STT: Gladia Solaria-1** — turn-aware стриминг через WebSocket
- **Перевод: Gladia Translation** — встроенный в тот же WebSocket, модель `enhanced`, без отдельного LLM-перевода
- **LLM: только подсказки** — синхронный `GenerateAnswers` (GLM-4.7-Flash)
- **Двухфазный коннект** — POST `/v2/live` → `{id, url}` → WebSocket dial
- **Reconnect** — supervisor внутри провайдера: exponential backoff, 5 попыток, далее EventError

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
│(собеседник)│ │48k→16k  │  │  ► JSON: transcript + translation │    │
└──────────┘  └────┬────┘  └────────────┬─────────────────────┘    │
                   │ audioStream        │                           │
                   │ (только loopback)  │                           │
                   │                    │                           │
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

Микрофон (ваш голос) захватывается **только при `SAVE_AUDIO=true`** (`CaptureConfig.EnableMic`) и идёт напрямую в логгер сессии (запись MP3). В STT/перевод микрофон не попадает — `audioStream` получает только loopback (собеседник).

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

# Языки (ISO 639-1; пусто = дефолт)
SOURCE_LANG=en
TARGET_LANG=ru
```

Полный шаблон: `.env.example` в корне проекта.

### `config.yaml` — несекретные параметры

```yaml
# Логи
log_dir: "./logs"

# База CV (Candidate Context) — путь к текстовому файлу с фактами кандидата
candidate_context_file: "candidate_context.md"

# Conversation context — ограничение истории интервью
conversation:
  recent_turns: 6
  max_context_tokens: 4000
```

**Candidate Context** — постоянные факты кандидата (база CV), читается из файла `candidate_context_file` и передаётся в LLM как system message (системный промпт). **Conversation Context** — история текущего интервью (последние `recent_turns` turns, обрезается по `max_context_tokens`), передаётся в user prompt.

Два контекста разделены: факты кандидата (база CV) не смешиваются с историей разговора. Информация о компании/вакансии (сказанное интервьюером) остаётся в conversation history и не становится фактами кандидата.

**Fact-level retrieval (режим с 2026-08):** вместо загрузки `candidate_context_file` целиком можно задать `candidate_context.dir` — тогда приложение строит локальный lexical index и на каждый вопрос отбирает точечно релевантные факты (profile + топ фактов по score). Полная база в prompt не попадает. Архитектура блока — `docs/candidate-context-architecture.md`, формат данных — `docs/candidate-context-format.md`.

---

## Gladia Live API v2

Двухфазный коннект: `POST /v2/live` → `{id, url}` → WebSocket dial. PCM-фреймы 80ms (2560 байт, 16kHz mono). События: `transcript` (interim/final) + `translation` (enhanced, EN→RU). Связка через `lastOriginal` — перевод приходит после финального транскрипта. Reconnect: supervisor-горутина в провайдере, повторный двухфазный коннект с exponential backoff (5 попыток → EventError).

**Подробнее:** `docs/gladia-flow.md`

## LLM

OpenAI-совместимый API (Groq free tier, `openai/gpt-oss-20b`). Синхронный `GenerateAnswers(ctx, AnswerRequest)`. Промпт: 1 подсказка в формате `<SRC>: <...> | <TGT>: <...>` — теги из пары языков (`SOURCE_LANG`/`TARGET_LANG`, дефолт `EN`/`RU`). Правила ответа (system-промпт) вынесены из кода в текстовый файл `SYSTEM_PROMPT_FILE` (по умолчанию `candidate_context/system_prompt.md`): он читается на старте `translator.LoadSystemPromptTemplate`, плейсхолдеры `{{SRC_TAG}}`/`{{TGT_TAG}}` подставляются языковой парой; если файла нет — он создаётся из встроенного дефолта (go:embed) и работа продолжается; пустой путь, нечитаемый файл или неизвестный плейсхолдер валят старт (fail-fast). Разбор ответа — `ParseAnswer`. Контекст: Candidate Context (база CV, system) + Conversation Context (история интервью, user). Детекция вопроса: `IsQuestion()` — `?` или вопросительные слова в начале. Управление генерацией: F1–F3 + Esc через dispatcher, F4 — тумблер зоны 4 (полная таблица — docs/UI.md). Ошибка генерации уходит в UI как `UIMessage{Type: Error}`.

**Подробнее:** `docs/qa-architecture.md`

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
├── context/         # Candidate context: fact-level lexical retrieval (index/score/budget)
└── common/          # Config, STTEvent, общие типы
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
││ → audio-  │ │ → logger │ │ (2 гор.) │ │  interim │ │ loop     │
││   Stream  │ │ (только  │ │          │ │  → UI    │ │          │
││           │ │ при Save-│ │          │ │  final   │ │          │
││           │ │ Audio)   │ │          │ │  → UI    │ │          │
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
| 3 | `capture·mic` | `internal/capture` | runCtx.Done() (только при `SAVE_AUDIO`) |
| 4 | `capture·mic·logger` (routeRawMic) | `internal/pipeline` | runCtx.Done() (только при `SAVE_AUDIO`) |
| 5 | `stt·gladia·supervise` | `internal/stt` | supCtx.Done() / исчерпание попыток реконнекта |
| 6 | `stt·gladia·writePump` | `internal/stt` | ctx.Done() / conn close (connClosed) |
| 7 | `stt·gladia·readPump` | `internal/stt` | ctx.Done() / ошибка чтения (→ supervisor) |
| 8 | `route·stt` (runSTT) | `internal/pipeline` | runCtx.Done() |
| 9 | `dispatcher` | `internal/dispatcher` | runCtx.Done() / textStream close |
| 10 | `dispatcher·answerWorker` | `internal/dispatcher` | answerCh close / ctx.Done() + drain |
| 11 | `ui·gioui` | `internal/ui` | DestroyEvent |

### Каналы

| Канал | Тип | Буфер | Путь |
|---|---|---|---|
| `audioCh` (GladiaProvider) | `chan []byte` | 64 | runCapture (loopback) → writePump |
| `textCh` (GladiaProvider) | `chan STTEvent` | 32 | readPump → runSTT |
| `textStream` (Pipeline) | `chan STTEvent` | 64 | runSTT → dispatcher |
| `answerCh` (Dispatcher) | `chan string` | 16 | route → answerWorker |
| `dispatchDone` | `chan struct{}` | 1 | dispatcher → shutdown |

### Dispatcher (`internal/dispatcher/`)
Главный организатор логики работы приложения и каналов 

**Логика маршрутизации:** раздельные потоки — без `historySeen` и связки через `lastOriginal`.

```
event.ChannelID == "translation"
    → UI TranslationHistory (зона 2, перевод)   [append-only]

event.Event == EventUpdate
    → UI Interim (зона 1)

event.Event == EventEndOfTurn && ChannelID != "translation"
    → UI TranscriptionHistory (зона 4, оригинал)
    → IsQuestion(event.Text)?
         да → enqueueQuestion(question)
         нет → ничего
```

Зона 2 (Translation) и зона 4 (History) питаются из разных типов сообщений: `Translation` и `History`.

**Контракт UI-сообщений (`internal/ui`):**

- `UIMessage.Answers []Answer`, где `Answer{Source, Target}` — пара «ответ в исходном языке / перевод в целевом». Поля нейтральны, т.к. языковая пара настраиваема.
- Формат строки подсказки: `SRC: <…> | TGT: <…>`. Разбор — `translator.ParseAnswer(s, srcTag, tgtTag)`; теги — `strings.ToUpper(SOURCE_LANG/TARGET_LANG)`.
- **Параметризация языков:** `Config.SourceLang/TargetLang` (из `SOURCE_LANG`/`TARGET_LANG`, ISO 639-1, дефолт **en→ru**) прокидывается через dispatcher в `ParseAnswer` и в рендер шаблона system-промпта (`SYSTEM_PROMPT_FILE`, плейсхолдеры `{{SRC_TAG}}`/`{{TGT_TAG}}`). Пустые значения трактуются как дефолт.
- **Тип `Error`:** ошибка движка генерации отправляется как `UIMessage{Type: Error}` (не как строка в `Answers`); рисуется красным в зоне 3 и имеет приоритет над подсказками. `AddMessage(Error)` заменяет предыдущую ошибку.
- **Переводы — без стриминга:** зона 2 — только `append` финальных переводов; ветки `pending`/`streaming` (`MsgStatus`) удалены, dispatcher шлёт только `done`. `Translation` в pipeline не стримится.

**Очередь подсказок (answerQueue):**

- Буферизованный канал `answerCh` (16) и одна горутина `answerWorker`. Зачем:
- Предотвращает спам LLM-запросов → **rate-limit 429** больше не возникает
- Последовательные запросы (FIFO) — сервер не перегружается
- Неблокирующая отправка: если очередь переполнена — дроп с debug-логом
- Graceful shutdown: worker дренирует оставшиеся вопросы перед выходом

**Управление генерацией (механизм):**

- `commandCh` (chan `GenerationCommand`) — команды F1–F3 из глобальных hotkeys (`internal/hotkey/`, Win32 `RegisterHotKey`); F1 (`CommandAnswer`) — обычная генерация, F2–F3 — regeneration на текущий вопрос без нового turn (последняя версия становится текущим ответом).
- `cancelCh` — Esc: отменяет активную генерацию (`activeCancel`) и очищает очередь (`cancelled` + `dropQueue`).
- F4 — тумблер зоны 4 (окно оригиналов): идёт в `overlay.ToggleTranscriptionHistory()` напрямую, **мимо dispatcher**. Зона 4 скрыта при старте (`historyVisible=false`); F4 показывает/прячет.
- Один `answerWorker` обрабатывает и вопросы, и команды **последовательно** (FIFO) — без параллельных LLM-запросов.

### Логирование

Operational-логи (запуск/остановка компонентов, ошибки, отладка) пишутся в **CSV сессии** через `sessLog.LogDebug()`, а не в `slog`. `slog` используется только для фатальных ошибок в `main.go`. В Windows GUI-режиме (`-H windowsgui`) консоль отсутствует, поэтому slog JSON-хендлер пишется в `logs/fatal-<date>.log` (открывается до загрузки конфига; уровень переустанавливается из `cfg.SlogLevel()` после); при невозможности открыть файл — fallback на `io.Discard`. `fatalError` дополнительно записывает сообщение напрямую в этот файл (`writeFatalLog` + `Sync`) перед `os.Exit` и показывает MessageBoxW.

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
    GenerateAnswers(ctx context.Context, req AnswerRequest) ([]string, error)
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

## Debug-режим ручного ввода (`DEBUG_INPUT=1`)

Флаг `.env` `DEBUG_INPUT` (bool, yaml-тег `DEBUG_INPUT`; `.env` в приоритете над
config.yaml) включает режим отладки без транскрибатора: overlay-окно остаётся
кликабельным и принимает фокус (стили `WS_EX_NOACTIVATE`/`WS_EX_TRANSPARENT`
не применяются — `applyWindowStylesWith` в `Overlay.Run`), а ниже зоны 4
рендерится поле ручного ввода.

Путь данных:

```
.env DEBUG_INPUT → common.Config.DebugInput → pipeline.Config.DebugInput
                                                        │
pipeline.New: cfg.OverlayCfg.DebugInput = cfg.DebugInput (проводка в оверлей:
             зона ввода + пропуск стилей окна)
pipeline.New: manualCh = make(chan string, 8) → overlay.SetManualInput(manualCh)
                                                        │
pipeline.runSTT: select { src | manualCh | ctx.Done }
  manual-ветка → common.STTEvent{Event: EventEndOfTurn, ChannelID: "speaker",
                                  Text: TrimSpace(текст), Timestamp: time.Now()}
  → p.routeSTTEvent(ctx, event) — тот же путь, что у Gladia
```

- Живой транскрибатор (Gladia + capture) продолжает работать параллельно;
  введённая строка идёт полным путём STT-события (pipeline → dispatcher → QA → UI).
- Ответ генерируется, только если строка распознана вопросом эвристикой
  `translator.IsQuestion` («?» в строке или англ. вопросное слово в начале);
  при `AutoAnswer=false` вопрос копится меткой `QIdle`, ответ по F1
  (подробности — `docs/UI.md`, раздел debug-режима).
- `manualCh` создаёт pipeline (буфер 8); `close` не нужен — владелец живёт до
  конца, выход по `ctx.Done()`. Ветка select по nil-каналу блокируется вечно
  (эквивалент отсутствия ветки при `DEBUG_INPUT=0`).
- Инвариант: контракты `STTProvider`, `dispatcher.Run`, `OverlayUI` не меняются —
  только опциональный сеттер `Overlay.SetManualInput` и новая ветка select.

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
