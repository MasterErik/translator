# План реализации Translator

**Создан:** 2026-07-21
**Последнее обновление:** 2026-07-22

## Статус

| Этап | Статус | Суб-агент |
|------|--------|-----------|
| 0. Структура и конфигурация | ✅ completed | deleg_068a0b00 |
| 1. Интерфейсы и общие типы | ✅ completed | deleg_068a0b00 |
| 2. Двухканальный захват аудио | ✅ completed | deleg_068a0b00 |
| 3. Модуль STT (Deepgram + Sherpa stub) | ✅ completed | deleg_56c0bcf7 |
| 4. Переводчик и подсказки (GPT-4o-mini) | ✅ completed | deleg_18436907 |
| 5. UI оверлей на GioUI | ✅ completed | deleg_d7de630d |
| 6. Логирование и сохранение сессий | ✅ completed | deleg_570e7b04 |
| 7. Интеграция (main.go, graceful shutdown) | ✅ completed | deleg_4491e242 |
| 8. Заключительная проверка | ✅ completed | — |

---

## Этап 0: Структура и конфигурация

**Файлы:**
- `go.mod` — модуль `github.com/mastererik/translator`
- `cmd/app/main.go` — заглушка entry point
- `internal/common/config.go` — Config struct, загрузка из env + YAML
- `internal/common/events.go` — STTEvent, UIEvent

**Критерии приёмки:**
- [x] `go build ./...` — без ошибок
- [x] `go vet ./...` — без предупреждений
- [x] Все типы имеют godoc-комментарии
- [x] Config загружается из env vars (DEEPGRAM_API_KEY, OPENAI_API_KEY)
- [x] config.yaml парсится корректно (с дефолтами)

**Тесты:**
- [x] `config_test.go`: парсинг YAML, дефолтные значения, env override
- [x] `events_test.go`: создание событий, временные метки

---

## Этап 1: Интерфейсы и общие типы

**Файлы:**
- `internal/stt/provider.go` — STTProvider interface + STTEvent
- `internal/translator/provider.go` — LLMProvider interface
- `internal/logger/provider.go` — SessionLogger interface

**Критерии приёмки:**
- [x] Все три интерфейса определены
- [x] Интерфейсы узкие (minimal method surface)
- [x] Документированы godoc
- [x] `go build ./...` проходит

**Тесты:**
- [x] `provider_test.go`: компиляционная проверка (var _ STTProvider = &MockProvider{})

---

## Этап 2: Двухканальный захват аудио (`internal/capture`)

**Файлы:**
- `internal/capture/capture.go` — Capture struct, открытие двух malgo устройств
- `internal/capture/resampler.go` — 48kHz Stereo → 16kHz Mono
- `internal/capture/capture_test.go` — unit tests
- `internal/capture/resampler_test.go` — table-driven tests

**Критерии приёмки:**
- [x] Два устройства захвата (loopback + mic) открываются
- [x] Аудио-буферы передаются через chan []byte
- [x] Resampler корректно конвертирует 48kHz Stereo → 16kHz Mono
- [x] Graceful shutdown: контекст отменён → устройства закрыты → каналы drained
- [x] Нет утечек горутин

**Тесты:**
- [x] `resampler_test.go`: table-driven (известные входы → ожидаемые выходы)
- [x] `capture_test.go`: мок malgo устройства, проверка graceful shutdown
- [x] `capture_race_test.go`: `go test -race` — запуск 2 горутин захвата, отмена контекста

---

## Этап 3: Модуль STT (`internal/stt`)

**Файлы:**
- `internal/stt/deepgram.go` — DeepgramProvider (WebSocket)
- `internal/stt/sherpa_stub.go` — SherpaOnnxProvider (заглушка)
- `internal/stt/deepgram_test.go`
- `internal/stt/sherpa_stub_test.go`

**Критерии приёмки:**
- [x] DeepgramProvider реализует STTProvider
- [x] WebSocket-соединение с Deepgram устанавливается
- [x] interim_results отправляются немедленно
- [x] is_final == true проходит в Translator/Logger
- [x] Reconnect с exponential backoff при обрыве
- [x] SherpaOnnxProvider — чистая заглушка, удовлетворяет STTProvider
- [x] Замена Deepgram → Sherpa только в main.go

**Тесты:**
- [x] `deepgram_test.go`: мок WebSocket сервера, проверка interim/final событий
- [x] `sherpa_stub_test.go`: проверка что заглушка удовлетворяет интерфейсу
- [x] `deepgram_race_test.go`: конкуррентная отправка аудио + чтение текста

---

## Этап 4: Переводчик и подсказки (`internal/translator`)

**Файлы:**
- `internal/translator/openai.go` — OpenAIProvider (GPT-4o-mini)
- `internal/translator/engine.go` — TranslationEngine: sliding window + classification
- `internal/translator/prompts.go` — системные промпты
- `internal/translator/openai_test.go`
- `internal/translator/engine_test.go`

