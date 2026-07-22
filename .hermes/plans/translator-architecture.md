# Многопоточная архитектура Translator

**Создан:** 2026-07-22
**Версия:** v2 — worker pool + beep

---

## Диаграмма горутин и каналов

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
│ goroutine │ │ goroutine│ │ goroutine│ │ goroutine│
│           │ │          │ │          │ │          │
│ malgo →   │ │ malgo →  │ │ WS recv  │ │ Event    │
│ chan      │ │ chan     │ │ WS send  │ │ loop +   │
│ speaker   │ │ mic      │ │          │ │ render   │
│ (PCM)     │ │ (PCM)    │ │          │ │          │
└─────┬─────┘ └────┬─────┘ └────┬─────┘ └──────────┘
      │            │            │
      │ chan       │ chan       │ chan STTEvent
      │ []byte     │ []byte     │ (interim + final)
      │            │            │
      └─────┬──────┘            │
            │                   │
            ▼                   │
    ┌───────────────┐           │
    │ ROUTE/MERGE   │           │
    │ goroutine     │           │
    │               │           │
    │ 2:1 merge     │           │
    │ speaker+mic → │           │
    │ AudioStream() │           │
    └───────────────┘           │
            │                   │
            ▼                   ▼
    ┌───────────────┐   ┌───────────────────┐
    │ AUDIO SAVER   │   │ STT DISPATCH      │  ← ЦЕНТРАЛЬНЫЙ УЗЕЛ
    │ goroutine     │   │ goroutine         │
    │ (опционально) │   │                   │
    │               │   │ select {          │
    │ beep.Buffer   │   │  case textStream: │
    │ → WAV/MP3     │   │    if interim →   │
    │   на диск     │   │      UI.AddMsg()  │
    │               │   │    if final →     │
    └───────────────┘   │      go translate │
                        │  case transResult:│
                        │    UI.AddMsg()    │
                        │ }                 │
                        └────────┬──────────┘
                                 │ final + go
                    ┌────────────┼────────────┐
                    ▼            ▼            ▼
            ┌──────────┐ ┌──────────┐ ┌──────────┐
            │WORKER 1  │ │WORKER 2  │ │WORKER 3  │
            │goroutine │ │goroutine │ │goroutine │
            │          │ │          │ │          │
            │Translate │ │Translate │ │Translate │
            │(GPT-4o)  │ │(GPT-4o)  │ │(GPT-4o)  │
            │          │ │          │ │          │
            │if quest. │ │if quest. │ │if quest. │
            │ →Answers │ │ →Answers │ │ →Answers │
            └────┬─────┘ └────┬─────┘ └────┬─────┘
                 │            │            │
                 └────────────┼────────────┘
                              │ chan TranslationResult
                              ▼
                    ┌──────────────────┐
                    │ STT DISPATCH     │
                    │ (select case)    │
                    │ → UI.AddMsg()    │
                    │ → Logger.LogText │
                    └──────────────────┘
```

---

## Список горутин

| # | Имя | Назначение | Запуск | Завершение |
|---|-----|-----------|--------|------------|
| 1 | `main` | graceful shutdown | `main()` | `os.Exit(0)` |
| 2 | `capture·loopback` | malgo WASAPI loopback → chan speaker | `runCapture` | ctx.Done() → malgo.Stop() |
| 3 | `capture·mic` | malgo WASAPI mic → chan mic | `runCapture` | ctx.Done() → malgo.Stop() |
| 4 | `route/merge` | слияние speaker+mic → AudioStream | `runCapture` | speaker/mic chan close |
| 5 | `stt·deepgram` | WebSocket send/recv → chan textStream | `deepgram.Start()` | `deepgram.Stop()` |
| 6 | **`stt·dispatch`** | центральный select-узел | `runDispatch()` | textStream close |
| 7 | `ui·gioui` | цикл событий GioUI + рендеринг | `runUI()` | DestroyEvent |
| 8 | `audio·saver` | beep.Buffer → WAV/MP3 на диск | (опционально) | flush + close |
| 9 | `worker·1` | Translate(text, history) | dispatch → go worker() | ctx.Done() |
| 10 | `worker·2` | Translate(text, history) | dispatch → go worker() | ctx.Done() |
| 11 | `worker·3` | Translate(text, history) | dispatch → go worker() | ctx.Done() |
| 12 | `logger` | async JSON-логгирование (через канал) | `NewFileSessionLogger()` | `Close()` → drain |

**Итого:** 12 горутин (3 worker'а). Расширяемо: N worker'ов настраивается через `TRANSLATION_WORKERS` (default: 3).

---

## Каналы

| Канал | Тип | Буфер | Отправитель | Получатель |
|---|---|---|---|---|
| `speakerPCM` | `chan []byte` | 32 | capture·loopback | route/merge |
| `micPCM` | `chan []byte` | 32 | capture·mic | route/merge |
| `audioStream` | `chan []byte` | 64 | route/merge | stt·deepgram |
| `textStream` | `chan STTEvent` | 16 | stt·deepgram | stt·dispatch |
| `transResults` | `chan transJob` | 16 | worker·1..3 | stt·dispatch |
| `logJobs` | `chan logJob` | 256 | stt·dispatch | logger (async) |

---

## Структуры данных

```go
// transJob — задание на перевод, летит от dispatch к worker'у
type transJob struct {
    event    common.STTEvent
    resultCh chan transResult
}

