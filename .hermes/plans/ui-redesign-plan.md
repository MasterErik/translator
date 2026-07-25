# План: редизайн UI — 4 зоны + история оригиналов

Дата: 2026-07-23  
Статус: не начат  
Основа: макет `.hermes/plans/ui-redesign-mockup.md`

## Файлы к изменению

| Файл | Что меняется |
|------|-------------|
| `internal/ui/types.go` | + `History` UIMessageType |
| `internal/ui/overlay.go` | render: 4 зоны, layoutHistory, layoutTranslation — 5 строк |
| `internal/pipeline/pipeline.go` | + History-сообщение при EndOfTurn |
| `main.go` / `main_stub.go` | высота 400 → 650 |
| `internal/common/config.go` | + OverlayMaxLines default |

---

## Шаг 1 — types.go: новый тип сообщения

- [ ] Добавить `History UIMessageType = "History"` в `UIMessageType`

## Шаг 2 — pipeline.go: отправка History

- [ ] В `handleStreamingTranslation` после `isQ := translator.IsQuestion(...)`:
  - добавить `p.overlay.AddMessage(UIMessage{Type: ui.History, Text: event.Text, Timestamp: event.Timestamp})`

## Шаг 3 — overlay.go: новый render

- [ ] `render()` → 4 зоны вместо 3:
  1. `layoutRigid` → `layoutInterim` (речь, 2 строки)
  2. `layoutSeparator`
  3. `layoutRigid` → `layoutTranslation` (перевод, фикс. 5 строк — MaxLines=5)
  4. `layoutRigid` → `layoutAnswers` (подсказки, только если есть)
  5. `layoutSeparator`
  6. `layoutFlexed(1)` → `layoutHistory` (история оригиналов, скролл)

- [ ] `layoutTranslationZone` → переименовать в `layoutTranslation`, убрать скролл истории переводов, показывать ТОЛЬКО текущий перевод (streaming или последний done), 5 строк
- [ ] Новая функция `layoutHistory(gtx, th, history, fs)` — вертикальный список всех History-сообщений

- [ ] `AddMessage` → добавить case `History`: просто append (не заменять)

## Шаг 4 — main.go / main_stub.go: высота

- [ ] `OverlayHeight` default: 400 → 650 (в `config.go:applyDefaults`)

## Шаг 5 — Проверка

- [ ] `go build .` (cgo + !cgo)
- [ ] `go vet ./...`
- [ ] `go test -race ./...`
