# Архитектура Translator v4 — AI Interview / Meeting Assistant

**Версия:** 4.0 · **Дата:** 2026-07-25
**Статус:** ✅ Миграция на Gladia завершена. STT, перевод, LLM-подсказки — все компоненты работают. `go vet` чист, `go test -race ./...` → 8/8 PASS.

## Обзор системы

Приложение реального времени — прозрачный overlay поверх рабочего стола Windows. Распознаёт речь собеседника (EN), получает перевод от Gladia (встроенный, EN→RU), сохраняет IT-термины, генерирует подсказки при детекции вопроса.

**Ключевые изменения v4 (миграция с Deepgram на Gladia):**
- **STT: Gladia Solaria-1** — turn-aware стриминг через WebSocket
- **Перевод: Gladia Translation** — встроенный в тот же WebSocket, модель `enhanced`, без отдельного LLM-перевода
- **LLM: только подсказки** — `GenerateAnswersStream`, без `TranslateStream`
- **Двухфазный коннект** — POST `/v2/live` → `{id, url}` → WebSocket dial
- **Меньше горутин** — потеря 3 стриминг-горутин для перевода (было 12, стало 9)
- **Dispatch упрощён** — transcript → UI, translation → UI, вопрос → GenerateAnswers

### Историческая справка

| Версия | STT | Перевод | Дата |
|--------|-----|---------|------|
| v1–v3 | Deepgram Flux v2 | LLM (GLM-4.7-Flash) | до 2026-07-25 |
| **v4** | **Gladia Solaria-1** | **Gladia Translation (enhanced)** | 2026-07-25 |

Причины миграции: встроенный перевод в Gladia исключает сетевой round-trip до LLM, снижает задержку перевода с ~1.5s до ~200ms, упрощает архитектуру и уменьшает стоимость (один API вместо двух).

### Процесс архитектурных решений

- Многопоточная схема, выбор библиотек и значительные изменения — сначала план на утверждение пользователю, потом реализация
- Схемы сохраняются в `.hermes/plans/`
- Конфигурация: `.env` в приоритете над `config.yaml`

---

## Аудио-архитектура: VB-Cable

Двухканальный захват через виртуальный аудиоканал VB-Cable для разделения звука собеседника и вашего голоса.

```
Chrome/Teams ──► CABLE Input (Playback) ──► WASAPI Loopback ──► audioStream → Gladia WebSocket
Микрофон     ──► WASAPI Capture           ──► audioStream → Gladia WebSocket
                                                    │
                                          (опционально) → логгер аудио
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
        │          │           │            │            │
        ▼          ▼           ▼            ▼            ▼
┌───────────┐ ┌─────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐
│ CAPTURE   │ │ CAPTURE  │ │ STT      │ │ DISPATCH │ │ UI       │
│ Loopback  │ │ Mic      │ │ Gladia   │ │          │ │ GioUI    │
│ 80ms PCM  │ │ 80ms PCM │ │ Solaria-1│ │ select { │ │ event    │
│ → audio-  │ │ → fan-out│ │ WebSocket│ │  interim │ │ loop     │
│   Stream  │ │   STT +  │ │ write+   │ │  → UI    │ │          │
│           │ │   logger │ │ readPump │ │  final   │ │          │
│           │ │          │ │ (2 гор.) │ │  → UI    │ │          │
│           │ │          │ │          │ │  transl. │ │          │
│           │ │          │ │          │ │  → UI    │ │          │
│           │ │          │ │          │ │  ? → ans │ │          │
└───────────┘ └─────────┘ └────┬─────┘ └────┬─────┘ └──────────┘
                               │            │
                        textStream (chan STTEvent, buf 64)
                               │            │
                        ┌──────┘            │
                        │                   │
                        ▼                   ▼
               ┌──────────────┐    ┌───────────────┐
               │ Gladia       │    │ runSTT        │
               │ writePump    │    │ routeSTTEvent │
               │ audioCh → WS │    │ textCh →      │
               │ readPump     │    │ textStream    │
               │ WS → textCh  │    │               │
               └──────────────┘    └───────────────┘
```

### Список горутин

