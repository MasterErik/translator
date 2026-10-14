# Архитектура ответов на вопросы (LLM Question Answering)

Как Translator генерирует ответы на вопросы интервью через LLM и как это расширяется до личной базы данных (retrieval / RAG).

## Обзор

Приложение реального времени: распознаёт вопрос собеседника (EN), строит промпт из двух независимых контекстов (факты кандидата + история разговора) и просит LLM сгенерировать ответ от первого лица. Ответы выдаются последовательно через одну горутину-потребитель.

Ключевой принцип: **LLM не хранит память** — контекст собирается на стороне приложения и передаётся в каждом запросе.

## Поток ответа на вопрос

```
EndOfTurn (финальный транскрипт, ChannelID != "translation")
    │
    ▼
translator.IsQuestion(text)?
    ├── нет → ничего
    └── да
        ▼
dispatcher.enqueueQuestion(question)        // метка в QuestionTimeline, неблокирующе
        ▼
AutoAnswer (nil→true)?  ── нет ──▶  метка остаётся QIdle, ответ по F1
        │ да
        ▼
answerCh (chan answerTask, buf N)
        ▼
answerWorker (ОДНА горутина, FIFO)          // приоритет: hotkey/Cancel, иначе answerCh
        │  hotkeyCh (Demand/Prev/Next) и cancelCh (Esc) — отдельные каналы
        ▼
generateAnswers(question, command, idx)     // idx — метка таймлайна
        │
        ├─ ConversationContext = history.BuildContext()   // история интервью
        │
        ▼
AnswerRequest{Question, CandidateContext, ConversationContext, Command, SourceLang, TargetLang}
        ▼
├─ BuildAnswerPrompt(req)          // user prompt
│     "Recent conversation: …\nThe interviewer asked: <question>\n…[модификатор]"
├─ BuildSystemPrompt(srcLang, tgtLang)  // system prompt
│     SystemPromptAnswerGen + "\n\nCandidate context:\n" + candidate
        ▼
llm.GenerateAnswers(ctx, req)
        ▼
parseAnswerHints(content) → ["EN: <en> | RU: <ru>", …]
        ▼
SetAnswer/SetState(idx, QReady) + pushHeader(idx)   // шапка — только если метка активна
        ▼
history.RecordAnswer(question, answers[0])          // только если метка — хвостовая
```

## Контракты

