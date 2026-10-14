# Повторное code review — фаза 2 (ручные уточнения) + ревью-фиксы

Скоуп: `dabd61a..c8f8b7d` (13 коммитов, 36 файлов, +4208/−678), базовая линия
первого ревью — `a4ce96d`. Режим review-only: 4 параллельных ревьюера
(R1 dispatcher, R2 timeline/translator, R3 hotkey/pipeline, R4 UI/тесты);
все Major-находки верифицированы главным агентом по коду.

## 1. Вердикт

**Approve with changes.** Все находки первого ревью закрыты, фаза 2 реализована
в соответствии с планом, ворота зелёные. Осталось 2 подтверждённых Major
в коде/тестах (C1 — остаточный A-1 в пути cancelled-дропа; M-1 — фиксированные
sleep в commands_test.go) и 2 Major-кандидата на усмотрение автора (wmWake-выход,
hotkeyDone-токен), плюс пакет Minor.

## 2. Находки первого ревью → статус

| Находка | Суть | Статус | Доказательство |
|---|---|---|---|
| A-1 | Рассинхрон Step после Esc | **Закрыта в основных путях, остаточный дефект C1** | ResetStep в `dropQueue` (dispatcher.go:681-682), `enqueueAnswer` (:404-405), error-ветке (:841-842); **но** cancelled-дроп воркера (:432-433) не сбрасывает Step — см. C1 |
| A-2 | Индикатор fresh не подключён к UI | **Закрыта** | `2b8418b`: dispatcher.go:972,979 `HasFresh` → шапка; overlay.go:769-771 `showFreshIndicator` |
| A-3 | Мёртвый код UI | **Закрыта** | очишено в волнах ревью-фиксов; регресс — новый мёртвый `isMetadataOnly` (Nit n-1, overlay.go:634-639, 0 вызовов) |
| A-4 | Счётчик вопросов сбрасывается при evict | **Закрыта** | questions.go: totalAsked++ в Add, evictLocked не трогает; pushHeader `total := d.timeline.TotalAsked()` (dispatcher.go:967); тест commands_test.go:1009-1058 (TotalAsked=156 при окне 150) |
| B-1 | PostThreadMessageW без проверки | **Закрыта** | hotkey_windows.go:121-132 `if ret == 0 { return fmt.Errorf }`; но диагностика см. Minor r3-3 |
| B-2 | Утечка watcher-горутины | **Закрыта** | hotkey_windows.go:176-188 done-канал + два выхода из select |
| B-3 | bindings_test не сверяет vk | **Закрыта** | bindings_test.go:83-85 wantVK, :89-91 строгое `==` mods, :142-151 Shift-биндинги |
| B-9/B-10 | Ненадёжный shutdown хоткея | **Закрыта** | wake-сообщение (hotkey_windows.go:199-202), unregisterRegistered по факту регистрации (:152-159), pipeline.go:436-445 ожидание с таймаутом; оговорка — Major-кандидат r3-2 |
| D1/D2/D3 | Docs-расхождения | **Закрыты частично** | 08daf67 синхронизировал qa-architecture/UI.md; **но** qa-architecture.md:36,71-78 до сих пор показывает AnswerRequest с 6 полями без Clarifications — см. Minor doc-1 |
| D4 | Индикатор «➤» смешивал abs idx с оконным total | **Закрыта** | UI не считает, берёт готовый HasFresh из таймлайна (overlay.go:769-771); регресс-тест 155/156 (commands_test.go:1009-1058) |
| D5 | AnswerRequest.Clarifications отсутствует | **Закрыта** (снималась как фаза-2): B-T0 реализовал | conversation.go:50-68 — 7 полей; prompts.go:68,90-108 формат блока; nil → промпт байт-в-байт прежний (сверка с a4ce96d) |
| D6 | Тесты навигации-при-генерации синтетические | **Закрыта** | `90d2b2e`: TestNavigationDuringGenerationIgnoredAnswerToCorrectLabel (commands_test.go:353-442) — реальные HandleNext/HandlePrev; старый синтетический тест остался (Minor m-3) |
| D7 | Фиксированные sleep в dispatcher-тестах | **Закрыта формально, НЕ по существу** | dispatcher_test.go: 0 sleep; **но** в commands_test.go осталось 10 фиксированных sleep 10-100ms, часть — в «негативных» ассертах → ложно-зелёные (Major M-1) |
| D8 | Пороги waitForDestroy маскируют зависание | **Закрыта** | `6700186`: overlay_test.go:520 (50ms), :527 (20×=1s), пороги обоснованы; TestWaitForDestroyReceivesEvent — верхняя граница = destroyEventTimeout (:513) |

