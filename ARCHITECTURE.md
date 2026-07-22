# Архитектура Translator — AI Interview / Meeting Assistant

## Обзор системы

Приложение реального времени, работающее как прозрачный overlay поверх рабочего стола Windows. Предназначено для помощи на технических собеседованиях/встречах: переводит речь собеседника (EN→RU), сохраняет IT-термины, генерирует подсказки к ответам при детекции вопроса.

## Аудио-архитектура: виртуальный аудиоканал (VB-Cable)

Translator использует **двухканальный захват через виртуальный аудиоканал VB-Cable**, а не простой системный Loopback. Это принципиально важно для разделения звука собеседника и вашего голоса.

### Почему VB-Cable, а не простой Loopback?

WASAPI Loopback по умолчанию захватывает **весь системный звук** — всё, что идёт в динамики (Chrome, системные звуки, уведомления, ваш микрофонный мониторинг). Это создаёт эхо и смешивает ваш голос с голосом собеседника, делая двухканальное разделение невозможным.

**Решение:** VB-Cable создаёт изолированный виртуальный аудиоканал:

```
┌──────────────────────────────────────────────────────┐
│                  Аудио-поток (схема)                  │
├──────────────────────────────────────────────────────┤
│                                                      │
│  Chrome/Teams ──► CABLE Input (Playback)             │
│       │                   │                          │
│       │                   │  VB-Cable внутренний     │
│       │                   │  виртуальный мост        │
│       │                   │                          │
│       │                   ▼                          │
│       │         WASAPI Loopback захват ──────────┐   │
│       │         (с CABLE Input как Playback)      │   │
│       │                                          │   │
│  Микрофон ──► WASAPI Capture ────────────────┐   │   │
│                                              │   │   │
│                      ┌───────────────────────┘   │   │
│                      │                           │   │
│                      ▼                           ▼   │
│              chan "speaker" (PCM)    chan "mic" (PCM) │
│                      │                           │   │
│                      └──────────┬────────────────┘   │
│                                 ▼                     │
│                      STTProvider.AudioStream()        │
│                          (Deepgram)                   │
└──────────────────────────────────────────────────────┘
```

**VB-Cable создаёт два устройства:**

| Устройство | Тип WASAPI | Роль в Translator |
|-----------|-----------|-------------------|
| `CABLE Input (VB-Audio Virtual Cable)` | **Playback** | **LOOPBACK_DEVICE** — WASAPI loopback захватывает аудио, идущее в это устройство |
| `CABLE Output (VB-Audio Virtual Cable)` | **Recording** | **MIC_DEVICE** (опционально) — можно захватывать как виртуальный микрофон |

**Поток аудио:** Chrome (звонок) → CABLE Input (Playback) → WASAPI Loopback захват → Translator

Без VB-Cable `LOOPBACK_DEVICE` оставляется пустым — захватывается системный звук по умолчанию. Это работает для тестов, но не для двухканального разделения.

### Конфигурация

Приоритет: **env-переменные > `.env` > `config.yaml` > системные по умолчанию**.

```yaml
# config.yaml — production setup с VB-Cable
loopback_device: "CABLE Output (VB-Audio Virtual Cable)"
mic_device: "Microphone (Realtek High Definition Audio)"
```

При старте программа проверяет существование указанных устройств через `capture.ValidateDevice()` и выводит список всех доступных. Если устройство не найдено — падает с ошибкой и перечнем доступных.

## Диаграмма потоков данных

```
                          ┌──────────────────────────────────┐
                          │  VB-Cable (виртуальный канал)    │───► chan []byte "speaker"
                          │  CABLE Input → WASAPI Loopback   │
                          └──────────────────────────────────┘         │
Аудио-захват (malgo) ────┤                                             ▼
                          ┌──────────────────────────┐         [STTProvider Interface]
                          │  Microphone (Микрофон)   │         ├── Deepgram (Сейчас)
                          └──────────────────────────┘         └── Sherpa-onnx (Будущее)
                                                                       │
                                                               chan STTEvent
                                                                       │
                                                                       ▼
[Session Logger] ◄────────────────────────────── [Translation Engine]
(Сохранение json/pcm)                             (GPT-4o-mini + IT Context)
                                                                       │
                                                               chan UIEvent
                                                                       │
                                                                       ▼
                                                                [GioUI Overlay]
                                                              (Субтитры + Ответы A)
```

## Детальная архитектура модулей

```
┌─────────────────────────────────────────────────────────────────────┐
│                           cmd/app/main.go                           │
│  • Чтение config.yaml / env vars                                    │
│  • Wire-up: создание экземпляров, внедрение зависимостей            │
│  • Запуск всех горутин (capture, stt, translator, ui, logger)       │
│  • Graceful shutdown (signal handling)                              │
└─────────────────────────────────────────────────────────────────────┘
        │            │            │              │          │
        ▼            ▼            ▼              ▼          ▼
┌───────────┐ ┌──────────┐ ┌───────────┐ ┌──────────┐ ┌──────────┐
│  capture  │ │   stt    │ │translator │ │    ui    │ │  logger  │
│           │ │          │ │           │ │          │ │          │
│ • malgo   │ │• STTProv │ │• LLMProv  │ │• GioUI   │ │• Session │
│   devices │ │  iface   │ │  iface    │ │  overlay │ │  Logger  │
│ • 2 chans │ │• Deepgr. │ │• Sliding  │ │• Win32   │ │• JSON    │
│ • VB-Cable│ │  WS impl │ │  window   │ │  flags   │ │  writer  │
│ • 48→16   │ │• Sherpa  │ │• QA class │ │• 2 zones │ │• PCM dump│
│   kHz     │ │  stub    │ │  ifier    │ │          │ │          │
│ • Stereo→ │ │          │ │• Prompts  │ │          │ │          │
│   Mono    │ │          │ │           │ │          │ │          │
└───────────┘ └──────────┘ └───────────┘ └──────────┘ └──────────┘
        │            │            │              │          │
        └────────────┴────────────┴──────────────┴──────────┘
                              │
                              ▼
                    ┌─────────────────┐
                    │  common/        │
                    │  • config.go    │ — Config struct, load from YAML + env
                    │  • events.go    │ — STTEvent, UIEvent, TranslateEvent
                    └─────────────────┘
```