```go
// internal/translator/conversation.go
type GenerationCommand int
const (
    CommandAnswer        GenerationCommand = iota // обычный ответ
    CommandMoreContext                            // ответ с большей историей/контекстом
    CommandSimplerEnglish                         // переформулировка проще по-английски
)
// Команды хранятся в Step метки таймлайна и выдаются по кругу 0→1→2→0
// (NextStep). Прямых хоткеев на CommandMoreContext/CommandSimplerEnglish нет —
// все три выдаются одним F1 (см. «Таймлайн вопросов»).

type ConversationTurn struct {
    Question string
    Answer   string
}

type AnswerRequest struct {
    Question            string           // текущий вопрос
    CandidateContext    string           // постоянные факты кандидата (база CV)
    ConversationContext string           // история интервью (ограниченная)
    Command             GenerationCommand
    SourceLang          string           // ISO 639-1; пусто → дефолт en
    TargetLang          string           // ISO 639-1; пусто → дефолт ru
}

type ConversationHistory struct { … }
func NewConversationHistory(recentTurns, maxContextTokens int) *ConversationHistory
func (h *ConversationHistory) RecordAnswer(question, answer string)
func (h *ConversationHistory) Recent() []ConversationTurn
func (h *ConversationHistory) BuildContext() string

// internal/translator/provider.go
type LLMProvider interface {
    GenerateAnswers(ctx context.Context, req AnswerRequest) ([]string, error)
}

// internal/dispatcher/dispatcher.go
type AnswerGenerator interface {
    GenerateAnswers(ctx context.Context, req translator.AnswerRequest) ([]string, error)
}

// internal/dispatcher/questions.go — таймлайн вопросов
type QState int
const (
    QIdle QState = iota // вопрос обнаружен, генерация не поставлена («idle»)
    QQueued             // вопрос в очереди («queued»)
    QGenerating         // идёт генерация («generating»)
    QReady              // подсказки готовы («ready»)
    QFailed             // генерация упала («failed»)
)

type QEntry struct {
    Idx      int    // АБСОЛЮТНЫЙ номер метки (с 0, не сбрасывается при вытеснении)
    Question string
    Answer   []ui.Answer
    State    QState
    Step     int    // индекс в цикле команд 0=Answer,1=MoreContext,2=SimplerEnglish (mod 3)
    Mode     string // режим последней генерации: "answer"/"more context"/"simpler"
}

type QuestionTimeline struct { … }
func NewQuestionTimeline() *QuestionTimeline
func (t *QuestionTimeline) Add(question string) int          // новая метка (QIdle), tail-follow; возвращает АБСОЛЮТНЫЙ idx
func (t *QuestionTimeline) Active() (int, QEntry, bool)
func (t *QuestionTimeline) Prev() bool                       // F2, clamp по границе
func (t *QuestionTimeline) Next() bool                       // F3, clamp по границе
func (t *QuestionTimeline) OnTail() bool                     // активная метка — последняя?
func (t *QuestionTimeline) HasFresh() (int, bool)            // свежая метка справа? (абсолютный idx)
func (t *QuestionTimeline) ClearFresh()
func (t *QuestionTimeline) SetState(idx int, s QState)
func (t *QuestionTimeline) SetAnswer(idx int, answers []ui.Answer)
func (t *QuestionTimeline) NextStep(idx int) (translator.GenerationCommand, bool)
func (t *QuestionTimeline) SetMode(idx int, mode string)
func (t *QuestionTimeline) ResetPending()                    // Queued/Generating → Idle (Esc)
func (t *QuestionTimeline) ResetStep(idx int)                // Step метки → 0 (дроп задания)
func (t *QuestionTimeline) TotalAsked() int                  // АБСОЛЮТНОЕ число вопросов за сессию
func (t *QuestionTimeline) Snapshot() []QEntry               // глубокие копии

// internal/dispatcher/hotkeys.go — действие функциональной клавиши (НЕ команда генерации)
type HotkeyAction int
const (
    ActionDemand HotkeyAction = iota // F1
    ActionPrev                       // F2
    ActionNext                       // F3
)

// Config (internal/dispatcher)
AutoAnswer *bool // nil→true: вопрос сразу ставит задание CommandAnswer
```

## Два контекста (строго разделены)

| Контекст | Содержание | Куда попадает | Источник |
|---|---|---|---|
| **Candidate Context** | Постоянные факты кандидата: опыт, проекты, технологии, обязанности, достижения | system prompt | файл `candidate_context.md` (`CANDIDATE_CONTEXT_FILE`, gitignored) |
| **Conversation Context** | Уже обсуждавшееся в текущем интервью (Q/A пары) | user prompt | `ConversationHistory` в памяти |

Правила:

1. LLM не придумывает факты сверх Candidate Context — `SystemPromptAnswerGen` прямо требует «Use only information available in the provided candidate context».
2. Информация о компании/вакансии (зарплата, «у нас 500 сотрудников» и т.п.) остаётся в conversation history как реплика интервьюера и НЕ становится фактом кандидата.
3. Динамическая история НЕ помещается в system prompt — только статичный Candidate Context.
4. Candidate Context имеет приоритет над conversation history.

## Fact-level candidate context retrieval (новый режим)

Помимо legacy-передачи полного файла `CANDIDATE_CONTEXT_FILE` целиком, есть режим **fact-level retrieval**:

