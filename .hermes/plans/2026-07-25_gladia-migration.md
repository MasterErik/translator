# План миграции: Deepgram + LLM-перевод → Gladia STT + Translation

> **Для Hermes:** реализовать поэтапно, каждый этап — независимый коммит. После этапов 1-3 проект должен компилироваться и проходить тесты.

**Цель:** заменить Deepgram на Gladia для STT, использовать встроенный Gladia Translation вместо LLM-перевода EN→RU, упростить архитектуру.

**Ключевое решение:** Gladia отдаёт перевод в том же WebSocket-соединении (событие `translation`). LLM остаётся только для генерации подсказок (`GenerateAnswers`) при детекции вопроса. Это убирает целый слой: `handleStreamingTranslation` + `TranslationEngine.TranslateStream`.

**Tech Stack (изменения):**
- STT: Deepgram Flux v2 → Gladia Solaria-1 (WebSocket, модель `solaria-1`)
- Перевод: LLM (GLM-4.7-Flash) → Gladia Translation (встроенный, модель `enhanced`)
- LLM: остаётся только для подсказок (GLM-4.7-Flash / Z.AI)

---

## Архитектура v4 (после миграции)

```
┌──────────────────────────────────────────────────────────────────┐
│                        MAIN GOROUTINE                            │
│  signal.NotifyContext → ctx → запуск всех горутин → ожидание Ctrl+C │
└──────────────────────────────────────────────────────────────────┘
        │          │           │            │
        ▼          ▼           ▼            ▼
┌───────────┐ ┌─────────┐ ┌──────────┐ ┌──────────┐
│ CAPTURE   │ │ CAPTURE  │ │ STT+TR   │ │ UI       │
│ Loopback  │ │ Mic      │ │ Gladia   │ │ GioUI    │
│ 80ms      │ │ 80ms     │ │ WS send  │ │ overlay  │
│ malgo →   │ │ malgo →  │ │ WS recv  │ │ event    │
│ chan      │ │ chan     │ │          │ │ loop     │
│ speaker   │ │ mic      │ │          │ │          │
└─────┬─────┘ └────┬─────┘ └────┬─────┘ └──────────┘
      │            │            │
      └─────┬──────┘            │ chan STTEvent (+Translation)
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
    │ (опционально) │   │ transcript(final) │
    │               │   │  → UI (Interim)   │
    │ PCM → MP3/WAV │   │ translation       │
    │ (go-lame/beep)│   │  → UI (Перевод)   │
    └───────────────┘   │ isQuestion?       │
                        │  → GenerateAnswers│
                        │  → UI (Подсказки) │
                        └───────────────────┘
```

**Ключевые отличия от v3:**
1. Один WebSocket к Gladia вместо Deepgram — и STT, и перевод в одном соединении
2. Нет стриминг-горутин для перевода — перевод приходит готовым от Gladia
3. Нет `TranslationEngine.TranslateStream` — только `GenerateAnswersStream` для подсказок
4. `dispatch` упрощается: `transcript.is_final=true` → показываем в UI; `translation` → показываем перевод
5. LLM вызывается только при `isQuestion()` — для генерации подсказок

---

## Этап 0: Аудит и зачистка (подготовка)

### Задача 0.1: Удалить SherpaOnnxProvider
**Файлы:**
- Удалить: `internal/stt/sherpa_stub.go`
- Удалить: `internal/stt/sherpa_stub_test.go`
- Модифицировать: `internal/stt/provider.go` (убрать упоминание SherpaOnnxProvider из godoc)

**Причина:** stub, нигде не используется в production, `Start()` всегда возвращает ошибку. Не нужен.

### Задача 0.2: Обновить godoc в STTProvider
**Файл:** `internal/stt/provider.go`
- Заменить `"SherpaOnnxProvider (future)"` на `"GladiaProvider"` в комментарии

---

## Этап 1: GladiaProvider — ядро STT + Translation

### Задача 1.1: Создать `internal/stt/gladia.go`
**Новый файл:** `internal/stt/gladia.go`

