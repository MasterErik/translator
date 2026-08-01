# План: Диагностика пустых ответов LLM (подсказки не появляются) — ВЫПОЛНЕН

## Проблема

После добавления очереди `answerWorker` подсказки перестали появляться.
Лог `session_2026-07-31_22-47-53.csv` показывает:
- Очередь работает, запросы к LLM идут
- Ошибок 429 больше нет
- Все ответы: `"dispatcher: подсказки пусты"` — LLM возвращает ответ без ошибки, но `parseAnswerHints` даёт 0 строк

## Гипотеза — ПОДТВЕРЖДЕНА

GLM-4.7-Flash с `thinking: enabled` (по умолчанию) возвращает content в `reasoning_content`,
а реальный ответ в `content` — пустой. Либо формат ответа API изменился.

## Шаги

### Шаг 1 — Интеграционный тест `openai_integration_test.go` ✅

Создан файл `internal/translator/openai_integration_test.go`:
- Билд-тег: `//go:build integration`
- Читает `.env`: `LLM_API_KEY`, `LLM_BASE_URL`, модель из `config.yaml`
- Создаёт `OpenAIProvider` с thinking enabled и disabled
- Вызывает `GenerateAnswers` для каждого
- Выводит сырой `content` через `t.Log`
- Падает если оба варианта возвращают пустой ответ

### Шаг 2 — Запустить тест, получить сырой ответ ✅

```bash
go test -tags=integration -v -run TestIntegration ./internal/translator/...
```

Определено:
- При thinking=enabled контент в `reasoning_content`, `content` пустой
- При thinking=disabled контент в `content` как обычно
- Подтверждена гипотеза

### Шаг 3 — Исправить причину ✅

Внесены изменения:
- `internal/pipeline/pipeline.go` — `llmProv.DisableThinking()` при инициализации
- `internal/translator/openai.go` — fallback на `reasoning_content` в обоих путях (go-openai и raw HTTP)

### Шаг 4 — Добить покрытие тестами dispatcher до ≥90% ✅

Было: 86.7% → Стало: **99.1%**

| Функция | Было | Стало |
|---|---|---|
| `New` | 84.6% | 92.3% |
| `route` | 90.5% | 100.0% |
| `answerWorker` | 92.9% | 100.0% |
| `drainQueue` | 37.5% | 100.0% |
| `GenerateAnswers` | 68.8% | 100.0% |

Добавлено 8 тестов:
- ✅ 4.1 `TestDispatcherGenerateAnswersError`
- ✅ 4.2 `TestDispatcherGenerateAnswersNilOverlay`
- ✅ 4.3 `TestDispatcherEmptyAnswerNoLogger`
- ✅ 4.4 `TestDispatcherAnswerWorkerChannelClose`
- ✅ 4.5 `TestDispatcherDrainQueueWithDupes`
- ✅ 4.6 `TestDispatcherDrainQueueChannelClose`
- ✅ 4.7 `TestDispatcherNewDefaultQueueSize`
- ✅ 4.8 `TestDispatcherLogTranslationError`

## Результат

```
go vet ./internal/...     → OK (чисто)
go test ./internal/dispatcher/... → PASS, coverage: 99.1%
go test ./internal/translator/... → PASS
```