- При заданном `CANDIDATE_CONTEXT_DIR` приложение строит локальный lexical index по базе фактов кандидата.
- На каждый вопрос из базы отбираются только **точечно релевантные** факты: профиль (краткая сводка) + топ фактов по lexical score.
- Полная база в prompt **не попадает** — в `CandidateContext` идёт только отобранное подмножество, ограниченное бюджетом токенов.

Параметры (секция `candidate_context` в `config.yaml` / переменные `CANDIDATE_CONTEXT_*` в `.env`):

| Параметр | Env-переменная | Default | Назначение |
|---|---|---|---|
| `dir` | `CANDIDATE_CONTEXT_DIR` | `""` | Путь к базе фактов; пусто = legacy (`CANDIDATE_CONTEXT_FILE` целиком) |
| `max_tokens` | `CANDIDATE_CONTEXT_MAX_TOKENS` | `2000` | Бюджет фактов в токенах |
| `max_profile_tokens` | `CANDIDATE_CONTEXT_MAX_PROFILE_TOKENS` | `150` | Бюджет профиля в токенах |
| `min_score` | `CANDIDATE_CONTEXT_MIN_SCORE` | `0` | Минимальный lexical score |
| `top_k` | `CANDIDATE_CONTEXT_TOP_K` | `5` | Максимум фактов в prompt |

Подробнее:
- `docs/candidate-context-architecture.md` — архитектура (индекс, scoring, отбор).
- `docs/candidate-context-format.md` — формат базы фактов.

## Ограничение контекста

```
conversation:
  recent_turns: 6          # максимум turns в context
  max_context_tokens: 4000 # лимит размера (основной критерий)
  auto_answer: true        # авто-генерация при обнаружении вопроса (отсутствует → true)
```

- `auto_answer` (env `CONVERSATION_AUTO_ANSWER`, тип `*bool`): `nil`/отсутствует → **true**, т.е. вопрос сразу ставит задание `CommandAnswer`; `false` — вопрос лишь создаёт метку `QIdle`, ответ генерируется по F1. Указатель, а не `bool`, чтобы отличить «не задано» от «явно выключено».

- Основной критерий — **размер текста**, а не число предложений.
- `common.EstimateTokens(s) = (len(s) + 3) / 4` (файл `internal/common/tokens.go`) — простая оценка (~1 токен на 4 байта). Единственная точка оценки токенов (история и candidate context). Заменяется на tokenizer-реализацию текущей модели, когда она станет доступна.
- При превышении лимита: удаляются **старые** turns (с начала), сохраняются самые свежие; текущий вопрос и Candidate Context не удаляются.
- История не растёт бесконечно: `RecordAnswer` держит максимум `recentTurns` turns.

## Таймлайн вопросов (QuestionTimeline)

Вопросы интервью образуют **таймлайн меток** (`internal/dispatcher/questions.go`) —
история вопросов с состоянием ответа. В зоне 3 UI метка отображается как
`[N]маркер` (см. `docs/UI.md`).

```go
type QEntry struct {
    Idx      int     // АБСОЛЮТНЫЙ номер метки (окно кольца; не сбрасывается при вытеснении)
    Question string
    Answer   []ui.Answer
    State    QState  // QIdle/QQueued/QGenerating/QReady/QFailed
    Step     int     // цикл команд 0→1→2→0 (mod 3)
    Mode     string  // "answer" / "more context" / "simpler"
}
```

### Кольцевой буфер

Таймлайн хранит метки в **кольцевом буфере** вместимости
`questionTimelineCapacity = 150` (~2 часа интервью при ~1 вопросе/мин):

- `entries` — окно меток; `base` — абсолютный номер `entries[0]` (число
  вытесненных меток); внутренняя позиция метки = `idx - base` (`rel`).
