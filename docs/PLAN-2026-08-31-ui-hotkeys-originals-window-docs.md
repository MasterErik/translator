# UI: hotkey-раскладка F1–F4, окно оригиналов, стартовые линии, docs — Plan

> **For Hermes:** Use subagent-driven-development skill to implement this plan task-by-task.

**Goal:** Актуализировать UI до текущей реальности: новая раскладка клавиш F1–F4 (think deeper убран), F4 = тумблер окна истории оригиналов (английского), стартовая отрисовка линий всех зон, синхронизация docs/UI.md и docs/ARCHITECTURE.md.

**Architecture:** Хоткеи-маршрутизация уже разделена: F9 уходит прямо в overlay (без dispatcher), F1–F4 — в dispatcher. План убирает CommandThinkDeeper, сдвигает F3→F2 / F4→F3, добавляет F4 → overlay.ToggleTranscriptionHistory() (заменяя F9). Стартовые линии — настройка начального `historyVisible = true` при старте (зоны 1–3 рисуются всегда, зона 4 получает separator+каркас). Доки обновляются последними, по факту кода.

**Tech Stack:** Go 1.22+, GioUI v0.10.1, codegraph-навигация, `go test ./internal/...` по пакетам.

---

## Контекст (проверено в коде 2026-08-31)

- `internal/pipeline/pipeline.go:541` — `handleHotkey`: F9 → `overlay.ToggleTranscriptionHistory()`; F1→CommandAnswer, F2→CommandThinkDeeper, F3→CommandMoreContext, F4→CommandSimplerEnglish, Esc→Cancel.
- `internal/translator/conversation.go:20-28` — enum `GenerationCommand` c CommandThinkDeeper/MoreContext/SimplerEnglish.
- `internal/translator/prompts.go:60-76` — `commandInstruction`: «проще английский» = LLM-промпт «Rephrase the answer using simpler English...», i.e. regeneration последнего ответа с инструкцией упрощения; перевод должен соответствовать новой английской версии.
- `internal/ui/overlay.go:43` — `historyVisible bool` (зона 4, 4 строки, шрифт fs-2), по умолчанию false → при старте зоны 1–3 рисуются, зона 4 и её separator отсутствуют.
- `internal/ui/overlay.go:224` — `ToggleTranscriptionHistory()` уже есть и потокобезопасен.
- docs/UI.md — устарел: описывает 4 постоянные зоны с TranslationHistory(EN)+TranscriptionHistory как зоны 2 и 3, без хоткеев.
- docs/ARCHITECTURE.md:154, 264-268 — описывает F1–F4 c think deeper.

## Решения (на утверждение)

1. **CommandThinkDeeper удаляется полностью** (enum, prompt, dispatcher-ветки, тесты) — модель сейчас не поддерживает; вернуть легко по git-истории.
2. **Новая раскладка:** F1 — обычный ответ, F2 — больше контекста, F3 — проще английский, F4 — окно оригиналов (toggle), Esc — отмена. F9 уходит (замена на F4).
3. **Стартовые линии:** `historyVisible` инициализируется `true` — при запуске видны все 4 зоны и все separator-линии; F4 прячет/показывает зону 4.

---

### Task 1: Удалить CommandThinkDeeper (translator-пакет)

**Files:**
- Modify: `internal/translator/conversation.go:20-28` (enum), `internal/translator/prompts.go:60-66` (commandInstruction)
- Test: `internal/translator/conversation_test.go`, `internal/translator/prompts_test.go`

**Steps:**
1. Удалить CommandThinkDeeper из enum, String(), commandInstruction и комментариев (F2→F3 сдвиг упоминаний оставить задаче 2 — здесь только удалить ветку ThinkDeeper).
2. Обновить тесты: убрать кейсы ThinkDeeper; кейс «неизвестная команда → пустая инструкция» сохранить.
3. Run: `go test ./internal/translator/... && go vet ./internal/translator/...` — PASS.
4. Commit: `refactor(translator): drop CommandThinkDeeper (unsupported by model)`.

### Task 2: Новая раскладка хоткеев в pipeline

