# Архитектура Translator v2 — AI Interview / Meeting Assistant

**Версия:** 2.0 · **Дата:** 2026-07-22
**Статус:** live-тест пройден, 119 тестов, 0 data race

## Обзор системы

Приложение реального времени — прозрачный overlay поверх рабочего стола Windows. Переводит речь собеседника (EN→RU), сохраняет IT-термины, генерирует подсказки при детекции вопроса.

**Ключевые изменения v2:**
- Deepgram Flux `/v2/listen` — встроенный turn detection, EndOfTurn ~260ms
- Worker pool (N=3) — перевод не блокирует STT-поток
- MP3/WAV сохранение аудио (go-lame/beep), сжатие ~10x
- `SAVE_AUDIO` опционально (`.env`), по умолчанию false

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

## Многопоточная архитектура (12 горутин)

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
    ┌───────────────┐   │ STT DISPATCH      │  ← ЦЕНТРАЛЬНЫЙ УЗЕЛ
    │ AUDIO SAVER   │   │                   │
    │ (опционально) │   │ select {          │
    │               │   │  textStream:      │
    │ PCM → MP3/WAV │   │   interim → UI    │
    │ (go-lame/beep)│   │   final → worker  │
    └───────────────┘   │  transResults:    │
                        │   translation → UI│
                        │ }                 │
                        └────────┬──────────┘
                                 │
                    ┌────────────┼────────────┐
                    ▼            ▼            ▼
            ┌──────────┐ ┌──────────┐ ┌──────────┐
            │WORKER 1  │ │WORKER 2  │ │WORKER 3  │
            │Translate │ │Translate │ │Translate │
            │GPT-4o-mini│ │GPT-4o-mini│ │GPT-4o-mini│
            │+Answers  │ │+Answers  │ │+Answers  │
            └────┬─────┘ └────┬─────┘ └────┬─────┘
                 └────────────┼────────────┘
                              ▼ chan transResults (buf 16)
                    ┌──────────────────┐
                    │ STT DISPATCH     │
                    │ → UI.AddMsg()    │
                    │ → Logger.LogText │
                    └──────────────────┘
```

### Список горутин

| # | Горутина | Назначение | Запуск | Завершение |
|---|----------|-----------|--------|------------|
| 1 | `main` | graceful shutdown, сигналы | `main()` | `os.Exit(0)` |
| 2 | `capture·loopback` | malgo WASAPI loopback → chan speaker | `runCapture` | ctx.Done() |
| 3 | `capture·mic` | malgo WASAPI mic → chan mic | `runCapture` | ctx.Done() |
| 4 | `route/merge` | speaker+mic → AudioStream | `runCapture` | chan close |
| 5 | `stt·deepgram` | Flux v2 WebSocket send/recv | `deepgram.Start()` | `deepgram.Stop()` |
| 6 | `stt·dispatch` | центральный select: textStream + transResults | `runDispatch()` | textStream close |
| 7 | `ui·gioui` | GioUI event loop + рендеринг | `runUI()` | DestroyEvent |
| 8 | `audio·saver` | PCM → MP3/WAV на диск (опционально) | `runCapture` | flush + close |
| 9–11 | `worker·1..3` | параллельный перевод GPT-4o-mini | dispatch | ctx.Done() |
| 12 | `logger` | async JSON-логгирование (через канал) | `NewFileSessionLogger()` | `Close()` |

### Каналы

| Канал | Тип | Буфер | Путь |
|---|---|---|---|
| `speakerPCM` | `chan []byte` | 32 | loopback → route |
| `micPCM` | `chan []byte` | 32 | mic → route |
| `audioStream` | `chan []byte` | 64 | route → deepgram |
| `textStream` | `chan STTEvent` | 16 | deepgram → dispatch |
| `transResults` | `chan transResult` | 16 | workers → dispatch |
| `logJobs` | `chan logJob` | 256 | dispatch → logger |

---

## Поток данных

### Interim (промежуточный)
```
PCM → Flux v2 → TurnInfo.Update → textStream → dispatch → UI.AddMsg(interim)
```

### Final (окончательный)
```
PCM → Flux v2 → TurnInfo.EndOfTurn (~260ms) → textStream → dispatch → go worker
                                                                          │
                                                              GPT-4o-mini (1-3s)
                                                                          │
                                                              transResults → dispatch → UI
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
- `Update` → interim-транскрипт
- `EndOfTurn` → финальный транскрипт, запуск перевода
- `StartOfTurn` / `TurnResumed` → игнорируются (без транскрипта)

