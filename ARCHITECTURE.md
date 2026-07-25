# Архитектура Translator v3 — AI Interview / Meeting Assistant

**Версия:** 3.0 · **Дата:** 2026-07-23
**Статус:** стриминг-перевод + трёхзонный UI, все тесты PASS

## Обзор системы

Приложение реального времени — прозрачный overlay поверх рабочего стола Windows. Переводит речь собеседника (EN→RU), сохраняет IT-термины, генерирует подсказки при детекции вопроса.

**Ключевые изменения v3:**
- **Стриминг-перевод** — токены идут в UI инкрементально, без ожидания полного ответа. Замена worker pool на прямые стрим-горутины.
- **GLM-4.7-Flash** (Z.AI) вместо GPT-4o-mini — задержка ~1s (было 3-5s)
- **thinking:disabled** — транспортный слой в `openai.go` инжектит `{"thinking":{"type":"disabled"}}` для Z.AI
- **Трёхзонный UI** — речь (Interim), перевод (скролл 10 строк), подсказки
- **Цветовой индикатор** — янтарный (pending) → зелёный (streaming) → тёмный (done)
- **Событийная перерисовка** — `w.Invalidate()` при каждом `AddMessage`
- **Свежие контексты** — стриминг и подсказки с собственными `context.WithTimeout`

---

## Аудио-архитектура: VB-Cable

Двухканальный захват через виртуальный аудиоканал VB-Cable для разделения звука собеседника и вашего голоса.

```
Chrome/Teams ──► CABLE Input (Playback) ──► WASAPI Loopback ──► chan speaker (PCM)
Микрофон     ──► WASAPI Capture           ──► chan mic (PCM)
                        │                           │
                        └──────────┬────────────────┘
                                   ▼
                           route/merge → AudioStream → Deepgram Flux v2
```

| Устройство | Тип WASAPI | Роль |
|---|---|---|
| `CABLE Input (VB-Audio Virtual Cable)` | Playback | LOOPBACK_DEVICE |
| `CABLE Output (VB-Audio Virtual Cable)` | Recording | MIC_DEVICE (опционально) |

---

## Многопоточная архитектура (9 горутин)

```
┌──────────────────────────────────────────────────────────────────────┐
│                        MAIN GOROUTINE                                │
│  signal.NotifyContext → ctx → запуск всех горутин → ожидание Ctrl+C │
└──────────────────────────────────────────────────────────────────────┘
        │          │           │            │
        ▼          ▼           ▼            ▼
┌───────────┐ ┌─────────┐ ┌──────────┐ ┌──────────┐
│ CAPTURE   │ │ CAPTURE  │ │ STT      │ │ UI       │
│ Loopback  │ │ Mic      │ │ Deepgram │ │ GioUI    │
│ 80ms      │ │ 80ms     │ │ Flux v2  │ │ overlay  │
│ malgo →   │ │ malgo →  │ │ WS send  │ │ event    │
│ chan      │ │ chan     │ │ WS recv  │ │ loop     │
│ speaker   │ │ mic      │ │          │ │          │
└─────┬─────┘ └────┬─────┘ └────┬─────┘ └──────────┘
      │            │            │
      └─────┬──────┘            │ chan STTEvent
            │                   │ (buf 16)
            ▼                   │
    ┌───────────────┐           │
    │ ROUTE/MERGE   │           │
    │ 2:1 speaker+  │           │
    │ mic → Audio-  │           │
    │ Stream (buf64)│           │
    └───────────────┘           │
            │                   ▼
            ▼           ┌───────────────────┐
    ┌───────────────┐   │ DISPATCH          │  ← ЦЕНТРАЛЬНЫЙ УЗЕЛ
    │ AUDIO SAVER   │   │                   │
    │ (опционально) │   │ select {          │
    │               │   │  textStream:      │
    │ PCM → MP3/WAV │   │   interim → UI    │
    │ (go-lame/beep)│   │   final → stream  │
    └───────────────┘   │  streamDone:      │
                        │   счётчик         │
                        │ }                 │
                        └────────┬──────────┘
                                 │
                    ┌────────────┼────────────┐
                    ▼            ▼            ▼
            ┌──────────┐ ┌──────────┐ ┌──────────┐
            │ STREAM 1 │ │ STREAM 2 │ │ STREAM 3 │
            │ GLM-4.7  │ │ GLM-4.7  │ │ GLM-4.7  │
            │ токены→  │ │ токены→  │ │ токены→  │
            │ UI инкр. │ │ UI инкр. │ │ UI инкр. │
            └──────────┘ └──────────┘ └──────────┘
```

### Список горутин

