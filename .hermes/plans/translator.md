# План реализации Translator

**Создан:** 2026-07-21
**Последнее обновление:** 2026-07-21

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

---

## Этап 0: Структура и конфигурация

**Файлы:**
- `go.mod` — модуль `github.com/mastererik/translator`
- `cmd/app/main.go` — заглушка entry point
- `internal/common/config.go` — Config struct, загрузка из env + YAML
- `internal/common/events.go` — STTEvent, UIEvent

**Критерии приёмки:**
- [ ] `go build ./...` — без ошибок
- [ ] `go vet ./...` — без предупреждений
- [ ] Все типы имеют godoc-комментарии
- [ ] Config загружается из env vars (DEEPGRAM_API_KEY, OPENAI_API_KEY)
- [ ] config.yaml парсится корректно (с дефолтами)

**Тесты:**
- [ ] `config_test.go`: парсинг YAML, дефолтные значения, env override
- [ ] `events_test.go`: создание событий, временные метки

---

## Этап 1: Интерфейсы и общие типы

**Файлы:**
- `internal/stt/provider.go` — STTProvider interface + STTEvent
- `internal/translator/provider.go` — LLMProvider interface
- `internal/logger/provider.go` — SessionLogger interface

**Критерии приёмки:**
- [ ] Все три интерфейса определены
- [ ] Интерфейсы узкие (minimal method surface)
- [ ] Документированы godoc
- [ ] `go build ./...` проходит

**Тесты:**
- [ ] `provider_test.go`: компиляционная проверка (var _ STTProvider = &MockProvider{})

---

## Этап 2: Двухканальный захват аудио (`internal/capture`)

**Файлы:**
- `internal/capture/capture.go` — Capture struct, открытие двух malgo устройств
- `internal/capture/resampler.go` — 48kHz Stereo → 16kHz Mono
- `internal/capture/capture_test.go` — unit tests
- `internal/capture/resampler_test.go` — table-driven tests

**Критерии приёмки:**
- [ ] Два устройства захвата (loopback + mic) открываются
- [ ] Аудио-буферы передаются через chan []byte
- [ ] Resampler корректно конвертирует 48kHz Stereo → 16kHz Mono
- [ ] Graceful shutdown: контекст отменён → устройства закрыты → каналы drained
- [ ] Нет утечек горутин

**Тесты:**
- [ ] `resampler_test.go`: table-driven (известные входы → ожидаемые выходы)
- [ ] `capture_test.go`: мок malgo устройства, проверка graceful shutdown
- [ ] `capture_race_test.go`: `go test -race` — запуск 2 горутин захвата, отмена контекста

---

## Этап 3: Модуль STT (`internal/stt`)

**Файлы:**
- `internal/stt/deepgram.go` — DeepgramProvider (WebSocket)
- `internal/stt/sherpa_stub.go` — SherpaOnnxProvider (заглушка)
- `internal/stt/deepgram_test.go`
- `internal/stt/sherpa_stub_test.go`

**Критерии приёмки:**
- [ ] DeepgramProvider реализует STTProvider
- [ ] WebSocket-соединение с Deepgram устанавливается
- [ ] interim_results отправляются немедленно
- [ ] is_final == true проходит в Translator/Logger
- [ ] Reconnect с exponential backoff при обрыве
- [ ] SherpaOnnxProvider — чистая заглушка, удовлетворяет STTProvider
- [ ] Замена Deepgram → Sherpa только в main.go

**Тесты:**
- [ ] `deepgram_test.go`: мок WebSocket сервера, проверка interim/final событий
- [ ] `sherpa_stub_test.go`: проверка что заглушка удовлетворяет интерфейсу
- [ ] `deepgram_race_test.go`: конкуррентная отправка аудио + чтение текста

---

## Этап 4: Переводчик и подсказки (`internal/translator`)

**Файлы:**
- `internal/translator/openai.go` — OpenAIProvider (GPT-4o-mini)
- `internal/translator/engine.go` — TranslationEngine: sliding window + classification
- `internal/translator/prompts.go` — системные промпты
- `internal/translator/openai_test.go`
- `internal/translator/engine_test.go`

**Критерии приёмки:**
- [ ] OpenAIProvider реализует LLMProvider
- [ ] Перевод с сохранением IT-терминов (Deadlock, CQRS, Kubernetes...)
- [ ] Sliding window: последние 3–5 фраз в контексте
- [ ] Детекция вопроса → параллельная генерация 2–3 тезисов
- [ ] Ретраи при 429 (rate limit) с backoff

**Тесты:**
- [ ] `openai_test.go`: мок OpenAI API, проверка перевода и генерации ответов
- [ ] `engine_test.go`: sliding window, классификация вопросов
- [ ] `prompts_test.go`: проверка наличия ключевых инструкций в промптах
- [ ] `engine_race_test.go`: конкуррентный вызов Translate + GenerateAnswers

---

## Этап 5: UI оверлей (`internal/ui`)

**Файлы:**
- `internal/ui/overlay.go` — GioUI two-zone layout
- `internal/ui/window.go` — Win32: WS_EX_TOPMOST, WS_EX_LAYERED, WS_EX_NOACTIVATE
- `internal/ui/overlay_test.go`

**Критерии приёмки:**
- [ ] Окно поверх всех окон (WS_EX_TOPMOST)
- [ ] Прозрачный фон, клики проходят сквозь (WS_EX_LAYERED)
- [ ] Верхняя зона: живой перевод
- [ ] Нижняя зона: нумерованные подсказки (1, 2, 3)
- [ ] Крупный читаемый шрифт
- [ ] Нет блокировок UI event loop

**Тесты:**
- [ ] `overlay_test.go`: проверка layout, рендеринг текста
- [ ] `window_test.go`: проверка Win32 флагов

---

## Этап 6: Логирование (`internal/logger`)

**Файлы:**
- `internal/logger/session.go` — FileSessionLogger: запись JSON + PCM
- `internal/logger/session_test.go`

**Критерии приёмки:**
- [ ] Запись STTEvent в `session_YYYY-MM-DD_HH-MM.json`
- [ ] Асинхронное сохранение PCM на диск (loopback + mic)
- [ ] Неблокирующая запись (буферизованный канал)
- [ ] Close() flush'ит все ожидающие записи
- [ ] Корректное создание директории логов

**Тесты:**
- [ ] `session_test.go`: запись событий, проверка JSON, flush
- [ ] `session_race_test.go`: конкуррентный вызов LogText + SaveAudioChunk

---

## Этап 7: Интеграция (`cmd/app/main.go`)

**Файлы:**
- `cmd/app/main.go` — wire-up всех модулей, graceful shutdown

**Критерии приёмки:**
- [ ] `go build ./...` — успешная сборка
- [ ] `go run ./cmd/app` — приложение запускается
- [ ] SIGINT → graceful shutdown, все ресурсы освобождены
- [ ] `go test -race ./...` — без data race

**Тесты:**
- [ ] `main_test.go`: интеграционный smoke-тест (запуск → shutdown)