---

## Translation Engine (worker pool)

```go
type TranslationEngine struct {
    llm       LLMProvider       // GPT-4o-mini
    window    []string          // sliding window (max 5)
    maxWindow int
    mu        sync.RWMutex
}
```

**Pipeline per EndOfTurn:**
1. Append to sliding window → FIFO, trim to maxWindow
2. Get history (window except most recent)
3. Translate(text, history) → Russian (temperature 0.1)
4. Classify: `isQuestion()` — ? suffix or question words
5. If question → async GenerateAnswers() (temperature 0.3)

**Worker pool:** N=3 горутины (настраивается через `TRANSLATION_WORKERS` env). Каждая вызывает `ProcessFinalTranscript()` → результат в `transResults` → dispatch → UI.

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

type SessionLogger interface {
    LogText(event STTEvent) error
    SaveAudioChunk(channelID string, pcm []byte) error
    Close() error
}

type AudioEncoder interface {
    Write(pcm []int16) error
    Close() error
}
```

---

## Конфигурация

Приоритет: **env > .env > config.yaml**

```yaml
# config.yaml
deepgram_model: "flux-general-en"
openai_model: "gpt-4o-mini"
target_language: "ru"
log_dir: "./logs"
audio_sample_rate: 16000
window_size: 5
save_audio: false
loopback_device: "CABLE Input (VB-Audio Virtual Cable)"
```

```env
# .env
DEEPGRAM_API_KEY=...
OPENAI_API_KEY=...
SAVE_AUDIO=true           # опциональное сохранение аудио
TRANSLATION_WORKERS=3     # количество параллельных переводчиков
LOOPBACK_DEVICE=CABLE Input (VB-Audio Virtual Cable)
```

---

## Graceful Shutdown

1. SIGINT/SIGTERM → ctx cancel
2. capture.Close() → malgo устройства остановлены
3. speakerPCM/micPCM закрыты → route завершается
4. deepgram.Stop() → WebSocket close → textStream закрыт
5. workers завершаются по ctx.Done()
6. transResults закрыт → dispatch завершается
7. ui получает DestroyEvent
8. audio·saver flush + close (MP3 финальный фрейм)
9. logger.Close() → drain logJobs → flush JSON
10. os.Exit(0)

---

## Стек технологий

| Слой | Библиотека | Назначение |
|---|---|---|
| Audio Capture | `malgo` (CGo) | WASAPI Loopback + Mic |
| STT | Deepgram Flux `/v2/listen` | Turn-aware streaming |
| LLM | `go-openai` → GPT-4o-mini | Перевод + подсказки |
| MP3 | `go-lame` (CGo) | PCM → MP3 кодирование |
| WAV | `beep/v2` | WAV fallback |
| UI | `gioui.org` v0.10.1 | Прозрачный overlay |
| Win32 | `golang.org/x/sys/windows` | WS_EX_TOPMOST/LAYERED |
| Logger | `encoding/json` | JSON Lines |

---

## Задержка (было → стало)

```
v1 (Nova-2):  речь → тишина ~1-2с → is_final → перевод 1-3с  =  2-5 сек
v2 (Flux):    речь → EndOfTurn ~260ms → worker pool → перевод  =  ~1.2-3.2 сек
```

Выигрыш: ~1-2 секунды за счёт Flux turn detection + параллелизм worker pool.