| # | Горутина | Назначение | Запуск | Завершение |
|---|----------|-----------|--------|------------|
| 1 | `main` | graceful shutdown, сигналы | `main()` | `os.Exit(0)` |
| 2 | `capture·loopback` | malgo WASAPI loopback → chan speaker | `runCapture` | ctx.Done() |
| 3 | `capture·mic` | malgo WASAPI mic → chan mic | `runCapture` | ctx.Done() |
| 4 | `route/merge` | speaker+mic → AudioStream | `runCapture` | chan close |
| 5 | `stt·deepgram` | Flux v2 WebSocket send/recv | `deepgram.Start()` | `deepgram.Stop()` |
| 6 | `dispatch` | центральный select: textStream + стрим-горутины | `runDispatch()` | textStream close |
| 7 | `ui·gioui` | GioUI event loop + рендеринг | `runUI()` | DestroyEvent |
| 8 | `audio·saver` | PCM → MP3/WAV на диск (опционально) | `runCapture` | flush + close |
| 9 | `logger` | async CSV-логгирование (через канал) | `NewFileSessionLogger()` | `Close()` |

### Каналы

| Канал | Тип | Буфер | Путь |
|---|---|---|---|
| `speakerPCM` | `chan []byte` | 32 | loopback → route |
| `micPCM` | `chan []byte` | 32 | mic → route |
| `audioStream` | `chan []byte` | 64 | route → deepgram |
| `textStream` | `chan STTEvent` | 16 | deepgram → dispatch |
| `streamDone` | `chan struct{}` | 64 | стрим-горутины → dispatch (shutdown) |
| `logJobs` | `chan logJob` | 256 | dispatch → logger |

---

## Поток данных

### Interim (промежуточный)
```
PCM → Flux v2 → TurnInfo.Update → textStream → dispatch → UI.AddMsg(Interim) → верхняя зона
```

### Final → Стриминг-перевод
```
PCM → Flux v2 → EndOfTurn (~260ms) → textStream → dispatch → go handleStreamingTranslation
                                                                     │
                                          ┌──────────────────────────┘
                                          ▼
                                    [переводится...] → UI (pending, янтарный фон)
                                          │
                                          ▼
                              ProcessFinalTranscriptStream → TranslateStream
                                          │
                                    токены → UI инкрементально (streaming, зелёный фон)
                                          │
                                          ▼
                                    финальный перевод → UI (done, тёмный фон)
                                          │
                                          ├─ isQuestion? → GenerateAnswers → UI (подсказки)
                                          └─ history trim (MaxLines)
```

### UI — три зоны
```
┌──────────────────────────┐
│ I have five years of...  │ ← Interim (речь, 2 строки, серый текст)
├──────────────────────────┤
│ У меня пять лет опыта... │ ← Перевод (скролл 10 строк)
│ Мы используем Redis...    │
│ [переводится...]          │
├──────────────────────────┤
│ 1. Подсказка один        │ ← AnswerCandidates (только для вопросов)
│ 2. Подсказка два         │
└──────────────────────────┘
```

### Сохранение аудио (SAVE_AUDIO=true)
```
PCM → AudioEncoder → MP3 (go-lame) / WAV (beep) → audio/speaker.mp3
```

---

## Deepgram Flux v2

| Параметр | Значение |
|---|---|
| Endpoint | `wss://api.deepgram.com/v2/listen` |
| Модель | `flux-general-en` |
| Кодирование | `linear16`, 16000 Hz, mono |
| Чанк | 80ms (2560 байт) |
| EndOfTurn | `eot_threshold=0.8`, ~260ms p50 |
| KeepAlive | НЕ используется (v1-специфичный) |

**События Flux:**
- `Update` → interim-транскрипт → верхняя зона UI
- `EndOfTurn` → финальный транскрипт → запуск стриминг-перевода
- `StartOfTurn` / `TurnResumed` → игнорируются (без транскрипта)

**Фильтрация дублирующих Update:** Deepgram Flux v2 шлёт `Update` на каждый аудио-фрейм (~200ms), даже если транскрипция не изменилась. `DeepgramProvider.lastInterimText` хранит последний промежуточный текст — при совпадении с предыдущим `Update` пропускается. При `EndOfTurn` сбрасывается для следующей фразы. Это снижает нагрузку на UI (лишние `Invalidate`) и уменьшает зашумлённость CSV-лога.

---

## Translation Engine

```go
type TranslationEngine struct {
    llm       LLMProvider       // GLM-4.7-Flash (OpenAI-совместимый)
    window    []string          // sliding window (max 5)
    maxWindow int
    mu        sync.RWMutex
}
```

