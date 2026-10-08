# Потоковая схема STT Gladia Solaria-1

**Статус:** ✅ потоковый (STT + встроенный перевод)
**Протокол:** WebSocket (двухфазный коннект через Gladia Live API v2)
**Модель STT:** `solaria-1`
**Модель перевода:** `enhanced`
**Параметры:** `encoding=wav/pcm&sample_rate=16000&bit_depth=16&channels=1&endpointing=0.3`

## Двухфазный коннект

```
┌─────────────────────────────────────────────────────────────────────────┐
│                     ФАЗА 1: ИНИЦИАЛИЗАЦИЯ СЕССИИ                        │
│                                                                         │
│  GladiaProvider.Start()                                                 │
│       │                                                                 │
│       ▼                                                                 │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │ POST https://api.gladia.io/v2/live                              │   │
│  │                                                                   │   │
│  │ Headers:                                                          │   │
│  │   x-gladia-key: <GLADIA_API_KEY>                                 │   │
│  │   Content-Type: application/json                                 │   │
│  │                                                                   │   │
│  │ Body:                                                             │   │
│  │   encoding: "wav/pcm"                                             │   │
│  │   bit_depth: 16                                                   │   │
│  │   sample_rate: 16000                                             │   │
│  │   channels: 1                                                     │   │
│  │   model: "solaria-1"                                              │   │
│  │   endpointing: 0.3                                                │   │
│  │   language_config:                                                │   │
│  │     languages: ["en"]                                             │   │
│  │     code_switching: false                                         │   │
│  │   realtime_processing:                                            │   │
│  │     translation: true                                             │   │
│  │     translation_config:                                           │   │
│  │       target_languages: ["ru"]                                    │   │
│  │       model: "enhanced"                                           │   │
│  │   messages_config:                                                │   │
│  │     receive_partial_transcripts: true                             │   │
│  │     receive_final_transcripts: true                               │   │
│  │     receive_realtime_processing_events: true                      │   │
│  └──────────────────────┬──────────────────────────────────────────┘   │
│                         │                                               │
│                         ▼ 201 Created                                   │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │ Response:                                                        │   │
│  │   {                                                              │   │
│  │     "id": "session-uuid-...",                                    │   │
│  │     "url": "wss://api.gladia.io/v2/live/session-uuid-..."       │   │
│  │   }                                                              │   │
│  └──────────────────────┬──────────────────────────────────────────┘   │
│                         │                                               │
└─────────────────────────┼───────────────────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                     ФАЗА 2: WEBSOCKET СТРИМИНГ                          │
│                                                                         │
│  ┌──────────┐    ┌──────────┐    ┌──────────────┐    ┌──────────────┐  │
│  │ CAPTURE  │    │ WRITE    │    │  WEBSOCKET   │    │ READ         │  │
│  │ аудио    │───▶│ PUMP     │───▶│  wss://       │───▶│ PUMP         │  │
│  │ 80ms PCM │    │ горутина │    │  api.gladia   │    │ горутина     │  │
│  │ 16kHz    │    │          │    │  .io/v2/live/ │    │              │  │
│  │ mono     │    │ Binary-  │    │  <session-id> │    │              │  │
│  │          │    │ Message  │    │               │    │              │  │
│  └──────────┘    └──────────┘    └──────────────┘    └──────┬───────┘  │
│                                                             │          │
│                                              ┌──────────────┘          │
│                                              ▼                         │
│                                    ┌──────────────────┐               │
│                                    │ parseAndEmit()   │               │
│                                    │                  │               │
│                                    │ JSON-сообщения:  │               │
│                                    │   ├─ transcript  │               │
│                                    │   │   is_final=  │               │
│                                    │   │    false/true│               │
│                                    │   └─ translation │               │
│                                    └──────┬───────────┘               │
│                                           │                           │
└───────────────────────────────────────────┼───────────────────────────┘
                                            │
                                            ▼
                              ┌─────────────────────────┐
                              │      textCh (chan)       │
                              │      буфер 32            │
                              └───────────┬─────────────┘
                                          │
                                          ▼
                              ┌─────────────────────────┐
                              │       runSTT            │
                              │  routeSTTEvent()        │
                              │                         │
                              │  is_final?              │
                              │   ├─ false → неблок.    │
                              │   │          textStream │
                              │   └─ true  → блокир.    │
                              │              textStream │
                              └───────────┬─────────────┘
                                          │
                                          ▼
                              ┌─────────────────────────┐
                              │   textStream (chan)      │
                              │   буфер 64               │
                              └───────────┬─────────────┘
                                          │
                                          ▼
                              ┌─────────────────────────┐
                              │       dispatch          │
                              │                         │
                              │  ChannelID=="translation"│
                              │   → UI Translation      │
                              │                         │
                              │  Event=="Update"        │
                              │   → UI Interim          │
                              │                         │
                              │  Event=="EndOfTurn"     │
                              │   → UI History          │
                              │   → IsQuestion?         │
                              │    generateAnswers      │
                              └─────────────────────────┘
```

## Типы событий Gladia