- `QEntry.Idx` — **абсолютный** номер метки (с 0), совпадает с тем, что
  возвращает `Add` и принимают `SetState`/`SetAnswer`/`NextStep`/`SetMode`/
  `ResetStep`. Вытеснение головы (`evictLocked`: `base++`) не меняет номера
  оставшихся меток, поэтому задания/шапки по старому абсолютному `idx`
  продолжают указывать на ту же метку (или корректно игнорируются, если метка
  вытеснена).
- `totalAsked` — **абсолютный** счётчик всех `Add` за сессию; `evictLocked` его
  НЕ сбрасывает, поэтому `TotalAsked() ≥ len(Snapshot())`. На нём строится
  UI-контракт `ui.Answer.Total` (шапка «N из M» не «схлопывается» на 150-м
  вопросе).
- Внутренние позиции `active`/`freshAt` — индексы в `entries`; при вытеснении
  `evictLocked` сдвигает их на `-1`, а если вытеснена активная метка — `active`
  указывает на новую голову (ближайшую оставшуюся).
- Наружу возвращаются только значения/копии: `Active`/`Snapshot`/`SetAnswer`
  клонируют `Answer`, поэтому вызывающий не может повредить состояние таймлайна.

### Состояния метки (`QState`)

| Состояние | Когда | Что в зоне 3 |
|---|---|---|
| `QIdle` | вопрос обнаружен, генерация не поставлена (`auto_answer=false`); либо Esc сбросил очередь | «F1 — ответить» |
| `QQueued` | задание поставлено, worker ещё не взял его | «генерация…» |
| `QGenerating` | worker генерирует ответ | «генерация…» |
| `QReady` | ответ получен | строки Source/Target |
| `QFailed` | генерация упала (LLM/парсинг) | «ошибка генерации» (красным) |

### Навигация Prev/Next и tail-follow

- `Active` — **абсолютный** индекс активной метки; `Prev`/`Next` (F2/F3)
  двигают активную позицию с **clamp** по границам окна (на краю — no-op,
  возвращают `false`).
- **Tail-follow**: новая метка становится активной **только если до этого
  активной была прежняя хвостовая** (или таймлайн был пуст) — иначе пользователь
  листает историю и новая метка не должна «утаскивать» вид. Если метка добавлена
  не с хвоста, выставляется `freshAt` (внутренний индекс), который отдаётся
  наружу через `HasFresh() (int, bool)` — в зоне 3 рисуется индикатор `➤` о
  непросмотренной метке справа (`ui.Answer.HasFresh`).
- **Сброс индикатора** — `clearFreshOn`, вызываемый из `Prev`/`Next` при
  попадании на метку `freshAt` (индикатор гаснет, когда пользователь доходит до
  свежей метки). Публичный `ClearFresh` — явный сброс (используется тестами),
  активную позицию не меняет.
- `Snapshot` возвращает **глубокие копии** `QEntry` (включая `Answer`) — worker
  работает со снимком, не гоняясь с мутациями таймлайна.

### Правило F1 (`HandleDemand`)

Поведение F1 зависит от `State` активной метки. Общий busy-guard: если идёт
генерация (`busy`, `atomic.Bool`), F1 игнорируется с debug-логом независимо от
`State` — во время долгой генерации не копятся дубли (см. также
`HandlePrev`/`HandleNext`).

- **`QIdle`** → команда берётся из `NextStep(idx)` (шаг 0 всегда
  `CommandAnswer`), а не хардкодится: `Step` метки мог быть уже сдвинут при
  постановке авто-ответа (`enqueueQuestion` вызывает `NextStep`) или сброшен
  `ResetPending`/дропом очереди. `SetState(QQueued)`, `SetMode(modeOf(cmd))`,
  `enqueueAnswer`.
- **`QReady`/`QFailed`** → выполняется **следующий шаг цикла** модификаторов:
  `0=CommandAnswer → 1=CommandMoreContext → 2=CommandSimplerEnglish`, далее снова `0`
  (`NextStep` по модулю 3). Итоговый `Step` и `Mode` сохраняются в метке.