**Files:**
- Modify: `internal/pipeline/pipeline.go:541-564` (handleHotkey), комментарий.
**Шаги:**
1. Обновить handleHotkey: `KeyF2 → CommandMoreContext`, `KeyF3 → CommandSimplerEnglish`, `KeyF4 → ToggleTranscriptionHistory()` (перенести ветку из F9, удалить case KeyF9).
2. Обновить док-комментарий handleHotkey и комментарии в conversation.go (F2/F3 нумерация).
3. Run: `go test ./internal/pipeline/... && go vet ./internal/pipeline/...` — PASS (обновить pipeline_test / pipeline_integration_test, если мокают клавиши).
4. Commit: `feat(pipeline): rebind F2=MoreContext F3=SimplerEnglish F4=history toggle`.

### Task 3: Стартовая видимость зоны 4 (все линии с запуска)

**Files:**
- Modify: `internal/ui/overlay.go:43` — `historyVisible bool` → инициализация true (в конструкторе Overlay, не zero value).
- Test: `internal/ui/history_toggle_test.go`

**Steps:**
1. Тест (RED): новый тест — после создания Overlay `HistoryVisible() == true`; существующий toggle-тест остаётся зелёным.
2. Реализация: в конструкторе `o.historyVisible = true`.
3. Run: `go test ./internal/ui/...` — PASS.
4. Commit: `feat(ui): show all zones and separators at startup`.

### Task 4: docs/UI.md — переписать под текущий UI

**Files:**
- Rewrite: `docs/UI.md`

**Содержание:** 3 постоянные зоны (Interim, TranslationHistory EN+RU, AnswerCandidates) + зона 4 TranscriptionHistory (4 строки, по F4, скрыта по умолчанию — НЕТ: видна при старте, F4 прячет); параметры окна; автоскролл; кастомный event loop; таблица хоткеев F1–F4/Esc; TestWindowStarts. ASCII-схему обновить: 4 зоны, зона 4 «(F4)».
5. Commit: `docs(ui): rewrite UI.md for current zones and F1-F4 layout`.

### Task 5: docs/ARCHITECTURE.md — синхронизация

**Files:**
- Modify: `docs/ARCHITECTURE.md:154` и блок 264-268.

**Содержание:** убрать think deeper из перечня; новая раскладка F1–F4; F4 → overlay напрямую (мимо dispatcher), как раньше F9; зона 4 видна при старте.
- Run: финальная проверка (см. ниже).
- Commit: `docs(arch): sync hotkeys and zone visibility`.

### Task 6: Финальная проверка (только здесь, один раз)

```bash
export PATH="/c/msys64/ucrt64/bin:$PATH"
go vet ./...
go test ./...
go test -race ./...
```
Ожидаемо: всё PASS. Затем ручной smoke: `go run .` — при старте все 4 зоны с линиями, F4 прячет/показывает зону 4, F1/F2/F3 генерация, Esc отмена.

---

## Статус выполнения (2026-08-31, выполнено)

- Task 1 — выполнен (sub-agent, коммит `9e42365`): CommandThinkDeeper удалён из enum/prompts/тестов + dispatcher-тесты.
- Task 3 — выполнен (sub-agent, коммит `655e7f1`): historyVisible=true при старте, добавлен HistoryVisible(), TDD.
- Task 2 — выполнен (sub-agent, коммит `fd5d95f`): F2=MoreContext, F3=SimplerEnglish, F4=history toggle (мимо dispatcher), F9-ветка удалена, сборка pipeline восстановлена.
- Task 4 — выполнен (sub-agent, коммит `53bca52`): docs/UI.md переписан под текущий UI.
- Task 5 — выполнен (sub-agent, коммит `2ad6a98`): docs/ARCHITECTURE.md синхронизирован.
- Дополнительно (коммит `57a7a79`): вычищена мёртвая KeyF9-обвязка в internal/hotkey, комментарии overlay.go F9→F4.
- Task 6 — финальная проверка: `go vet ./...` PASS, `go test ./...` PASS; `go test -race ./...` — см. ниже. Ручной smoke (`go run .`) — за пользователем.

## Риски / вопросы

- Удаление enum-значения — ломающее изменение для dispatcher-очередей, где могут копиться старые команды: проверить `internal/dispatcher/commands_test.go` на ThinkDeeper-кейсы (входит в Task 1/2 scope).
- Если historyVisible=true при старте нежелательно для размера окна 650px (зона 4 отнимает ~4 строки) — тривиально вернуть false, но по требованию «сразу отрисовать линии для всех зон» — true.
- F9-пользовательская привычка: заменяем на F4 осознанно (F9-код удаляется, не остаётся дублем).