// transResult — результат перевода, летит от worker'а к dispatch
type transResult struct {
    translation string
    answers     []string
    isQuestion  bool
    err         error
    event       common.STTEvent // оригинальное событие для логирования
}
```

---

## Поток данных (по шагам)

### Interim-транскрипт
```
speaker/mic PCM → route → AudioStream → Deepgram WS → textStream → dispatch
                                                                       │
                                                          UI.AddMsg(interim)
                                                          Logger.LogText(event)
```

### Final-транскрипт
```
speaker/mic PCM → route → AudioStream → Deepgram WS → textStream → dispatch
                                                                       │
                                                    ┌──────────────────┘
                                                    │ go worker.Translate()
                                                    │    │
                                                    │    ▼ (1-3 сек)
                                                    │ transResults ← worker
                                                    │    │
                                                    │    ▼
                                                    │ dispatch (select case)
                                                    │    │
                                                    │    ├── UI.AddMsg(translation)
                                                    │    ├── UI.AddMsg(answers) // если вопрос
                                                    │    └── Logger.LogText(event)
                                                    │
                                                    └── (dispatch продолжает читать textStream)
```

### Сохранение аудио (опционально)
```
speaker/mic PCM → route → audio·saver → beep.Buffer
                                  │
                                  └── периодический flush → WAV/MP3 на диск
                                                        → Close() при shutdown
```

---

## Graceful Shutdown (порядок)

```
1. SIGINT/SIGTERM → ctx cancel
2. capture·loopback + capture·mic: malgo.Stop() → speakerPCM/micPCM close
3. route/merge: выход при закрытии входных каналов
4. stt·deepgram: deepgram.Stop() → WebSocket close → textStream close
5. worker·1..3: ctx.Done() → выход из Translate → transResults close
6. stt·dispatch: textStream close → выход из select → drain transResults
7. ui·gioui: DestroyEvent → выход из Event loop
8. audio·saver: flush beep.Buffer → close файлы
9. logger: Close() → drain logJobs → flush JSON
10. os.Exit(0)
```

---

## Конфигурация

```env
# .env
SAVE_AUDIO=true              # сохранять аудио (default: false)
TRANSLATION_WORKERS=3        # количество параллельных переводчиков (default: 3)
```

```yaml
# config.yaml
save_audio: false            # переопределяется через SAVE_AUDIO в .env
```

Приоритет: `env > .env > config.yaml`

---

## Сравнение: было → стало

| Аспект | Было (v1) | Стало (v2) |
|---|---|---|
| Перевод | Синхронный в цикле STT | Worker pool, результат через канал |
| Блокировка | 1-3 сек без interim | Interim идут непрерывно |
| Параллелизм | 1 вызов GPT-4o-mini | N=3 параллельных |
| Сохранение аудио | Всегда, raw PCM | Опционально, WAV/MP3 |
| Буферизация аудио | Побайтово в файл | beep.Buffer → пачки |
| Формат аудио | .pcm (сырой) | .mp3 (go-lame) или .wav (beep) |
| Конфигурация | config.yaml | .env + config.yaml |