- **`QQueued`/`QGenerating`** → F1 **игнорируется**: коалесинг. Дополнительно
  `hotkeyCh` неблокирующ (при переполнении действие дропается).

**Сброс `Step` при отмене/дропе задания.** Если задание отдано в `Step`-цикле, но
так и не дошло до LLM (Esc: `ResetPending`; переполнение очереди: `enqueueAnswer`;
дроп очереди: `dropQueue`), `Step` метки сбрасывается в 0 — иначе следующий F1
пропустил бы шаг цикла (регрессия A-1). `ResetPending` сбрасывает `Step` только
у меток, вернувшихся из `QQueued`/`QGenerating` в `QIdle`; `ResetStep(idx)` —
точечный сброс по абсолютному `idx`.

Команды генерации хранятся **в метке** (`Step`/`Mode`), а не приходят из хоткея:
прямых биндингов на `CommandMoreContext`/`CommandSimplerEnglish` больше нет.
`HotkeyAction` (`ActionDemand`/`ActionPrev`/`ActionNext`, `internal/dispatcher/hotkeys.go`)
идёт по отдельному каналу `hotkeyCh` и **не является `GenerationCommand`** —
F2/F3 вообще не запускают генерацию, только двигают активную метку.

### Уточнения (clarifications, фаза 2)

Ручной поток уточнений: интервьюер говорит уточняющую реплику → пользователь
жмёт **Shift+F2** — реплика прикрепляется к активной метке как уточнение
(`Clarifications []string` в `QEntry`), **не** создавая новой метки.

- **`lastFinal`** (`Dispatcher`, собственный мьютекс `lastFinalMu`): каждая
  **финальная реплика интервьюера** (EndOfTurn, вопрос ИЛИ не-вопрос)
  сохраняется как кандидат уточнения. Пустой текст игнорируется.
- **`HandleClarify` (Shift+F2)**: пустой таймлайн или нет `lastFinal` → no-op.
  Иначе сначала `AbsorbLast(lastFinal)` — если последняя метка окна является
  дублем-вопросом (её `Question == lastFinal`), метка удаляется, её текст идёт
  уточнением в предыдущую метку (это решает случай «уточнение распознано как
  вопрос»); если слияние не выполнено — `AddClarification(activeIdx, lastFinal)`.
  Затем шапка активной метки обновляется (`pushHeaderForState`). `lastFinal`
  **потребляется** (очищается) — повторный Shift+F2 без новой фразы = no-op.
- **`HandleUnclarify` (Shift+F3)**: `RemoveLastClarification(activeIdx)` +
  обновление шапки; no-op, если уточнений нет.
- **Генерация**: `AnswerRequest.Clarifications` заполняется из
  `timeline.Clarifications(idx)` во всех путях (auto-answer, F1/`HandleDemand`).
  В `BuildAnswerPrompt` — блок «Clarifications from the interviewer:» после
  вопроса: пустой срез → блок опускается; >3 → **последние 3**. Копии
  `Clarifications` переживают вытеснение кольцевого буфера (глубокие копии в
  `cloneEntry`).
- **UI**: шапка активной метки несёт стек уточнений (`ui.Answer.Clarifications`)
  — в зоне 3 виден тот же состав, что уходит в LLM (рендер — `docs/UI.md`).

### Esc

`Cancel` (Esc) отменяет активную генерацию (`activeCancel`) и очищает очередь
(`cancelled` + `dropQueue`), затем `timeline.ResetPending()` переводит метки
`QQueued`/`QGenerating` → `QIdle` и сбрасывает их `Step` в 0 (отменённое задание
не должно двигать цикл F1 — см. «Правило F1»). **Метки и уже готовые ответы
сохраняются**; новый вопрос снова включает очередь (флаг `cancelled` снимается).

### AutoAnswer и RecordAnswer

