# Code review: Timeline вопросов, зона 3

Дата: 2026-10-14. Ревью по текущему состоянию файлов (без истории изменений).
Источник требований: план v2 `.hermes/plans/2026-10-08_220728-question-timeline.md`, фаза 1 (Task 0–7).
Фаза 2 (Shift+F2/F3, уточнения) в ревью не входит; её следов в коде нет (grep Shift/modShift/0x0004 по internal/ — 0 совпадений) — подтверждено областью B.

## 1. Вердикт

**Approve with changes.** Ядро контракта «потребитель всегда знает, что вопрос задан» реализовано и протестировано; одна Major-находка (A-1, рассинхрон Step после Esc), один Major-подозреваемый (D5, отсутствие Clarifications — зависит от требований плана) и пакет Minor-находок (мёртвый код, docs-расхождения, тайминг-сны в тестах).

## 2. Соответствие плану (Task 0–7)

| Задача | Статус | Свидетель |
|---|---|---|
| 0. ui.Answer-метаданные (QuestionIdx/Total/QuestionText/State/Mode) | реализовано | internal/ui/types.go:33-44 (все 5 полей + контракт нулевых значений) |
| 1. Timeline + QEntry (ring cap=150, base, Idx) | реализовано | internal/dispatcher/questions.go (questionTimelineCapacity=150, QEntry.Idx, evictLocked) |
| 2. Лента меток зоны 3 (цвет по State, окно, абсолютные номера) | реализовано | internal/ui/overlay.go:729-769 (markLabelStyle, questionMarkColor), visibleWindow:591 |
| 3. Busy-guard F2/F3 при генерации | реализовано | internal/dispatcher/dispatcher.go (atomic.Bool, коалесинг) + TestPrevNextIgnoredDuringGeneration |
| 4. Управляющие действия отдельным типом | реализовано | internal/dispatcher/hotkeys.go: HotkeyAction{ActionDemand,ActionPrev,ActionNext}; GenerationCommand не расширен |
| 5. Shift+F2/F3 (фаза 2) | не реализовано (вне ревью, следов нет) | grep по internal/ — чисто |
| 6. Docs (qa-architecture, UI.md) | частично | qa-architecture.md:192-247 — timeline/состояния/F1 есть, но см. D2/D3; кольцевой буфер не описан |
| 7. Финальная верификация | реализовано | все вороты зелёные (см. п.4) |

## 3. Находки

### Major

**A-1. handleDemand QIdle-ветка продвигает Step, но команду отбрасывает — рассинхрон цикла F1 после Esc/переполнения очереди.**
`internal/dispatcher/dispatcher.go:464-470`. В ветке QIdle вызывается `NextStep(idx)` (Step 0→1 или 1→2), возвращённая команда отбрасывается, ставится жёстко CommandAnswer. Состояние QIdle с уже продвинутым Step достижимо: `ResetPending()` (questions.go:319-325) при Esc переводит QGenerating/QQueued→QIdle, не трогая Step (при AutoAnswer=true Step уже =1 после enqueueQuestion:326); `enqueueAnswer` (dispatcher.go:359) при переполнении — то же. После Esc первый F1 повторно делает NextStep (Step→2) и даёт CommandAnswer, второй F1 даёт CommandSimplerEnglish — шаг more context пропущен (цикл answer→answer→simpler вместо answer→more-context→simpler). Комментарий на l.440 («Step не трогаем») противоречит коду на l.467. Тестом не покрыто: TestHandleDemandResetsCancelledAfterEsc:648 ассертит только первый F1 после Esc.
Нарушено: план §Промпт «цикл модификаторов F1», чек-лист A(4)/(5).
Фикс: либо использовать возвращённую команду в QIdle-ветке, либо не продвигать Step там вовсе; сбрасывать Step при ResetPending/дропе в QIdle; синхронизировать комментарий.

### Требует решения автора

**D5. AnswerRequest.Clarifications отсутствует.** `internal/translator/conversation.go:50-64`, prompts.go:55-75 — поля нет, промпт его не использует. Если план фазы 1 требовал передачу уточнений — не реализовано; если это фаза 2 (уточнения) — находка снимается. Вынесено в вопросы (п.6).

### Medium

- **D4. Индикатор «➤» новых меток сломан при >150 вопросах.** overlay.go:687-694: `total <= active+1 → hide`, но Total = len(snap) (окно кольца ≤150), а active = meta.QuestionIdx — АБСОЛЮТНЫЙ. После 150-го вопроса QuestionIdx ≥ Total всегда → индикатор никогда не рисуется. Тестового покрытия 0. Нарушено: план §UI (индикатор свежих меток).
- **B-1. PostThreadMessageW: возврат игнорируется** (hotkey_windows.go:88). При недоставке WM_QUIT цикл GetMessageW зависает навсегда — ломается graceful shutdown (AGENTS.md). Фикс: проверять ret, добавить второй путь выхода (ctx.Done() + wake-сообщение).
- **B-3. bindings_test.go не сверяет vk с ожидаемыми кодами.** Подмена vkF1=0x71 пройдёт тест (id/vk/пары уникальны). Фикс: wantVK-таблица или table-driven {key,id,vk}. Чек-лист B(3) — покрыто неполно.
- **D1. docs/UI.md:50 противоречит коду:** заявлено «активная белым, остальные серым», фактически цвет ПО СОСТОЯНИЮ (янтарный/зелёный/красный/серый, overlay.go:752), активная отличается только альфой.
- **D3. docs/qa-architecture.md устарел:** parseAnswerHints→parseAnswers/translator.ParseAnswer; buildSystemPrompt(candidate)→BuildSystemPrompt(sourceLang,targetLang); AnswerRequest без SourceLang/TargetLang; QEntry без Idx; estimateTokens→common.EstimateTokens.
- **D6. TestStaleAnswerDoesNotOverwriteHeader тестирует синтетический путь** (commands_test.go:302-309): мутация таймлайна напрямую (timeline.Next×2+pushActiveHeader) вместо реальной навигации, т.к. HandleNext защёлкнут busy-guard. Регрессия в связке busy-guard↔навигация не будет поймана.
- **D7. 22 фиксированных time.Sleep в dispatcher_test.go** (до 800ms, напр. :755, :655, :400) вместо waitFor-поллинга — флак-риск на медленном CI. В commands_test.go waitFor уже есть — старые тесты не переведены.