| # | Горутина | Назначение | Запуск | Завершение |
|---|----------|-----------|--------|------------|
| 1 | `main` | graceful shutdown, сигналы | `main()` | `os.Exit(0)` |
| 2 | `capture·loopback` | malgo WASAPI loopback → audioStream | `runCapture` | ctx.Done() |
| 3 | `capture·mic` | malgo WASAPI mic → fan-out (STT + logger) | `runCapture` | ctx.Done() |
| 4 | `capture·mic·logger` | запись сырого микрофона → логгер | `runCapture` | ctx.Done() |
| 5 | `stt·gladia·writePump` | audioStream → Gladia WebSocket (BinaryMessage) | `GladiaProvider.Start()` | ctx.Done() / conn close |
| 6 | `stt·gladia·readPump` | Gladia WebSocket → parseAndEmit → textCh | `GladiaProvider.Start()` | ctx.Done() / conn close |
| 7 | `route·stt` (runSTT) | textCh → маршрутизация в textStream | `pipeline.Run()` | textCh close |
| 8 | `dispatch` | центральный select: transcript/translation → UI + вопрос → подсказки | `runDispatch()` | textStream close |
| 9 | `ui·gioui` | GioUI event loop + рендеринг | `runUI()` | DestroyEvent |

### Каналы

| Канал | Тип | Буфер | Путь |
|---|---|---|---|
| `audioCh` (внутри GladiaProvider) | `chan []byte` | 64 | runCapture → writePump |
| `textCh` (внутри GladiaProvider) | `chan STTEvent` | 32 | readPump → runSTT |
| `textStream` (в Pipeline) | `chan STTEvent` | 64 | runSTT → dispatch |
| `dispatchDone` | `chan struct{}` | 1 | dispatch → shutdown |

---

## Поток данных

### Interim (промежуточный)
```
PCM → Gladia WS → transcript (is_final=false) → textCh → textStream → dispatch → UI Interim (серый)
```

### Final → Перевод (встроенный Gladia)
```
PCM → Gladia WS → transcript (is_final=true) → textCh → textStream → dispatch → UI History (оригинал)
                                                                          │
                                                    ┌─────────────────────┘
                                                    │ (Gladia шлёт translation ПОСЛЕ transcript)
                                                    ▼
                    Gladia WS → translation → textCh → textStream → dispatch → UI Translation + UI History (оригинал + перевод)
```

### Вопрос → Подсказки (LLM)
```
dispatch: IsQuestion(transcript.Text) == true
    │
    └─► go generateAnswersAsync(question)
         │
         └─► engine.GenerateAnswersStream(ctx, question)
              │
              └─► SSE-токены → parseAnswerHints → UI AnswerCandidates
```

### UI — четыре зоны (GioUI v0.10.1)

```
┌──────────────────────────┐
│ I have five years of...  │ ← Зона 1: Interim (речь, 2 строки, белый текст)
├──────────────────────────┤ separator 3px
│ EN: We use Redis for...  │ ← Зона 2: TranslationHistory (скролл, макс 10 строк)
│ RU: Мы используем Redis  │           оригинал + перевод от Gladia
├──────────────────────────┤ separator 3px
│ We use Redis for caching │ ← Зона 3: TranscriptionHistory (скролл, макс 5 строк)
│ and message brokering    │           оригинал речи на английском
├──────────────────────────┤ separator 3px
│ EN: Redis is...          │ ← Зона 4: AnswerCandidates (1 подсказка, только вопросы)
│ RU: Redis — это...       │           формат: EN: ... | RU: ...
└──────────────────────────┘
```

**Параметры окна:**
- Размер: 800×650 (из `.env`: `OVERLAY_WIDTH`, `OVERLAY_HEIGHT`)
- Заголовок: пустой (screen sharing privacy)
- Заголовки зон: **отсутствуют** — только разделители 3px между зонами
- Позиционирование: `app.TopMost(true)` — поверх других окон
- Стили: **без** `WS_EX_LAYERED`/`WS_EX_TRANSPARENT` (ломают рендеринг Gio)
- Win32: `WS_EX_NOACTIVATE` через `findWindowByPID`
- Принцип: минимальные решения, без лишних HWND-стилей — `TopMost(true)` достаточно

**Автоскролл:**
- `prevHistLen` — сохраняется предыдущая длина списка
- `layout.List` — персистентный, не пересоздаётся
- При добавлении элемента: `ScrollTo(n-1)` — прокрутка к последнему

**Запуск:** `app.Main()` **не используется** — вместо него кастомный event loop.

**Тест:** `TestWindowStarts` — 40 строк, проверка первого и последнего элемента списка.

### Сохранение аудио (SAVE_AUDIO=true)
```
PCM (mic) → логгер → audio/speaker.mp3
```

---

## Gladia Live API v2

| Параметр | Значение |
|---|---|
| Endpoint init | `POST https://api.gladia.io/v2/live` |
| Endpoint WS | динамический URL (из ответа init) |
| Модель STT | `solaria-1` |
| Модель перевода | `enhanced` |
| Кодирование | `wav/pcm`, 16-bit, 16000 Hz, mono |
| Endpointing | `0.3` |
| Аутентификация | `x-gladia-key` header |

