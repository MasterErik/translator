### UI — четыре зоны (GioUI v0.10.1)

```
┌──────────────────────────┐
│ I have five years of...  │ ← Зона 1: Interim (речь, 3 строки видимых, скролл,
├──────────────────────────┤ separator 3px          белый текст)
│ EN: We use Redis for...  │ ← Зона 2: TranslationHistory (скролл переводов)
│ RU: Мы используем Redis  │           оригинал + перевод, формат EN/RU
├──────────────────────────┤ separator 3px
│ [2]●[3]…[4]○        ➤    │ ← Зона 3: AnswerCandidates — лента меток вопросов
│ Q3 (3/5): why Redis? · answer │  + шапка «Q<idx> (<idx>/<total>): <вопрос> · <mode>»
│ EN: Redis is...          │        строки ответа (Source белый / Target зелёный)
│ RU: Redis — это...       │        или подсказка состояния (idle/queued/failed)
├──────────────────────────┤ separator 3px
│             │              Зона 4: TranscriptionHistory (скролл, 4 строки) —
│             │              СКРЫТА при старте, F4 показывает
└──────────────────────────┘
```

**Зоны (3 постоянные + 1 тумблер):**

| Зона | Тип | Описание |
|------|-----|----------|
| 1: Interim | `Interim` | Текущая речь (верх). 3 видимых строки, белый текст, **вертикальный скролл**. Замена — только последний. На EndOfTurn dispatcher шлёт финальный полный текст — заменяет partial, фраза выведена целиком. |
| 2: TranslationHistory | `Translation` | Переводы (EN + RU). Скролл, `Flexed 0.45`. Только append (как History) — streaming-ветка `pending`/`streaming` удалена, dispatcher шлёт только `done`. |
| 3: AnswerCandidates | `AnswerCandidates` | Подсказки ответов. С контрактом таймлайна — **лента меток вопросов** + **шапка активного вопроса** сверху, ниже `Source`/`Target` (формат `SRC: … \| TGT: …`). Скролл. Хранится история (`answersHistory`) — рендерится последняя подсказка. Пустой ответ заменяется **подсказкой состояния** (idle/queued/failed). Дополнительно принимает тип **`Error`** (`UIMessageType`): ошибка генерации движка рисуется красным и имеет приоритет над подсказками. |
| 4: TranscriptionHistory | `History` | Оригиналы речи (EN). Нижняя зона, высота ровно 4 строки, **единый шрифт `fs`**. Только append. Тумблер — **F4**. |

- **Шрифт — единый `fs` во всех зонах** (Task 3.0: убран `fs-2`/floor для зон 2–4 и ошибок).
- **Межстрочный интервал — стандартный Gio:** `overlayLabel` задаёт `LineHeightScale = 1.0`
  (одна крутилка `lineSpacingScale` на будущее; `LineHeightScale` масштабирует шрифт, поэтому
  трогать осознанно). `LineHeight` явно не задаётся. Все строки — только через `overlayLabel`.

- Зона 4 **скрыта при старте** (`historyVisible: false` в конструкторе `NewOverlay`) —
  её separator и сама зона отсутствуют до нажатия **F4**.
- **History-данные не включают зону 4 сами:** `AddMessage` с типом `History` только
  добавляет сообщение и инвалидирует кадр, не меняя `historyVisible` — зоны 1–3
  рендерятся независимо от наполненности History, зона 4 появляется исключительно
  по F4 (регрессионный тест `zone4_hidden_test.go`).
- **F4** показывает/прячет зону 4: когда скрыта, её separator и сама зона отсутствуют
  в `Flex` и не занимают место — высоту освобождённую получают зоны 2–3.
- Только зоны 1–3 постоянны в layout; зона 4 добавляется при `historyVisible`.

**Зона 3 — таймлайн вопросов** (контракт задаётся полями `ui.Answer`: `QuestionIdx`,
`Total`, `QuestionText`, `State`, `Mode`, `HasFresh`; при нулевых метаданных рендер
идёт по старому формату — просто строки подсказок):

- **Лента меток** (строка над шапкой): `[N]маркер` для каждого вопроса таймлайна.
  Маркер состояния — `○` idle, `…` queued/generating, `●` ready, `!` failed.
  Цвет метки задаётся **состоянием генерации** (`questionMarkColor`): **янтарный**
  `queued`/`generating` (`questionPendingColor`), **зелёный** `ready`
  (`questionReadyColor`), **красный** `failed` (`layoutErrorColor`), **серый**
  `idle`/неизвестное (`questionIdleColor`, приглушённый blue-grey). Активная и
  неактивная метки используют один оттенок состояния и различаются только
  **альфой**: активная непрозрачна (`A=255`), неактивная приглушена
  (`markInactiveAlpha=200`, `markLabelStyle`/`overlay.go`).