**Двухфазный коннект Gladia:**
1. `POST https://api.gladia.io/v2/live` → `{id, url}` (wss:// URL с токеном)
2. WebSocket dial → `url`
3. При дисконнекте — reconnect на тот же URL (сессия сохраняется)

**Конфигурация init (JSON body):**
```json
{
  "encoding": "wav/pcm",
  "bit_depth": 16,
  "sample_rate": 16000,
  "channels": 1,
  "model": "solaria-1",
  "endpointing": 300,
  "language_config": {
    "languages": ["en"],
    "code_switching": false
  },
  "realtime_processing": {
    "translation": true,
    "translation_config": {
      "target_languages": ["ru"],
      "model": "enhanced"
    }
  },
  "messages_config": {
    "receive_partial_transcripts": true,
    "receive_final_transcripts": true,
    "receive_realtime_processing_events": true
  }
}
```

**Структура `GladiaProvider`:**
```go
type GladiaProvider struct {
    apiKey      string
    wsURL       string   // получен из init
    wsConn      *websocket.Conn
    audioCh     chan []byte
    textCh      chan common.STTEvent
    mu          sync.Mutex
    ctx         context.Context
    cancel      context.CancelFunc
    model       string   // "solaria-1"
    targetLang  string   // "ru"
}
```

**Методы (реализуют `STTProvider`):**
- `NewGladiaProvider(apiKey, model, targetLang string) *GladiaProvider`
- `Start(ctx)` → POST init → получить URL → dial WebSocket → запустить writePump + readPump
- `Stop()` → cancel ctx → close WS
- `AudioStream() chan<- []byte`
- `TextStream() <-chan common.STTEvent`

**Вспомогательные:**
- `initSession()` → `POST /v2/live` → возвращает `{id, url}`
- `writePump()` → читает audioCh, пишет бинарные фреймы в WS
- `readPump()` → читает JSON из WS, парсит события

### Задача 1.2: Парсинг событий Gladia в `gladia.go`
**JSON-события от Gladia (WebSocket):**

**Transcript (interim + final):**
```json
{
  "type": "transcript",
  "data": {
    "id": "00-00000011",
    "is_final": true,
    "utterance": {
      "text": "Hello world.",
      "start": 0, "end": 0.48,
      "confidence": 0.91,
      "language": "en"
    }
  }
}
```

**Translation:**
```json
{
  "type": "translation",
  "data": {
    "utterance_id": "utt_001",
    "utterance": {"text": "Hello world.", "language": "en"},
    "translated_utterance": {"text": "Привет мир.", "language": "ru"}
  }
}
```

**Маппинг на STTEvent:**
| Gladia | STTEvent |
|--------|----------|
| `transcript` + `is_final=false` | `EventUpdate` + `Text` = utterance.text |
| `transcript` + `is_final=true` | `EventEndOfTurn` + `Text` = utterance.text |
| `translation` | `EventEndOfTurn` + `Text` = translated_utterance.text, `ChannelID` = "translation" |

**Важно:** `translation` приходит ПОСЛЕ финального `transcript` и ссылается на тот же `utterance_id`. В `parseAndEmit` связываем перевод с тем же utterance.

### Задача 1.3: Тесты GladiaProvider
**Новый файл:** `internal/stt/gladia_test.go`

- `TestGladiaProvider_ImplementsSTTProvider` — compile-time interface check
- `TestGladiaProvider_InitSession` — мок HTTP-сервера, проверка POST-запроса
- `TestGladiaProvider_ParseTranscript` — парсинг JSON transcript → STTEvent
- `TestGladiaProvider_ParseTranslation` — парсинг JSON translation → STTEvent
- `TestGladiaProvider_StopIdempotent` — двойной Stop без паники

### Задача 1.4: Интеграционный тест
**Новый файл:** `test/gladia_test/gladia_test.go`

- Инициализация сессии → WebSocket connect → отправка PCM → получение transcript + translation
- Требует `GLADIA_API_KEY` в `.env`

---

## Этап 2: Адаптация Pipeline

### Задача 2.1: Обновить Pipeline.Config
**Файл:** `internal/pipeline/pipeline.go`

Заменить:
```go
DeepgramAPIKey string
DeepgramModel  string
```
На:
```go
GladiaAPIKey  string
GladiaModel   string  // default: "solaria-1"
TargetLang    string  // default: "ru"
```

### Задача 2.2: Обновить конструктор Pipeline.New()
**Файл:** `internal/pipeline/pipeline.go`

```go
sttProv := stt.NewGladiaProvider(cfg.GladiaAPIKey, cfg.GladiaModel, cfg.TargetLang)
```

### Задача 2.3: Обновить runDispatch — обработка translation
**Файл:** `internal/pipeline/pipeline.go`

Сейчас `runDispatch` различает `EventEndOfTurn` → запускает `handleStreamingTranslation`. Новая логика:

```go
for event := range p.textStream {
    switch event.Event {
    case common.EventUpdate:
        // Interim → UI (серый текст)
        overlay.AddMessage(ui.UIMessage{Type: ui.Interim, Text: event.Text})
    case common.EventEndOfTurn:
        if event.ChannelID == "translation" {
            // Перевод от Gladia → UI (зона перевода + история)
            overlay.AddMessage(ui.UIMessage{Type: ui.Translation, Text: event.Text})
            // Если оригинал был вопросом → запустить GenerateAnswers
            // (нужно запоминать последний оригинал)
        } else {
            // Оригинал → сохранить для истории, показать в UI
            overlay.AddMessage(ui.UIMessage{Type: ui.History, Text: event.Text})
            // Проверить на вопрос
            if translator.IsQuestion(event.Text) {
                go p.generateAnswersAsync(event.Text)
            }
        }
    }
}
```

### Задача 2.4: Обновить generateAnswersAsync
**Файл:** `internal/pipeline/pipeline.go`

Упрощается — больше нет перевода, только подсказки. Убрать вызов `TranslateStream`.

### Задача 2.5: Обновить shutdown
**Файл:** `internal/pipeline/pipeline.go`

Убрать `dispatchDone` (больше нет ожидания стриминг-горутин). Shutdown становится:
1. STT Stop
2. UI WaitShutdown
3. Logger Close

---

## Этап 3: Обновление main.go и конфигурации

### Задача 3.1: main.go — замена Deepgram на Gladia
**Файл:** `main.go`

- Заменить `DEEPGRAM_API_KEY` → `GLADIA_API_KEY`
- Заменить `DEEPGRAM_MODEL` → `GLADIA_MODEL`
- Добавить `TARGET_LANG` (default "ru")

### Задача 3.2: main_stub.go
**Файл:** `main_stub.go`

Аналогичные замены.

### Задача 3.3: config.yaml
**Файл:** `config.yaml`

```yaml
gladia_model: "solaria-1"
target_lang: "ru"
# Убрать: deepgram_model
# openai_model, llm_base_url — остаются для подсказок
```

### Задача 3.4: common/config.go
**Файл:** `internal/common/config.go`

- Добавить поля `GladiaAPIKey`, `GladiaModel`, `TargetLang`
- Убрать `DeepgramAPIKey`, `DeepgramModel`
- `loadFromEnv()`: читать `GLADIA_API_KEY`, `GLADIA_MODEL`, `TARGET_LANG`

---

## Этап 4: Зачистка Deepgram и LLM-перевода

### Задача 4.1: Удалить deepgram.go
**Файл:** удалить `internal/stt/deepgram.go`

### Задача 4.2: Удалить deepgram_test.go (если есть)
**Файл:** удалить тесты Deepgram

### Задача 4.3: Упростить TranslationEngine
**Файл:** `internal/translator/engine.go`

- Убрать `TranslateStream` и `Translate` (перевод теперь от Gladia)
- Оставить только `GenerateAnswers` / `GenerateAnswersStream`
- Оставить `IsQuestion`
- Оставить sliding window для контекста подсказок

### Задача 4.4: Упростить LLMProvider интерфейс
**Файл:** `internal/translator/provider.go`

```go
type LLMProvider interface {
    GenerateAnswers(ctx context.Context, question string, cvContext string) ([]string, error)
}
type StreamingAnswersProvider interface {
    LLMProvider
    GenerateAnswersStream(ctx context.Context, question string, cvContext string) (<-chan string, error)
}
```

Убрать `Translate` / `TranslateStream`.

### Задача 4.5: Обновить OpenAIProvider
**Файл:** `internal/translator/openai.go`

- Убрать метод `Translate` / `TranslateStream`
- Оставить `GenerateAnswers` / `GenerateAnswersStream`
- Оставить `thinkingTransport` для Z.AI

### Задача 4.6: Обновить моки и тесты
**Файлы:** `internal/translator/*_test.go`, `internal/pipeline/*_test.go`

- Моки должны реализовать новый урезанный интерфейс
- Обновить тесты pipeline: убрать проверки TranslateStream, добавить проверки обработки translation-событий

---

## Этап 5: Интеграционное тестирование

### Задача 5.1: Обновить интеграционные тесты
**Файл:** `test/gladia_test/gladia_test.go`

- Полный цикл: захват → Gladia STT → получение transcript + translation → UI
- Проверка EndOfTurn для оригинала И для перевода

### Задача 5.2: Ручное тестирование
- `CGO_ENABLED=1 go run .` с реальным Gladia API ключом
- Проверка: Windows → Chrome/Teams → VB-Cable → Gladia → UI с переводом
- Проверка graceful shutdown

---

## Этап 6: Документация

### Задача 6.1: Обновить ARCHITECTURE.md
- Отразить новую архитектуру с Gladia
- Обновить схему горутин (меньше на 3 стрим-горутины)

### Задача 6.2: Обновить AGENTS.md (при необходимости)
- Обновить упоминания Deepgram → Gladia

### Задача 6.3: Обновить translator skill
- Новые env-переменные: `GLADIA_API_KEY`
- Новый стек: Gladia Solaria-1
- Убрать pitfall'ы связанные с Deepgram (eot_threshold, EagerEndOfTurn, дублирующие Update)

---

## Сводка: что удаляется, что добавляется

| Удалить | Заменить на |
|---------|------------|
| `internal/stt/deepgram.go` (406 строк) | `internal/stt/gladia.go` (~250 строк) |
| `internal/stt/sherpa_stub.go` (53 строки) | — (не нужно) |
| `internal/stt/sherpa_stub_test.go` (134 строки) | `internal/stt/gladia_test.go` |
| `handleStreamingTranslation()` в pipeline | Прямая обработка `translation` событий |
| `TranslateStream`/`Translate` в translator | — (перевод от Gladia) |
| `DeepgramAPIKey`/`DeepgramModel` в Config | `GladiaAPIKey`/`GladiaModel`/`TargetLang` |

**Оценка чистой экономии кода:** ~300 строк (deepgram 406 + sherpa 187 + упрощение pipeline ~100 + упрощение translator ~100 − gladia ~250 − новые тесты ~200 = **−343 строки**)

---

## Риски

1. **Gladia endpointing vs Deepgram eot_threshold** — разное поведение. Gladia endpointing по умолчанию 0.05s — может давать больше EndOfTurn на паузах. Нужно протестировать и подобрать значение (рекомендуется 300ms для встреч).
2. **Задержка translation** — Gladia Translation приходит ПОСЛЕ финального транскрипта. Нужно замерить реальную задержку: transcript final → translation.
3. **Перевод не кастомизируемый** — Gladia Translation не использует system prompt. IT-термины могут переводиться неидеально. Если качество перевода неприемлемо — фолбэк на LLM-перевод.
4. **Gladia тарификация** — нужно проверить модель оплаты (посекундная? за перевод?). Может быть дороже чем Deepgram + LLM.
5. **Совместимость с gl=1 (один канал)** — Gladia принимает 1-8 каналов, но перевод работает поканально.