**Pipeline per EndOfTurn (стриминг):**
1. Append to sliding window → FIFO, trim to maxWindow
2. Get history (window except most recent)
3. `TranslateStream(ctx, text, history)` → токены в канал (temperature 0.1)
4. Токены инкрементально → UI (streaming-статус)
5. По завершении → классификация `IsQuestion()`
6. Если вопрос → `GenerateAnswers()` (temperature 0.3)

**Фолбэк:** если провайдер не реализует `StreamingTranslator` → синхронный `Translate` через канал из одного элемента.

---

## LLM: GLM-4.7-Flash (Z.AI)

| Параметр | Значение |
|---|---|
| Endpoint | `https://api.z.ai/api/paas/v4/chat/completions` |
| Модель | `glm-4.7-flash` |
| thinking | **disabled** (транспортный слой `thinkingTransport`) |
| max_tokens | `LLM_MAX_TOKENS` (default 1024) |
| temperature | 0.1 (translate), 0.3 (answers) |
| Стриминг | SSE, `stream: true` |

**thinkingTransport** — `http.RoundTripper` в `openai.go`, инжектит `{"thinking":{"type":"disabled"}}` в тело запроса к `api.z.ai`. Без этого все токены уходят в `reasoning_content`, а `content` остаётся пустым.

**Перевод на GLM не использует Translation Agent** (`/api/v1/agents`). Agent API в 3-4 раза медленнее из-за встроенной трёхэтапной рефлексии (перевод → анализ → исправление) и требует платного аккаунта.

---

## Ключевые интерфейсы

```go
type STTProvider interface {
    Start(ctx context.Context) error
    Stop() error
    AudioStream() chan<- []byte       // 16kHz mono PCM
    TextStream() <-chan STTEvent
}

type LLMProvider interface {
    Translate(ctx, text, history) (string, error)
    GenerateAnswers(ctx, question, cvContext) ([]string, error)
}

type StreamingTranslator interface {
    LLMProvider
    TranslateStream(ctx, text, history) (<-chan string, error)
}

type SessionLogger interface {
    LogText(event STTEvent) error
    LogTranslation(event, translation, answers []string) error
    SaveAudioChunk(channelID string, pcm []byte) error
    Close() error
}
```

---

## Конфигурация

Приоритет: **env > .env > config.yaml**

### config.yaml
```yaml
deepgram_model: "flux-general-en"
openai_model: "glm-4.7-flash"
llm_base_url: "https://api.z.ai/api/paas/v4/"
target_language: "ru"
log_dir: "./logs"
window_size: 5
save_audio: false
loopback_device: "CABLE Input (VB-Audio Virtual Cable)"
```

### .env
```env
DEEPGRAM_API_KEY=...
OPENAI_API_KEY=...
LLM_BASE_URL=https://api.z.ai/api/paas/v4/
OPENAI_MODEL=glm-4.7-flash
LLM_MAX_TOKENS=1024
LOOPBACK_DEVICE=CABLE Input (VB-Audio Virtual Cable)
OVERLAY_WIDTH=800
OVERLAY_HEIGHT=400
OVERLAY_MAX_LINES=10
SAVE_AUDIO=true
```

---

## Graceful Shutdown

1. SIGINT/SIGTERM → ctx cancel
2. capture.Close() → malgo устройства остановлены
3. speakerPCM/micPCM закрыты → route завершается
4. deepgram.Stop() → WebSocket close → textStream закрыт
5. dispatch: закрытие textStream → ожидание активных стримов (streamDone)
6. ui: `system.ActionClose` → слив DestroyEvent
7. audio·saver flush + close (MP3 финальный фрейм)
8. logger.Close() → drain logJobs → flush CSV
9. os.Exit(0)

---

## Стек технологий

| Слой | Библиотека | Назначение |
|---|---|---|
| Audio Capture | `malgo` (CGo) | WASAPI Loopback + Mic |
| STT | Deepgram Flux `/v2/listen` | Turn-aware streaming |
| LLM | `go-openai` → GLM-4.7-Flash | Стриминг-перевод + подсказки |
| MP3 | `go-lame` (CGo) | PCM → MP3 кодирование |
| WAV | `beep/v2` | WAV fallback |
| UI | `gioui.org` v0.10.1 | Прозрачный трёхзонный overlay |
| Win32 | `golang.org/x/sys/windows` | WS_EX_TOPMOST/LAYERED |

---

## Задержка

```
Flux EndOfTurn:  ~260ms (от речи до финального транскрипта)
GLM-4.7-Flash:   ~1s   (от запроса до полного перевода, thinking:disabled)
Первый токен:    ~0ms  (стриминг, UI обновляется инкрементально)
Общая:           ~1.3s (речь → перевод на экране)
```