**Критерии приёмки:**
- [x] OpenAIProvider реализует LLMProvider
- [x] Перевод с сохранением IT-терминов (Deadlock, CQRS, Kubernetes...)
- [x] Sliding window: последние 3–5 фраз в контексте
- [x] Детекция вопроса → параллельная генерация 2–3 тезисов
- [x] Ретраи при 429 (rate limit) с backoff

**Тесты:**
- [x] `openai_test.go`: мок OpenAI API, проверка перевода и генерации ответов
- [x] `engine_test.go`: sliding window, классификация вопросов
- [x] `prompts_test.go`: проверка наличия ключевых инструкций в промптах
- [x] `engine_race_test.go`: конкуррентный вызов Translate + GenerateAnswers

---

## Этап 5: UI оверлей (`internal/ui`)

**Файлы:**
- `internal/ui/overlay.go` — GioUI two-zone layout
- `internal/ui/window.go` — Win32: WS_EX_TOPMOST, WS_EX_LAYERED, WS_EX_NOACTIVATE
- `internal/ui/overlay_test.go`

**Критерии приёмки:**
- [x] Окно поверх всех окон (WS_EX_TOPMOST)
- [x] Прозрачный фон, клики проходят сквозь (WS_EX_LAYERED)
- [x] Верхняя зона: живой перевод
- [x] Нижняя зона: нумерованные подсказки (1, 2, 3)
- [x] Крупный читаемый шрифт
- [x] Нет блокировок UI event loop

**Тесты:**
- [x] `overlay_test.go`: проверка layout, рендеринг текста
- [x] `window_test.go`: проверка Win32 флагов

---

## Этап 6: Логирование (`internal/logger`)

**Файлы:**
- `internal/logger/session.go` — FileSessionLogger: запись JSON + PCM
- `internal/logger/session_test.go`

**Критерии приёмки:**
- [x] Запись STTEvent в `session_YYYY-MM-DD_HH-MM.json`
- [x] Асинхронное сохранение PCM на диск (loopback + mic)
- [x] Неблокирующая запись (буферизованный канал)
- [x] Close() flush'ит все ожидающие записи
- [x] Корректное создание директории логов

**Тесты:**
- [x] `session_test.go`: запись событий, проверка JSON, flush
- [x] `session_race_test.go`: конкуррентный вызов LogText + SaveAudioChunk

---

## Этап 7: Интеграция (`cmd/app/main.go`)

**Файлы:**
- `cmd/app/main.go` — wire-up всех модулей, graceful shutdown

**Критерии приёмки:**
- [x] `go build ./...` — успешная сборка
- [x] `go run ./cmd/app` — приложение запускается
- [x] SIGINT → graceful shutdown, все ресурсы освобождены
- [x] `go test -race ./...` — без data race

**Тесты:**
- [x] `main_test.go`: интеграционный smoke-тест (запуск → shutdown)

---

## Этап 8: Заключительная проверка (2026-07-22)

### 8.1 Исправление data race
- [x] `engine.go` — TranslationResult получил `answersMu sync.Mutex` + `GetAnswers()`/`SetAnswers()`
- [x] `logger/session.go` — мьютекс удерживается на всё время send в канал (Close не может закрыть writeCh во время send)
- [x] `main_test.go` — mockLLMProvider получил `sync.Mutex` на Translate и GenerateAnswers
- [x] `go test -race ./...` — ноль data race (все 8 пакетов)

### 8.2 Расширенное логирование
- [x] `engine.go` — логи перевода (`slog.Info` текст→перевод, is_question) и подсказок (старт/успех/ошибка)
- [x] `app.go` — логи промежуточных транскриптов (`slog.Debug`), таймаутов подсказок
- [x] `main.go` — лог выбранных устройств (`loopback_device`, `mic_device`)

### 8.3 Виртуальный аудиоканал VB-Cable
- [x] `ARCHITECTURE.md` обновлён: схема VB-Cable, CABLE Input = loopback, CABLE Output = capture
- [x] `config.yaml` — исправлен комментарий с правильным именем устройства
- [x] `cmd/cable_test/main.go` — новый инструмент проверки VB-Cable (перечисление + пробный захват)
- [x] VB-Cable проверен: CABLE Input найден, захват 250 кадров за 5 секунд — OK
- [x] `references/audio-device-troubleshooting.md` — обновлены имена устройств

### 8.4 Багфиксы
- [x] `capture.go` — `startLoopback`/`startMic` параметр `chan<- []byte` → `chan []byte` (совместимость с `monitorShutdown`)
- [x] `main.go` — хардкод `2`→`4` для `malgo.Loopback`, `1`→`2` для `malgo.Capture`

### 8.5 Интеграционное тестирование
- [x] `manual_test` — STT Deepgram транскрибирует, GPT-4o-mini переводит с сохранением IT-терминов
- [x] `translator.exe` с `LOOPBACK_DEVICE=CABLE Input` — стартует, валидация OK, PCM пишется
- [x] `go vet ./...` — чисто
- [x] `go test -race -count=1 ./...` — 8/8 пакетов pass

### Итог
- Все 7 этапов плана + заключительная проверка выполнены
- Data race: 0
- Тестов: 92 pass
- VB-Cable: проверен и работает
- Логирование: все ключевые точки покрыты slog