- **Окно ленты:** максимум **8** видимых меток (`questionStripMaxVisible`); при большем
  числе показывается окно вокруг активной — `visibleWindow` центрирует активную и
  прижимается к границам `[0, total-1]`.
- **Индикатор свежих меток:** у правого края ленты рисуется `➤` — «есть
  непросмотренная метка правее активной», к которой можно перейти по **F3
  (Next)**. Признак приходит от dispatcher в `ui.Answer.HasFresh` (из
  `QuestionTimeline.HasFresh()` — свежесть считается по **абсолютному** idx
  метки, а не сравнением `QuestionIdx` с `Total`), UI его не досчитывает
  (`showFreshIndicator(meta) = meta.HasFresh`). Индикатор гаснет, когда
  пользователь доходит по Prev/Next до свежей метки: `clearFreshOn` внутри
  `Prev`/`Next` сбрасывает `freshAt` при попадании на эту метку.
- **Шапка активного вопроса:** `Q<idx> (<idx>/<total>): <вопрос>`, вопрос обрезается
  до **2 строк** (`maxQuestionHeaderLines`, с ellipsis). `idx` — **абсолютный**
  номер метки, `total` — **абсолютное** число заданных за сессию вопросов
  (`QuestionTimeline.TotalAsked()`, не сбрасывается при вытеснении меток из
  кольца). Рядом — **подпись режима**
  `· <mode>`: `answer` / `more context` / `simpler` (пусто у idle-метки).
- **Тело ответа:** строки `Source` (белый) / `Target` (зелёный) с автоскроллом.
  Когда текста ответа нет, вместо пустоты — **подсказка состояния** по `State`:
  idle → «F1 — ответить», queued/generating → «генерация…» (жёлтоватый),
  failed → «ошибка генерации» (красный). Явный `State` имеет приоритет; пустой/ready —
  как idle.
- Шрифт подписей таймлайна — `smallFontSize(fs)` = `fs-2` с полом 10.
- **Стартовый каркас:** с запуска окно рисует каркас — separator-линии зон 1–3 видны
  сразу, без текста. Высоты зон кратны фактической высоте строки:
  `lineHeightPx(fs)` — измеренная оффскрин-рендером высота глиф-бокса одной строки
  (кэш по fs), высота зоны = k × `lineHeightPx(fs)`. Зона 1 (Interim, Rigid) — 3
  строки (`interimVisibleLines=3`, высота фиксирована, текст скроллится внутри),
  зона 4 (Transcription History) — 4 строки (`historyVisibleLines=4`); пустые
  Flexed-зоны 2 и 3 занимают всю выделенную Flexed-высоту
  (`gtx.Constraints.Max.Y`). Зона 4 в стартовый каркас не входит (скрыта).

**Параметры окна:**

- Размер: из `.env` (`OVERLAY_WIDTH`, `OVERLAY_HEIGHT`), по умолчанию **800×650**.
  В коде (`NewOverlay`) zero-value дефолты — `Width 1200`, `Height 650`, `FontSize 18`.
- `app.Size(unit.Dp(...))` — размер в dp, `app.Title("")` — пустой заголовок (screen sharing privacy).
- Заголовки зон: **отсутствуют** — только разделители 3px между зонами.
- Языковая пара: `SOURCE_LANG`/`TARGET_LANG` из `.env` (ISO 639-1, дефолт **en→ru**)
  прокидывается через `Config` → dispatcher → `ParseAnswer`/`BuildSystemPrompt`.
- **Межстрочный интервал:** стандартный Gio — `overlayLabel` задаёт `LineHeightScale = 1.0`
  (крутилка `lineSpacingScale` на будущее; `LineHeightScale` масштабирует шрифт, поэтому
  дефолт = стандартный Gio) и **не задаёт** `LineHeight`. Все строки во всех зонах — только
  через `overlayLabel`.
- Позиционирование: `app.TopMost(true)` — поверх других окон.
- Win32: через `findWindowByPID` применяются `WS_EX_NOACTIVATE` и `WS_EX_TRANSPARENT`
  (`setNoActivate`) — окно не крадёт фокус и не ловит мышь. `WS_EX_LAYERED` **не** используется.
- Стили применяются асинхронно после появления нативного окна (`tryApplyStyles`, до 3 с,
  экспоненциальная задержка от 50 мс).

**Автоскролл (всегда в конец):**

- Единая политика для всех скроллируемых зон (1, 2, 3, 4): каждый кадр каждый
  `layout.List` доскролливается в конец — хелпер `autoScrollBottom(list)`
  вызывает `ScrollToEnd`. Счётчики длины (`prevTransLen`/`prevTranscLen`) и
  геттеры `*AtEnd` удалены: они больше не нужны.