### Minor / Nit

- **A-2. HasFresh/ClearFresh/freshAt — мёртвый код** в production-пути (questions.go:238-256): вызываются только из тестов; индикатор не доходит до UI (см. D4 — связка).
- **A-3. GenerateAnswers: doc-comment врёт** про shutdown/drainQueue (dispatcher.go:534-538) — реально вызывается только из тестов.
- **A-4 (Nit). ui.Answer.Total = размер окна кольца, а не общее число вопросов** (dispatcher.go:779, types.go:39 — код и комментарий согласованы, расхождение только с формулировкой плана): при >150 вопросах UI покажет «150/150».
- **B-2/B-10. Goroutine-утечка ctx-watcher при раннем выходе Run; pipeline не дожидается hotkey.Run** (hotkey_windows.go:74-89, pipeline.go:607-611). Фикс: done-канал + select.
- **B-4. bindings_test проверяет mods маской, не строгим равенством** — добавление MOD_ALT пройдёт незамеченным.
- **B-5. unregisterAll(stopID) хрупок к дублям id** — останавливается на первом совпадении; лучше итерировать по числу зарегистрированных.
- **B-9. hotkey_stub.go возвращает ctx.Err()** — DeadlineExceeded не отфильтрован в pipeline.go:608; контракт Run расходится между windows/stub.
- **D2. qa-architecture.md:226: «ClearFresh сбрасывает при возврате к хвосту»** — в реальности ClearFresh нигде не вызывается, сброс идёт через clearFreshOn в Prev/Next.
- **D8 (Nit). TestWaitForDestroy* привязаны к часовым порогам** — редкий ложный failure возможен на медленном CI.
- **B-7 (Nit). Комментарий dispatcher.go:440 противоречит коду** (дубль аспекта A-1).
- **B-6 (info). Контракт «onKey не блокирует» не задокументирован** — сейчас выполняется (все ветки неблокирующие), но хрупок к регрессии.

## 4. Результаты прогонов (ворота)

- `gofmt -l ./internal` — 16 файлов; проверено gofmt -d: все diff чисто line-ending (CRLF) — false positive, НЕ находка.
- `go vet ./...` — 0 предупреждений.
- `GOOS=windows go vet ./internal/hotkey/` — чисто.
- `go test ./...` — все 11 пакетов ok.
- `go test -race ./internal/...` — все 10 пакетов ok.

## 5. Что сделано хорошо

1. Кольцевой буфер: абсолютная нумерация base+QEntry.Idx, evictLocked корректно сдвигает active/freshAt; вытеснение активной метки переводит её на новую голову — покрыто сильными поведенческими тестами.
2. Глубокое копирование во всех публичных getter'ах (Active/Snapshot/SetAnswer) — нет алиасинга на внутренний срез, контракт покрыт отдельными тестами.
3. Немедленный push шапки при детекции вопроса до завершения генерации — «потребитель знает, что вопрос задан» реализован как контракт (TestEnqueueQuestionPushesHeaderImmediately).
4. Анти-флак приёмы в новых тестах: blockingEngine (release-канал вместо сна) и waitFor-поллинг; test-driven таблицы высот lineHeightPx защищают инвариант «зона = k × измеренная строка».

## 6. Вопросы автору

1. Требовал ли план фазы 1 поле AnswerRequest.Clarifications (D5)? Если да — это Major-пробел; если уточнения относятся к фазе 2 — находка снимается.
2. Индикатор «➤» (D4): планируется ли подключение HasFresh/ClearFresh к UI (тогда фикс вычисления по абсолютным индексам обязателен), или индикатор выпадает из объёма и A-2 следует закрыть удалением мёртвого кода?
3. Допустима ли семантика ui.Answer.Total как «окно кольца» (A-4), или нужен абсолютный счётчик вопросов?

## Пробелы тестового покрытия (сводка от D)

- Индикатор ➤ (показ/скрытие по правилу) — 0 тестов; правило сломано при >150 вопросах (D4).
- Реальный путь «навигация во время генерации игнорируется → ответ в правильную метку» (D6).
- Esc, перебиваемый новым вопросом до отмены генерации (гонка Cancel ↔ enqueueQuestion:313).
- Приоритет hotkeyCh при заполненной answerCh (qa-architecture.md:280).
- Вытеснение активной метки в комбинации с fresh (evictLocked сдвигает freshAt — не проверяется).
- Повторный F1 на не-хвостовой QReady-метке (RecordAnswer-гейт вместе с навигацией).
- Частичный тег при TargetLang≠ru ('EN: x | DE: y | RU: z').
- lineHeightCache тестируется через чтение внутреннего поля (тест на реализацию), нет поведенческого «кэш не пересчитывается».

---
Отчёт собран из результатов 4 областных ревьюеров (A dispatcher/timeline, B hotkey/pipeline, C ui, D тесты+docs) с самостоятельной верификацией пунктов области C (types.go, markLabelStyle/questionMarkColor, smallFontSize, visibleWindow, шапка maxQuestionHeaderLines, подсказка «F1 — ответить»).