**Двухфазный коннект:**
1. `POST /v2/live` с JSON-конфигурацией (модель, язык, перевод, endpointing)
2. Ответ: `{"id": "...", "url": "wss://..."}` — статус 201 Created
3. WebSocket dial по полученному URL
4. Отправка PCM-фреймов (BinaryMessage) + чтение JSON-событий

**События Gladia:**

| Тип | STTEvent.Event | ChannelID | Назначение |
|---|---|---|---|
| `transcript` (is_final=false) | `EventUpdate` | `"speaker"` | Interim → UI |
| `transcript` (is_final=true) | `EventEndOfTurn` | `"speaker"` | Final → dispatch (сохраняется как lastOriginal) |
| `translation` | `EventEndOfTurn` | `"translation"` | Перевод → UI Translation + связка с lastOriginal |

**Связывание transcript ↔ translation:** Gladia шлёт `translation` ПОСЛЕ `transcript`. Dispatch хранит `lastOriginal` — последний финальный транскрипт. При получении `translation` выводит пару (оригинал + перевод) в историю.

---

## LLM: GLM-4.7-Flash (Z.AI)

| Параметр | Значение |
|---|---|
| Endpoint | `https://api.z.ai/api/paas/v4/chat/completions` |
| Модель | `glm-4.7-flash` |
| Вызов | `GenerateAnswers` (синхронный) + `GenerateAnswersStream` (SSE-стриминг) |
| max_tokens | `LLM_MAX_TOKENS` (default 1024) |
| temperature | 0.3 (answers) |
| Подсказок | **1** (промпт `SystemPromptAnswerGen` → "EXACTLY 1 bullet point") |
| Формат | `EN: <English> \| RU: <Russian>` — UI разделяет на две строки |
| Контекст | `CVContext` из `config.yaml` → `Pipeline.Config.CVContext` → `engine.SetCVContext()` → `BuildAnswerPrompt(question, cvContext)` |
| Thinking | **включён по умолчанию** (GLM-4.7-Flash включает thinking автоматически). `DisableThinking()` посылает `"thinking": {"type": "disabled"}` в raw HTTP-запросе |

**LLM используется ТОЛЬКО для генерации подсказок.** Перевод выполняется Gladia (встроенный, модель `enhanced`).

**thinkingTransport удалён из стриминг-пути** — управление thinking теперь через флаг `disableThinking` в `OpenAIProvider`, без инжекции в тело запроса на уровне транспорта.

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
    GenerateAnswers(ctx context.Context, question string, cvContext string) ([]string, error)
}

type StreamingAnswersProvider interface {
    LLMProvider
    GenerateAnswersStream(ctx context.Context, question string, cvContext string) (<-chan string, error)
}

type SessionLogger interface {
    LogText(event STTEvent) error
    LogTranslation(event STTEvent, translation string, answers []string) error
    SaveAudioChunk(channelID string, pcm []byte) error
    Close() error
}
```

**Изменения относительно v3:** из `LLMProvider` убран `Translate`. Интерфейс `StreamingTranslator` удалён. Перевод выполняется Gladia, а не LLM.

---

## Конфигурация

Приоритет: **env > .env > config.yaml**

### config.yaml
```yaml
openai_model: "glm-4.7-flash"
llm_base_url: "https://api.z.ai/api/paas/v4/"
target_lang: "ru"
log_dir: "./logs"
window_size: 5
save_audio: false
loopback_device: "CABLE Input (VB-Audio Virtual Cable)"
```

### .env
```env
GLADIA_API_KEY=...
OPENAI_API_KEY=...
LLM_BASE_URL=https://api.z.ai/api/paas/v4/
OPENAI_MODEL=glm-4.7-flash
LLM_MAX_TOKENS=1024
LOOPBACK_DEVICE=CABLE Input (VB-Audio Virtual Cable)
OVERLAY_WIDTH=800
OVERLAY_HEIGHT=650
OVERLAY_MAX_LINES=10
SAVE_AUDIO=false
```

---

## Graceful Shutdown

1. SIGINT/SIGTERM → ctx cancel
2. capture.Close() → malgo устройства остановлены, audioCh закрыт
3. Gladia.Stop() → ctx cancel → writePump/readPump завершаются, WebSocket close
4. textCh закрывается → runSTT завершается → textStream закрывается
5. dispatch: textStream close → dispatchDone сигнал
6. shutdown ждёт dispatchDone
7. ui: WaitShutdown()
8. logger.Close() → flush
9. os.Exit(0)

**Упрощение относительно v3:** нет ожидания стриминг-горутин (streamDone). Только dispatchDone от dispatch.

---

## Стек технологий

| Слой | Библиотека | Назначение |
|---|---|---|
| Audio Capture | `malgo` (CGo) | WASAPI Loopback + Mic |
| STT + Перевод | Gladia Solaria-1 `/v2/live` | Turn-aware streaming + встроенный перевод |
| WebSocket | `gorilla/websocket` | Gladia WebSocket клиент |
| LLM | `go-openai` → GLM-4.7-Flash | Генерация подсказок (стриминг) |
| UI | `gioui.org` v0.10.1 | Прозрачный четырёхзонный overlay |
| Win32 | `golang.org/x/sys/windows` | WS_EX_TOPMOST/LAYERED |
| Config | `joho/godotenv` + `gopkg.in/yaml.v3` | .env + config.yaml |

---

## Задержка (измеренные значения)

```
Gladia STT:       ~260ms (от речи до финального транскрипта, endpointing=0.3)
Gladia перевод:   ~200ms (от transcript до translation, встроенный)
LLM подсказки:    ~1-2s  (GenerateAnswersStream, асинхронно, не блокирует UI)
Общая (речь → перевод на экране): ~460ms
```

---

## Интеграционное тестирование

### Структура тестов

```
test/
  cable_test/          проверка VB-Cable (CGO, список устройств, 5s захвата)
  llm_test/            тест LLM-подсказок (GenerateAnswers + GenerateAnswersStream)
  gladia_test/         интеграционный тест Gladia (STT + перевод на реальном API)
