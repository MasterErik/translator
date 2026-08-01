# Интеграционное тестирование Translator

## Структура тестов

```
test/
  cable_test/          проверка VB-Cable (CGO, список устройств, 5s захвата)
  llm_test/            тест LLM-подсказок (GenerateAnswers + GenerateAnswersStream)
  gladia_test/         интеграционный тест Gladia (STT + перевод на реальном API)
generate_test_wav.py   генерация тестового WAV-файла (Edge TTS / fallback tone)
```

## Интеграционный тест Gladia (`test/gladia_test`)

**Что проверяет:**
1. WebSocket-подключение к Gladia API v2 (двухфазный коннект: POST `/v2/live` → dial)
2. Отправка PCM-аудио фреймами по 80ms (2560 байт, 16kHz mono)
3. Получение и разбор событий: `transcript` (interim/final), `translation`
4. Генерацию подсказок через LLM (GLM-4.7-Flash, синхронный `GenerateAnswers`)

**Запуск:**
```bash
# 1. Сгенерировать тестовый WAV с речью
python generate_test_wav.py

# 2. Запустить интеграционный тест
go run ./test/gladia_test
```

**Требования к `.env`:**
- `GLADIA_API_KEY` — ключ Gladia API
- `LLM_API_KEY` — ключ LLM (Z.AI)
- `LLM_BASE_URL` — URL LLM API (по умолчанию `https://api.z.ai/api/paas/v4/`)

## Генератор тестового WAV (`generate_test_wav.py`)

Генерирует `test_speech.wav` в корне проекта:
- **Основной режим:** Edge TTS (Windows built-in) — синтезирует фразу *«Hello, could you explain what a deadlock is and how to avoid it in Go programming language?»*
- **Fallback:** синусоида 440Hz с fade in/out — если Edge TTS недоступен
- Формат: WAV, 16kHz, mono, 16-bit PCM
- Длительность: ~3 секунды
- Требования: Python 3, Windows (для Edge TTS через PowerShell)

## Ручное тестирование LLM (`test/llm_test`)

```bash
go run ./test/llm_test
```

Проверяет:
- `GenerateAnswers` (синхронный batch) — вопрос про mutex vs channel
- `GenerateAnswersStream` (SSE-стриминг) — вопрос про CAP theorem
- Выводит замеры времени и токены
- Формат ответа: `EN: <English> | RU: <Russian>`

## Проверка аудиоустройств (`test/cable_test`)

```bash
go run ./test/cable_test
```

Выводит все playback/capture устройства с пометкой ★ для VB-Cable. Требует CGO + GCC.

## Юнит-тесты пакетов

```bash
# Все тесты (без race detector — см. ограничение Windows TSan)
go test ./...

# С coverage-отчётом
go test -coverprofile=coverage.out ./internal/...
go tool cover -func=coverage.out

# Отдельные пакеты
go test -v ./internal/pipeline/...
go test -v ./internal/translator/...
go test -v ./internal/stt/...
go test -v ./internal/logger/...
go test -v ./internal/capture/...
go test -v ./internal/ui/...
go test -v ./internal/common/...
```

---

## SLA & Performance Test Constraints

Все интеграционные и юнит-тесты ДОЛЖНЫ валидировать следующие пороги:

### 1. Audio Latency

| Параметр | Значение |
|---|---|
| Размер PCM-фрейма | **80ms** (2560 байт, 16kHz Mono 16-bit) |
| Задержка захват → audioStream | MAX ≤ 10ms |
| Fallback при backpressure | Дроп фрейма, rate-limited log (1 раз в 10s) |

### 2. Gladia WebSocket SLA

| Параметр | Значение |
|---|---|
| Таймаут двухфазного коннекта (POST + Dial) | MAX ≤ 2000ms |
| Таймаут HTTP (POST /v2/live) | 10s |
| Таймаут WS Dial | 15s |
| Таймаут WS Write | 5s |
| Задержка Interim STT | MAX ≤ 500ms |
| Задержка EndOfTurn (Final transcript) | MAX ≤ 1000ms |
| Задержка Translation (после transcript) | MAX ≤ 300ms |
| Endpointing | 300ms (оптимально для встреч) |
| Reconnect | Exponential backoff (1s→2s→4s→…→64s cap, 10 попыток) |

### 3. LLM Generation SLA

| Параметр | Значение |
|---|---|
| Модель | `glm-4.7-flash` (Z.AI) |
| Time-To-First-Token (TTFT) | MAX ≤ 1200ms |
| Полная генерация (1 подсказка EN\|RU) | MAX ≤ 2500ms |
| Таймаут ответа (`AnswerTimeout`) | 10s (background context) |
| Temperature | 0.3 |
| Max Tokens | `LLM_MAX_TOKENS` (default 1024) |
| Fallback | Таймаут → контекст отменяется |

### 4. Buffer & Memory Invariants

| Канал | Тип | Буфер | ~Время буфера | При переполнении |
|---|---|---|---|---|
| `audioCh` (Gladia) | `chan []byte` | **64** | ~5 сек (80ms × 64) | Дроп старых фреймов + rate-limited Warn |
| `textCh` (Gladia) | `chan STTEvent` | **32** | — | Блокировка readPump |
| `textStream` (Pipeline) | `chan STTEvent` | **64** | — | Interim: неблок. дроп; Final: блок. отправка |
| `logCh` (mic fan-out) | `chan []byte` | **32** | ~2.5 сек | Дроп + Warn |
| `dispatchDone` | `chan struct{}` | **1** | — | Сигнал shutdown |

### 5. Общие ограничения

| Параметр | Значение |
|---|---|
| Горутины (steady-state) | 9 |
| Graceful shutdown | ≤ 3s от SIGINT до exit |
| Утечки горутин | 0 (проверка `runtime/pprof`) |
| Data races | 0 (Windows: `go test ./...` без `-race` из-за TSan) |