## 3. Новые находки

### Major

**C1. Cancelled-дроп задания не сбрасывает Step — остаточный A-1.**
`internal/dispatcher/dispatcher.go:432-433`:
```go
if d.cancelled.Load() {
    d.timeline.SetState(task.idx, QIdle)
    continue
}
```
`ResetStep` не вызывается, в отличие от трёх симметричных путей дропа
(:404-405, :681-682, :841-842). `ResetPending` (questions.go:486) сбрасывает
Step только у QQueued/QGenerating — метка уже QIdle и проскакивает. После
Esc у такой метки Step=1: первый F1 в QIdle-ветке вернёт CommandMoreContext
вместо CommandAnswer — тот же симптом A-1. Путь достижим: `select` воркера
(:420) читает answerCh и cancelCh с равной вероятностью; теста на эту
ветку нет (TestEscThenF1KeepsCommandCycle покрывает только error-ветку).
Fix: добавить `d.timeline.ResetStep(task.idx)` в :432-435.
Нарушено: docs/qa-architecture.md §сброса Step при отмене. Верифицировано
главным агентом по коду.

**M-1. 10 фиксированных time.Sleep в commands_test.go, в т.ч. в «негативных» ассертах (D7 по существу не закрыт).**
`internal/dispatcher/commands_test.go`: 183, 467, 472, 543, 615, 745, 754,
1184, 1301, 1350 (sleep 10-100ms). Паттерн `Sleep(100ms); if len(engine.Calls()) != 0 → Error`
даёт ложно-зелёный результат на медленном CI: запоздавший вызов LLM ещё не
зарегистрирован — тест проходит, хотя контракт нарушен. Все места заменяемы
на существующий `waitIdle(t, d)` (commands_test.go:32-37). Fix: перевести
«негативные» ассерты на waitIdle; для :467 (5×HandleDemand + 10ms) убрать
sleep, оставив серию вызовов. Верифицировано grep'ом: 11 реальных вызовов
time.Sleep (включая 5ms-поллинг waitFor), dispatcher_test.go — 0.

**r3-1 (кандидат Major). Выход из GetMessageW по любому WM_APP+1 без проверки ctx.**
`internal/hotkey/hotkey_windows.go:199-202`: `if m.message == wmWake { return nil }`.
Любое чужое сообщение WM_APP+1, адресованное потоку (hook/плагин в процессе),
молча остановит все хоткеи при живом ctx — тихий отказ без диагностики.
Комментарий :34-38 («если WM_QUIT потерян») описывает нереальный сценарий:
оба сообщения постятся одним watcher'ом (:183-184). Fix: atomic-флаг отмены,
выставляемый watcher'ом, и проверка его в ветке wmWake. Severity — на
усмотрение автора: вероятность чужого WM_APP+1 в процессе низкая, но отказ
тихий и полный.

**r3-2 (кандидат Major → по оценке главного агента Minor). hotkeyDone — токен буфера 1, читается один раз.**
`internal/pipeline/pipeline.go:68,436-445`: единственный `<-p.hotkeyDone`.
Второй shutdown-путь увидит пустой канал и выйдет по таймауту; инвариант
«shutdown дождался Run» держится на том, что shutdown вызывается один раз.
Fix: `close(p.hotkeyDone)` вместо токена (идемпотентно) либо пересоздание
канала при каждом запуске.

### Minor

- **C2.** `lastFinal` потребляется до успеха операции: при провале AddClarification
  (вытесненная метка, dispatcher.go:528-534) реплика интервьюера теряется
  безвозвратно (лог «уточнение потеряно», UI не сигналит). Окно 150 делает
  гонку редкой. Fix: возвращать текст в lastFinal при неуспехе.
