# План: Tail Latency v2 — замер пользовательских задержек через Dispatcher

## Проблема

Текущий тест `test/tail_latency` читает `TextStream()` эмулятора напрямую и меряет «сырые» задержки STT-событий. Реальный production-путь проходит через Dispatcher и Overlay:

```
Emulator.TextStream() → Dispatcher.route() → Overlay.AddMessage() → UI render
```

Разница в погрешности небольшая (микросекунды на route + AddMessage), но архитектурно правильно замерять задержки на выходе Dispatcher'а — так же, как их видит пользователь в UI.

Дополнительно: сейчас нет метрики для времени появления подсказок (`AnswerCandidates`) — это четвёртая пользовательская зона.

| Что измеряем сейчас | Что должно измеряться |
|---|---|
| `first_interim` — время до первого STTEvent от эмулятора | `interim_ui` — время от отправки WAV до появления текста в зоне 1 (Interim) |
| `final` — время до EndOfTurn от эмулятора | `history_original` — время от отправки WAV до появления пары (оригинал + перевод) в зоне 3 (History) |
| `translation` — время до translation от эмулятора | `translation_ui` — время от отправки WAV до появления перевода в зоне 2 (Translation) |
| Нет | `answer_ui` — время от отправки WAV до появления подсказки в зоне 4 (AnswerCandidates) |

**Примечание:** `history_original` и `translation_ui` замеряются практически одновременно — Dispatcher отправляет `History` только вместе с `Translation` (см. `dispatcher.go:148-164`). Это отражает реальное поведение UI.

## Результаты codegraph-анализа (подтверждённые зависимости)

| Символ | Файл | Суть |
|---|---|---|
| `AnswerGenerator` | `dispatcher.go:21-23` | Интерфейс с `GenerateAnswers(ctx, question)` — 2 аргумента |
| `LLMProvider` | `translator/provider.go:13-17` | Интерфейс с `GenerateAnswers(ctx, question, cvContext)` — 3 аргумента |
| `TranslationEngine` | `translator/engine.go:59` | Адаптер: реализует `AnswerGenerator`, делегирует `LLMProvider` |
| `setupLLMEngine()` | `main.go:228` | Возвращает `dispatcher.AnswerGenerator` (через `llmAdapter`) |
| `llmAdapter` | `main.go:248-254` | Оборачивает `OpenAIProvider` → адаптирует 3-аргументный вызов в 2-аргументный |
| `Dispatcher.New()` | `dispatcher.go:63` | Принимает `AnswerGenerator` (engine) + `OverlayUI` + `SessionLogger` |
| `Dispatcher.route()` | `dispatcher.go:145` | Вызывает `overlay.AddMessage()` для всех 4 типов: Interim, Translation, History, AnswerCandidates |
| `Overlay.AddMessage()` | `ui/overlay.go:71` | Реальная имплементация — mutex + switch по типам |

**Ключевой вывод:** engine УЖЕ возвращается как `dispatcher.AnswerGenerator` — можно передавать напрямую в `Dispatcher.New()`.

**Архитектурное изменение в main.go:** сейчас LLM управляется снаружи через `llmWorker` + `llmQueue`. В новой архитектуре Dispatcher сам управляет очередью LLM (через `answerWorker`), поэтому `llmWorker`/`llmQueue`/`llmWg` из main.go удаляются.

## План исправления

### Шаг 1 — Добавить MockOverlay с временны́ми метками

Создать `test/tail_latency/mock_overlay.go` — реализация `dispatcher.OverlayUI`, которая:
- Сохраняет каждое сообщение с `time.Now()` в момент вызова `AddMessage`
- Экспортирует методы: `FirstInterimAt()`, `FirstHistoryAt()`, `FirstTranslationAt()`, `FirstAnswerAt()`

```go
package main

import (
    "sync"
    "time"

    "github.com/mastererik/translator/internal/ui"
)

type TimedMessage struct {
    ui.UIMessage
    ReceivedAt time.Time
}

type MockOverlay struct {
    mu                sync.Mutex
    messages          []TimedMessage
    firstInterim      time.Time
    firstHistory      time.Time
    firstTranslation  time.Time
    firstAnswer       time.Time
}

func (m *MockOverlay) AddMessage(msg ui.UIMessage) {
    m.mu.Lock()
    defer m.mu.Unlock()
    now := time.Now()
    m.messages = append(m.messages, TimedMessage{UIMessage: msg, ReceivedAt: now})
    switch msg.Type {
    case ui.Interim:
        if m.firstInterim.IsZero() {
            m.firstInterim = now
        }
    case ui.History:
        if m.firstHistory.IsZero() {
            m.firstHistory = now
        }
    case ui.Translation:
        if m.firstTranslation.IsZero() {
            m.firstTranslation = now
        }
    case ui.AnswerCandidates:
        if m.firstAnswer.IsZero() {
            m.firstAnswer = now
        }
    }
}

func (m *MockOverlay) FirstInterimAt() time.Time      { m.mu.Lock(); defer m.mu.Unlock(); return m.firstInterim }
func (m *MockOverlay) FirstHistoryAt() time.Time      { m.mu.Lock(); defer m.mu.Unlock(); return m.firstHistory }
func (m *MockOverlay) FirstTranslationAt() time.Time  { m.mu.Lock(); defer m.mu.Unlock(); return m.firstTranslation }
func (m *MockOverlay) FirstAnswerAt() time.Time       { m.mu.Lock(); defer m.mu.Unlock(); return m.firstAnswer }
```

### Шаг 2 — Переписать worker.go: эмулятор → Dispatcher → MockOverlay

