# Архитектура Translator — AI Interview / Meeting Assistant

## Обзор системы

Приложение реального времени, работающее как прозрачный overlay поверх рабочего стола Windows. Предназначено для помощи на технических собеседованиях/встречах: переводит речь собеседника (EN→RU), сохраняет IT-термины, генерирует подсказки к ответам при детекции вопроса.

## Диаграмма потоков данных

```
                          ┌──────────────────────────┐
                          │  Loopback (Динамики)     │───► chan []byte (In)
                          └──────────────────────────┘         │
Аудио-захват (malgo) ────┤                                     ▼
                          ┌──────────────────────────┐   [STTProvider Interface]
                          │  Microphone (Микрофон)   │   ├── Deepgram (Сейчас)
                          └──────────────────────────┘   └── Sherpa-onnx (Будущее)
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
│ • 48→16   │ │  WS impl │ │  window   │ │  flags   │ │  writer  │
│   kHz     │ │• Sherpa  │ │• QA class │ │• 2 zones │ │• PCM dump│
│ • Stereo→ │ │  stub    │ │  ifier    │ │          │ │          │
│   Mono    │ │          │ │• Prompts  │ │          │ │          │
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
malgo Device 1 (Loopback) ──► chan []byte ──► STTProvider.AudioStream()
malgo Device 2 (Mic)      ──► chan []byte ──► STTProvider.AudioStream()
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
    DeepgramAPIKey  string        // env: DEEPGRAM_API_KEY
    OpenAIAPIKey    string        // env: OPENAI_API_KEY
    OpenAIModel     string        // default: "gpt-4o-mini"
    DeepgramModel   string        // default: "nova-2"
    TargetLanguage  string        // default: "ru"
    LogDir          string        // default: "./logs"
    AudioSampleRate int           // default: 16000
    AudioChannels   int           // default: 1 (mono)
    WindowSize      int           // default: 5 (sliding window)
    CVContext       string        // CV/resume context for answer generation
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