- **doc-1.** qa-architecture.md:36,71-78: AnswerRequest описан с 6 полями —
  Clarifications (bc64576) в доки не попали (docs-синк 08daf67 был раньше).
  Сверять код с доками — доки должны быть контрактом.
- **R2-min1.** Нет golden-теста на байтовую идентичность промпта при nil
  Clarifications: все проверки в prompts_test.go — подстрочные (Contains);
  регрессию `\n`/порядка блоков не поймают. Fix:
  TestBuildAnswerPrompt_NilClarificationsByteIdentical с точным сравнением.
- **R2-min2.** overlay.go:880 `Q%d (%d/%d)` — при вытеснении даёт «Q5 (5/156)»:
  числитель абсолютный, читается как «5 из 156 в ленте». Контракт Total
  корректен, но подача вводит в заблуждение. Fix: согласовать формат
  (например «Q5 из 156») + тест на формат.
- **m-1.** clarifications_test.go:179-198: «прежний рендер байт-в-байт»
  проверяется своими предикатами, а не сравнением рендеров nil vs []string{}.
- **m-2.** Правило «➤» (relIdx активной < relIdx хвоста в окне) не
  зафиксировано пиксельным тестом: questions_strip_test.go:217-239 проверяет
  тавтологию showFreshIndicator(x)==x.HasFresh; нет кейса «активная = хвост
  окна» и измерения ширины ленты.
- **m-3/m-4.** Синтетический TestStaleAnswerDoesNotOverwriteHeader
  (commands_test.go:299-327) не мигрирован и его комментарий описывает
  недостижимый сценарий (Tail-follow: Add сам двигает активную на хвост,
  questions.go:311-317). Fix: удалить или свести к чистому юнит-тесту
  pushHeader.
- **r3-3.** hotkey_windows.go:164-166: ветка `Errno(0) → GetLastError()`
  мёртвая (проверено реальным вызовом RegisterHotKey: при отказе errno
  корректен, 0x581), комментарий :116-120 описывает нереализуемый случай.
- **r3-4.** Отказ postThreadMessage логируется только в slog.Warn, не в
  сессионный CSV-лог (SessionLogger) — в сессии диагностики не будет.
- **r3-5.** bindings_test.go:78-91 сверяет по позиции таблицы: перестановка
  записей даст ложно-красный. Fix: ключевать по b.id.
- **r3-6.** keyForID дублирует таблицу биндингов (hotkey_windows.go:95-114
  vs :82-90); согласованность держится только тестом. Fix: единая таблица
  {id, key, mods, vk}.

### Nit

- C3: лог после AbsorbLast (dispatcher.go:536-537) указывает на активную
  метку, а слияние произошло с хвостом — диагностика вводит в заблуждение.
- C4: HandleClarify при пустом таймлайне не потребляет lastFinal — старая
  реплика «оживёт» при появлении метки; контрактом не запрещено.
- R2-n3: AbsorbLast на пустом таймлайне возвращает (0,false); лучше (-1,false).
- n-1: isMetadataOnly (overlay.go:634-639) — мёртвый код, 0 вызовов.
- n-2: TestMetadataAnswersEmptySourceSkipped не ассертит пропуск пустой строки.
- r3-7: keyForID мапит 0..4 «на глаза» (id==Key) — молча полагается на совпадение.
- R2-n1: расширение Answer/OverlayMessage не централизовано — риск рассинхрона полей.
- R2-n2: clearFreshOn без явного early return при freshAt==-1 (читаемость).

## 4. Ворота (дословно)

```
go vet ./...                          → exit 0 (0 предупреждений)
gofmt (CR-stripped cmp по всем .go)   → CLEAN (CRLF-шум отсутствует)
go test -short -count=2 ./internal/...→ ok ×10 (capture 0.891s, common 0.190s,
                                        context 0.388s, dispatcher 4.332s,
                                        hotkey 0.062s, logger 0.181s,
                                        pipeline 16.102s, stt 3.851s,
                                        translator 24.357s, ui 0.828s)
go test -race ./internal/...          → ok ×10 (translator 13.306s, pipeline 16.305s,
                                        ui 3.274s, hotkey 1.080s, прочие cached)
go test -tags integration ./...        → ok (все пакеты; groq live-API прошёл
                                        без ретраев, translator 20.930s;
                                        TestWindowStarts ok 2.551s)
```