generate_test_wav.py   генерация тестового WAV-файла (Edge TTS / fallback tone)
```

### Интеграционный тест Gladia (`test/gladia_test`)

**Что проверяет:**
1. WebSocket-подключение к Gladia API v2 (двухфазный коннект)
2. Отправка PCM-аудио чанками по 20ms (640 байт)
3. Получение и разбор событий: interim, final transcript, translation
4. Генерацию подсказок через LLM (GLM-4.7-Flash)

**Запуск:**
```bash
# 1. Сгенерировать тестовый WAV с речью
python generate_test_wav.py

# 2. Запустить интеграционный тест
go run ./test/gladia_test
```

**Результаты (прогон 2026-07-25):**
- ✅ Gladia WebSocket — коннект успешен
- ✅ STT (Solaria-1) — распознавание речи работает
- ✅ Gladia Translation (enhanced, EN→RU) — перевод работает
- ✅ LLM (GLM-4.7-Flash) — подсказки генерируются
- ✅ `go vet ./...` — чисто
- ✅ `go test -race ./...` — 8/8 PASS, гонок нет

### Генератор тестового WAV (`generate_test_wav.py`)

Генерирует `test_speech.wav` в корне проекта:
- **Основной режим:** Edge TTS (Windows built-in) — синтезирует фразу *«Hello, could you explain what a deadlock is and how to avoid it in Go programming language?»*
- **Fallback:** синусоида 440Hz с fade in/out — если Edge TTS недоступен
- Формат: WAV, 16kHz, mono, 16-bit PCM
- Длительность: ~3 секунды
- Требования: Python 3, Windows (для Edge TTS через PowerShell)

### Ручное тестирование LLM (`test/llm_test`)

```bash
go run ./test/llm_test
```

Проверяет:
- `GenerateAnswers` (batch) — вопрос про mutex vs channel
- `GenerateAnswersStream` (SSE-стриминг) — вопрос про CAP theorem
- Выводит замеры времени и токены

### Проверка аудиоустройств (`test/cable_test`)

```bash
go run ./test/cable_test
```

Выводит все loopback/capture устройства с пометкой ★ для VB-Cable. Требует CGO.

---

## Финальный статус v4

| Компонент | Статус | Примечание |
|-----------|--------|------------|
| Gladia STT (Solaria-1) | ✅ | Двухфазный коннект, стриминг PCM |
| Gladia Translation (enhanced) | ✅ | Встроенный, EN→RU, ~200ms |
| LLM (GLM-4.7-Flash) | ✅ | Только подсказки, thinking enabled (default) |
| Pipeline | ✅ | 9 горутин, синхронный dispatch |
| GioUI | ✅ | Четыре зоны: interim / перевод / речь / подсказки |
| VB-Cable | ✅ | Loopback (собеседник) + Mic (свой голос) |
| Graceful shutdown | ✅ | ctx cancel → drain channels → flush logs |
| go vet | ✅ | Чисто |
| go test -race | ✅ | 8/8 PASS |
| Интеграционный тест Gladia | ✅ | STT + перевод + LLM — всё работает |