Вместо чтения `TextStream()` напрямую, worker проходит полный production-путь:

```
1. t0 = time.Now()
2. Создать GladiaEmulator(text, translation)
3. Создать MockOverlay
4. Создать Dispatcher с реальным engine (LLM) из конфига:
   dispatcher.New(mockOverlay, engine, nil, dispatcher.DefaultConfig())
   - engine — реальный AnswerGenerator (LLM), проброшенный из main.go
   - sessLog = nil — Dispatcher безопасно работает с nil-логгером
5. Запустить Dispatcher в горутине:
   go dispatcher.Run(iterCtx, emulator.TextStream(), done)
6. Запустить эмулятор: emulator.Start(iterCtx)
7. Отправить WAV в emulator.AudioStream() чанками по 80ms (2560 байт)
8. Закрыть audioStream (close(emulator.AudioStream()) или дождаться исчерпания)
9. Дождаться закрытия textStream (или таймаута) — dispatcher.Run() закроет done
10. emulator.Stop()
11. Замерить:
    - T_interim_ui        = mockOverlay.FirstInterimAt().Sub(t0)
    - T_history_original  = mockOverlay.FirstHistoryAt().Sub(t0)
    - T_translation_ui    = mockOverlay.FirstTranslationAt().Sub(t0)
    - T_answer_ui         = mockOverlay.FirstAnswerAt().Sub(t0)
```

**Важно:** engine пробрасывается из `main.go` через `workerConfig` (поле `Engine dispatcher.AnswerGenerator`). При `--llm` это `setupLLMEngine()` (уже возвращает `AnswerGenerator`), без `--llm` — `nil` (тогда AnswerCandidates не появятся, метрика `answer_ui` будет нулевой).

### Шаг 2.5 — Упростить main.go: убрать llmWorker/llmQueue

Dispatcher сам управляет очередью LLM (через `answerWorker`/`answerCh`), поэтому внешняя очередь больше не нужна:

```go
// БЫЛО (main.go):
var llmQueue chan llmRequest
var llmWg sync.WaitGroup
if *useLLM {
    engine := setupLLMEngine()
    llmQueue = make(chan llmRequest, 100)
    llmWg.Add(1)
    go func() { defer llmWg.Done(); llmWorker(ctx, engine, llmQueue) }()
}
// ... воркеры отправляют в llmQueue ...
wg.Wait()
close(llmQueue); llmWg.Wait()

// СТАЛО (main.go):
var engine dispatcher.AnswerGenerator
if *useLLM {
    engine = setupLLMEngine()
}
// ... воркеры создают Dispatcher(engine) каждый в runIteration ...
wg.Wait()
```

Удаляются: `llmRequest`, `llmResult`, `llmWorker`, `llmQueue`, `llmWg`. Поле `workerConfig.LLMQueue` заменяется на `workerConfig.Engine dispatcher.AnswerGenerator`.

**Важно:** каждый воркер создаёт СВОЙ экземпляр Dispatcher в `runIteration`. Dispatcher лёгкий (нет долгоживущих ресурсов), поэтому создание на итерацию — ок. Если нужен разделяемый engine — он thread-safe (`OpenAIProvider` защищён мьютексом).

### Шаг 3 — Обновить метрики

В `metrics.go` переименовать стадии:
- `connect` → удалить (connect эмулятора не отражает пользовательскую задержку; в production connect делается один раз при старте)
- `first_interim` → `interim_ui`
- `final` → `history_original`
- `translation` → `translation_ui`
- Добавить `answer_ui`

### Шаг 4 — Обновить отчёт

В `report.go` и `latency_calc.go`:
- Новые названия стадий
- Таймлайн с привязкой к зонам UI (1:Interim, 2:Translation, 3:History, 4:Answers)
- SLA из `docs/TESTING.md`:
  - Interim в UI: ≤ 500ms
  - Оригинал + перевод в History: ≤ 1000ms + 300ms = 1300ms
  - Перевод в UI: ≤ 1000ms + 300ms = 1300ms
  - Подсказка (LLM Answer): ≤ 2500ms

### Шаг 5 — Интеграционное сравнение с реальным Gladia (отдельный тест)

Сравнение эмулятора с реальным Gladia API — в отдельном интеграционном тесте (`test/gladia_latency/`), а не флагом в tail_latency. Причина: latency-тесты должны быть детерминированными и бесплатными (эмулятор), а реальный API недетерминирован и платный.

## Файлы

| Файл | Действие |
|---|---|
| `test/tail_latency/mock_overlay.go` | Создать |
| `test/tail_latency/worker.go` | Переписать: Dispatcher + MockOverlay вместо прямого чтения TextStream. Убрать llmRequest/llmResult/llmWorker. |
| `test/tail_latency/main.go` | Упростить: убрать llmQueue/llmWg, пробрасывать engine в workerConfig |
| `test/tail_latency/metrics.go` | Обновить названия стадий |
| `test/tail_latency/report.go` | Обновить вывод |
| `test/tail_latency/latency_calc.go` | Обновить модель |

## SLA-пороги (из docs/TESTING.md)

| Метрика | Порог | Что измеряет |
|---|---|---|
| Interim STT | ≤ 500ms | Первая транскрибация в зоне 1 |
| EndOfTurn (оригинал) | ≤ 1000ms | Оригинал в зоне 3 (через Dispatcher, вместе с переводом) |
| Translation | ≤ 300ms после оригинала | Перевод в зоне 2 |
| LLM Answer | ≤ 2500ms | Подсказка в зоне 4 (полная генерация) |