- `Config.AutoAnswer *bool`: `nil` → **true** (вопрос сразу ставит задание
  `CommandAnswer`); `false` — метка остаётся `QIdle`, ответ генерируется по F1.
- **`pushHeaderForState`/`RecordAnswer` — только для активной/хвостовой метки.**
  Шапка после генерации отправляется через `pushHeaderForState(idx)`: если
  метка задания хвостовая (`OnTail`), пушится её собственная шапка, иначе —
  шапка активной метки; устаревший ответ не перезаписывает шапку другого вопроса.
  Ответ пишется в `ConversationHistory` (`RecordAnswer`) **только если метка
  хвостовая** (`OnTail`): иначе ответ на старый вопрос стал бы последним turn'ом
  интервью и сломал бы хронологию conversation context.

## Регистрация хоткеев

- Глобальный `RegisterHotKey` (Win32) в `internal/hotkey/` — оверлей
  `WS_EX_NOACTIVATE` не получает клавиатуру, поэтому Gio `key.Event` не приходит.
- Все биндинги (F1–F4, Esc, Shift+F2/F3) регистрируются с **`MOD_NOREPEAT` (0x4000)** —
  удержание клавиши не спамит `WM_HOTKEY` (Shift-биндинги — `MOD_SHIFT|MOD_NOREPEAT`,
  id 1001/1002, маппинг id→`Key` через `keyForID`).
- Маршрутизация (`internal/pipeline`): F1 → `HandleDemand`, F2 → `HandlePrev`,
  F3 → `HandleNext`, Shift+F2 → `HandleClarify`, Shift+F3 → `HandleUnclarify`,
  Esc → `Cancel`; F4 обходит dispatcher —
  `overlay.ToggleTranscriptionHistory()` напрямую. Полная таблица — `docs/UI.md`.
- Модификаторы команд добавляются в `BuildAnswerPrompt` через `commandInstruction(cmd)`.

## Очередь (последовательная обработка)

- Один `answerWorker` обрабатывает и вопросы, и команды генерации **последовательно** (FIFO) — без параллельных LLM-запросов. `hotkeyCh` (Demand/Prev/Next) и `cancelCh` (Esc) — отдельные каналы, `select` обрабатывает их вне очереди заданий (навигация/отмена не ждут генерацию).
- Причина: Groq free tier возвращает **429** при частых запросах (rate-limit TPM).
- Неблокирующая отправка в `answerCh`/`hotkeyCh`: при переполнении задание/действие дропается с debug-логом.
- Коалесинг F1: во время генерации (`busy`) повторный `HandleDemand` не ставит второе задание.
- Shutdown: `Run` ждёт `<-answerDone` ПЕРЕД `close(answerCh)`; `workerCtx` отменяется при выходе `Run`, worker дренирует очередь.

## Точки расширения → личная база данных (retrieval / RAG)

Текущая архитектура — базис для ответов по личной базе данных:

```
Личная база данных (проекты / знания / документы кандидата)
        │
        ▼  semantic retrieval (будущее)
Relevant Projects / Relevant Context
        │
        ▼
AnswerRequest.CandidateContext      ← добавляется как НОВЫЙ источник фактов
        │
        ▼
Prompt → LLM → answer
```

Что можно добавить, не ломая структуру:

1. **Semantic retrieval** — выбор релевантных проектов/фактов из личной БД → подмешивание в `CandidateContext` (или отдельное поле `RelevantContext`).
2. **Tokenizer-based оценка** — замена `common.EstimateTokens` на точный tokenizer модели.
3. **Conversation summary** — LLM-саммари старой истории вместо простого отбрасывания (сейчас — только последние `recent_turns` turns).
4. **Индексация документов** — личная БД = набор проиндексированных документов (CV, проекты, заметки), по которым ищется контекст под вопрос.

Принцип: retrieval — это **источник** Candidate Context, а не новый pipeline. Вопрос → IsQuestion → (retrieval →) AnswerRequest → Prompt → LLM.