- `layout.List` персистентны (поля `interimList`, `translationList`,
  `transcriptionList`, `answersList`), хранят позицию скролла между кадрами, не
  пересоздаются.
- Зона 1 (Interim): `interimList` — один элемент (whole-phrase label без
  `MaxLines`), автоскролл к концу в каждый кадр с текстом; видимая высота
  фиксирована (`interimVisibleLines=3` строк), Rigid.
- **FrameMetrics:** `render()` возвращает `FrameMetrics{SeparatorCount,
  InterimAtEnd, AnswersAtEnd, TranslationsAtEnd, TranscriptionAtEnd}` —
  метрики принадлежат конкретному оверлею и кадру (без глобальных переменных).
  `Run()` значение игнорирует; его читают тесты.

**Кастомный event loop:**

- `app.Main()` **не используется**. `Overlay.Run` вручную дергает `app.Window.Event()`
  в цикле `select` с проверкой `ctx.Done()`.
- На `app.DestroyEvent` — завершение; на `app.FrameEvent` — `render` + `e.Frame`.
- `ctx.Done()` → `w.Perform(system.ActionClose)` и ожидание `app.DestroyEvent` для чистого выхода,
  но не дольше 2 секунд (`destroyEventTimeout`, функция `waitForDestroy`): если окно не ответило —
  принудительный выход с логом `LogDebug`, shutdown не виснет (P7).
- Запуск всех горутин — в `Pipeline.Run`: capture, STT, dispatch, UI, hotkeys; оверлей
  блокируется до отмены контекста или закрытия окна (`WaitShutdown`).

**Хоткеи:**

| Клавиша | Обработчик | Действие |
|---------|------------|----------|
| F1 | `dispatcher.HandleDemand()` (`ActionDemand`) | Запросить/перегенерировать ответ активного вопроса. Цикл: `answer → more context → simpler` (см. ниже). |
| F2 | `dispatcher.HandlePrev()` (`ActionPrev`) | Предыдущий вопрос таймлайна (clamp по границе). Генерацию **не** запускает. |
| F3 | `dispatcher.HandleNext()` (`ActionNext`) | Следующий вопрос таймлайна (clamp по границе). Генерацию **не** запускает. |
| F4 | `overlay.ToggleTranscriptionHistory()` | Тумблер зоны 4 TranscriptionHistory (показ/скрытие). |
| Esc | `dispatcher.Cancel()` | Отмена генерации и очереди; метки таймлайна и готовые ответы **остаются**. |

- **F1** ведёт себя по `State` активной метки: `idle` → обычный ответ; `ready`/`failed` →
  следующий шаг цикла модификаторов (`answer → more context → simpler → answer …`);
  `queued`/`generating` → нажатие игнорируется (коалесинг: во время генерации второе
  задание не ставится). Команды модификаторов хранятся в метке таймлайна — прямых
  биндингов на `CommandMoreContext`/`CommandSimplerEnglish` нет.
- **F2/F3** идут по отдельному каналу `hotkeyCh` (тип `HotkeyAction`, а **не**
  `GenerationCommand`) и только двигают активную метку (шапка в зоне 3 обновляется).
- **Esc** отменяет активную генерацию и очищает очередь; `Queued`/`Generating` метки
  возвращаются в `Idle`, новые вопросы снова встают в очередь.
- **MOD_NOREPEAT:** все биндинги (F1–F4, Esc) регистрируются через Win32
  `RegisterHotKey` с флагом **`MOD_NOREPEAT` (0x4000)** — удержание клавиши не
  повторяет `WM_HOTKEY`.
- F4 обходит dispatcher — `overlay.ToggleTranscriptionHistory()` вызывается напрямую.
- **Полная таблица хоткеев — только в этом файле**; `docs/qa-architecture.md`
  описывает состояния меток и правило F1, но таблицу не дублирует.

**Тест:** `TestWindowStarts` — интеграционный (build-тег `integration`): создаёт оверлей
1200×650, наполняет все 4 зоны (включая 40 строк в History для проверки скролла),
запускает `Run`, проверяет что каждая зона получила данные, положение скролла через
`FrameMetrics` (`InterimAtEnd`, `TranslationsAtEnd`, `TranscriptionAtEnd`), размеры
окна. Автоскролл «всегда в конец» покрыт юнит-тестами `autoscroll_test.go`.

**Языковая пара:** формат подсказок зоны 3 — `SRC: … | TGT: …`, где теги выводятся из
конфигурации (`SOURCE_LANG`/`TARGET_LANG`, ISO 639-1) через `strings.ToUpper`; пустые
значения трактуются как дефолт **en→ru**. Разбор строки — `translator.ParseAnswer`,
промпт — `translator.BuildSystemPrompt(sourceLang, targetLang)`.