Примечание: `gofmt -l` на CRLF-checkout даёт false positives по всем файлам —
ловушка из первого ревью, проверено CR-stripped сравнением, находкой не является.

## 5. Что сделано хорошо

1. Единая точка сборки AnswerRequest (dispatcher.go:812-820) — уточнения
   гарантированно попадают и в авто-, и в F1-путь без дублирования логики
   (единственное `AnswerRequest{` в non-test коде).
2. Идемпотентный снос регистраций хоткеев (unregisterRegistered по факту
   успешных, hotkey_windows.go:152-159) + непересекающиеся диапазоны id
   (0..4 / 1001-1002) — регресс инцидента с дублем id закрыт на уровне
   данных, кода и теста (bindings_test.go:110-114 проверяет невозможные id).
3. Поведенческие тесты идут по живым путям: pipeline_test.go:1408-1493
   поднимает реальный dispatch.Run и гоняет Shift+F2/F3 через textStream;
   commands_test.go:353-442 — реальные HandleNext/HandlePrev во время
   генерации; 0 sleep в dispatcher_test.go.
4. Глубокое копирование стека уточнений на всех публичных границах
   таймлайна (cloneEntry/Snapshot/Active/Clarifications) + -race чисто.

## 6. Вопросы автору

1. **AbsorbLast и не-вопросы.** lastFinal пишется на каждую EndOfTurn-реплику
   интервьюера (вопрос и не-вопрос), а слияние идёт по ТОЧНОМУ (байтовому,
   без TrimSpace — questions.go:215) равенству текста последней метки и
   lastFinal. Уточнение к первоначальной версии отчёта: ревьюер R2 указывал
   сравнение «после тримминга» — при верификации это не подтвердилось
   (сравнение было точным). Решение автора по итогам ревью: сравнение
   ДОЛЖНО идти после TrimSpace с обеих сторон — включено в волну правок.
2. **Severity r3-1 (wmWake).** Считать ли чужое WM_APP+1 в процессе
   реалистичной угрозой (Major) или принять риск (Minor с atomic-флагом
   на будущее)?
3. **Логирование hotkey-диагностики** в сессионный CSV-лог — нужно ли
   прокидывать SessionLogger в hotkey.Run (r3-4)?

## 7. Пробелы тестового покрытия

- Авто-путь ответа с непустыми Clarifications: оба теста
  (commands_test.go:1359, :1423) форсируют AutoAnswer=false и F1; ожидание
  «AutoAnswer=true несёт Clarifications» не проверено.
- AbsorbLast вне race-теста: questions_concurrent_test.go гоняет
  Add/AddClarification/RemoveLastClarification, но не AbsorbLast — самый
  «списочный» мутатор кольца.
- C1-ветка (cancelled-дроп в воркере, dispatcher.go:432-435) — нулевое
  покрытие; тест из п.3 Major M-1 закроет её попутно.
- freshAt при evictLocked: нет теста «fresh-метка вытеснена головой кольца →
  HasFresh()==false» (связка evict+fresh покрыта только для активной).
- lineHeightCache тестируется через чтение внутреннего поля
  (line_height_test.go:100-105) — поведенческого «кэш не пересчитывается» нет
  (перенос из первого ревью).
- Промпт: нет байтового golden-теста при nil Clarifications (R2-min1).
- UI: нет пиксельного теста правила «➤» на границе окна (m-2) и формата
  «(N/N)» при QuestionIdx << Total (R2-min2).
- Частичный тег TargetLang≠ru — вне скоупа волны, статус не изменился.
- window_starts_integration_test.go:150-161: zonesInfo пишет только t.Logf —
  пропорции зон (Flexed 0.45/0.55) не ассертятся.