| Тип | Поле `type` | Условие | STTEvent.Event | ChannelID | Действие |
|---|---|---|---|---|---|
| Промежуточный транскрипт | `transcript` | `is_final=false` | `Update` | `speaker` | UI Interim (серый) |
| Финальный транскрипт | `transcript` | `is_final=true` | `EndOfTurn` | `speaker` | Сохраняется в lastOriginal, UI History |
| Перевод | `translation` | — | `EndOfTurn` | `translation` | UI Translation + пара в UI History |
| metadata / error | `metadata`, `error` | — | — | — | Игнорируются |

## Структуры JSON

### transcript (is_final=true)
```json
{
  "type": "transcript",
  "data": {
    "id": "utterance-uuid",
    "is_final": true,
    "utterance": {
      "text": "I have five years of experience",
      "language": "en"
    }
  }
}
```

### translation
```json
{
  "type": "translation",
  "data": {
    "utterance_id": "utterance-uuid",
    "utterance": {
      "text": "I have five years of experience",
      "language": "en"
    },
    "translated_utterance": {
      "text": "У меня пять лет опыта",
      "language": "ru"
    }
  }
}
```

## Связывание transcript ↔ translation

Gladia гарантирует порядок: **transcript (final) → translation**. Dispatch использует `lastOriginal` — последний финальный транскрипт, сохранённый при `EventEndOfTurn`. При получении `translation` пара (оригинал + перевод) выводится в историю.

```
transcript is_final=true → lastOriginal = event
                            ↓
translation → UI Translation + UI History(lastOriginal.Text, translation.Text)
```

## PCM-фреймы

| Параметр | Значение |
|---|---|
| Длительность | **80ms** (`defaultBufferSizeMs = 80` в `internal/capture/types.go`) |
| Размер | 2560 байт (1280 сэмплов × 2 байта int16) |
| Частота | 16000 Hz, mono, 16-bit |
| Буфер audioCh | 64 фрейма (~5 сек) |
| При переполнении | Дроп старых фреймов + rate-limited Warn (1 раз в 10s) |

## Характеристики

| Параметр | Значение |
|---|---|
| Аудио-фрейм | 80ms = 1280 сэмплов × 2 байта = 2560 байт |
| Буфер audioCh | 64 фрейма (~5 сек) |
| Буфер textCh | 32 события |
| Буфер textStream | 64 события |
| Задержка STT | ~260ms (endpointing=0.3) |
| Задержка перевода | ~200ms (встроенный, enhanced) |
| Таймаут HTTP (init) | 10s |
| Таймаут WS dial | 15s |
| Таймаут WS write | 5s |

## Ключевые горутины

| Горутина | Роль |
|---|---|
| `supervise` | владеет парой pump'ов и автореконнектом; перезапускает пару при обрыве |
| `writePump` | audioCh → WebSocket (BinaryMessage, PCM); выходит по connClosed |
| `readPump` | WebSocket → parseAndEmit → textCh (JSON-события) |
| `runSTT` (routeSTTEvent) | textCh → textStream с разделением interim/final/translation |

## Reconnect (exponential backoff, supervisor)

При обрыве WebSocket (сеть/таймаут Gladia) supervisor-горутина внутри
`GladiaProvider` (не в pipeline — контракт `STTProvider` неизменен)
автоматически переподключается: полный двухфазный коннект заново
(`initSession` POST /v2/live → dial нового WS URL), параметры сессии
идемпотентны.

```
Состояния:  CONNECTED → RECONNECTING → CONNECTED
                                    → FAILED → EventError (UI зона 3)

Попытка 1: 1s + jitter (±25%)
Попытка 2: 2s + jitter
Попытка 3: 4s + jitter
Попытка 4: 8s + jitter
Попытка 5: 16s + jitter         — исчерпаны → EventError
```

Каждый цикл реконнекта (в т.ч. первый `Start`) владеет своей парой
pump'ов; `wsClosedCycle` (per-cycle CAS) + канал `connClosed` дают
синхронное завершение пары при обрыве (writePump просыпается немедленно,
не дожидаясь аудио-фрейма). `Stop()`/отмена ctx в любой точке — выход
без реконнекта; Stop сначала дожидается supervisor (`supDone`), потом
`pumpWg.Wait()` — дренаж без висящих горутин.

Тест: `internal/stt/gladia_reconnect_test.go` — fake WS-сервер
(httptest + gorilla/websocket), реальный обрыв TCP → реальное
переподключение; Stop во время реконнекта; исчерпание попыток.

| Параметр | Значение |
|---|---|
| Начальная задержка | `gladiaReconnectInitial` = 1s |
| Множитель | `gladiaReconnectFactor` = 2.0 |
| Cap | `gladiaReconnectMax` = 60s |
| Jitter | `±gladiaReconnectJitter` = 25% |
| Макс. попыток | `gladiaMaxReconnectAttempts` = 5 (const) |
| Действие при успехе | Счётчик попыток сброшен; аудио между попытками не буферизуется |
| Действие при провале | `STTEvent{Event: EventError}` через `g.emit` → UI зона 3 |