## Поток данных (подробно)

### 1. Аудио-захват → STT
```
VB-Cable CABLE Input ──► WASAPI Loopback ──► chan []byte "speaker" ──► STTProvider.AudioStream()
Microphone            ──► WASAPI Capture ──► chan []byte "mic"      ──► STTProvider.AudioStream()
                                                                           │
                                                                 (WebSocket / Local ONNX)
                                                                           │
                                                                           ▼
                                                                 chan STTEvent ──► Translator
                                                                 chan STTEvent ──► Logger
```

### 2. STT → Translator → UI
```
chan STTEvent
    │
    ├── IsFinal == false (interim) ──► UI (preview, low-latency)
    │
    └── IsFinal == true ──► Translation Engine
                                │
                    ┌───────────┴───────────┐
                    │                       │
                    ▼                       ▼
            Translate (EN→RU)      Classification
                    │               (Is it a question?)
                    │                       │
                    │                  YES  │  NO
                    │               ┌───────┘  └── done
                    │               ▼
                    │       GenerateAnswers()
                    │               │
                    └───────┬───────┘
                            ▼
                      chan UIEvent
                            │
                            ▼
                      GioUI Overlay
                    ┌──────────────────┐
                    │ Top: Translation │
                    │ Bot: Hints 1..3  │
                    └──────────────────┘
```

### 3. Логирование (параллельно)
```
chan STTEvent ──► SessionLogger.LogText()      ──► session_*.json
chan []byte   ──► SessionLogger.SaveAudioChunk()──► channel_*.pcm
```

## Ключевые интерфейсы

### STTProvider
```go
type STTProvider interface {
    Start(ctx context.Context) error
    Stop() error
    AudioStream() chan<- []byte       // 16kHz mono PCM input
    TextStream() <-chan STTEvent      // transcription output
}
```

**Реализации:**
- `DeepgramProvider` — текущая: WebSocket к Deepgram API, модель nova-2/nova-3
- `SherpaOnnxProvider` — будущая: локальный ONNX-движок, swap в main.go

### LLMProvider
```go
type LLMProvider interface {
    Translate(ctx context.Context, text string, history []string) (string, error)
    GenerateAnswers(ctx context.Context, question string, cvContext string) ([]string, error)
}
```

**Реализации:**
- `OpenAIProvider` — текущая: GPT-4o-mini через `go-openai`
- Будущие: локальные модели (Ollama, llama.cpp)

### SessionLogger
```go
type SessionLogger interface {
    LogText(event STTEvent) error
    SaveAudioChunk(channelID string, pcm []byte) error
    Close() error
}
```

## События (common/events.go)

```go
type STTEvent struct {
    Text      string
    IsFinal   bool
    ChannelID string    // "speaker" | "mic"
    Timestamp time.Time
    Error     error
}

type UIEvent struct {
    Type       UIEventType // Translation | Answer | AnswerCandidates
    Text       string
    Answers    []string    // only for AnswerCandidates
    Timestamp  time.Time
}
```

## Конфигурация (common/config.go)

```go
type Config struct {
    DeepgramAPIKey   string        // env: DEEPGRAM_API_KEY
    OpenAIAPIKey     string        // env: OPENAI_API_KEY
    OpenAIModel      string        // default: "gpt-4o-mini"
    DeepgramModel    string        // default: "nova-2"
    TargetLanguage   string        // default: "ru"
    LogDir           string        // default: "./logs"
    AudioSampleRate  int           // default: 16000
    AudioChannels    int           // default: 1 (mono)
    WindowSize       int           // default: 5 (sliding window)
    CVContext        string        // CV/resume context for answer generation
    LoopbackDeviceName string     // из config.yaml или .env (LOOPBACK_DEVICE)
    MicDeviceName    string        // из config.yaml или .env (MIC_DEVICE)
}
```

## Потокобезопасность

| Компонент      | Механизм синхронизации                     |
|---------------|--------------------------------------------|
| capture       | Каналы (неблокирующая отправка аудио)       |
| stt           | Mutex на WebSocket connection + каналы      |
| translator    | Mutex на sliding window + каналы            |
| ui            | Mutex на буфер отрисовки + GioUI event loop |
| logger        | Mutex на файловый writer + канал заданий     |

## Graceful Shutdown (порядок)

1. `signal.NotifyContext` ловит SIGINT/SIGTERM
2. ctx cancel → capture.Stop() (закрытие malgo устройств)
3. stt.Stop() (закрытие WebSocket)
4. translator — естественное завершение горутин по ctx
5. ui — закрытие GioUI окна
6. logger.Close() — flush всех буферов на диск
7. os.Exit(0)
