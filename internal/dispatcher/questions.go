package dispatcher

import (
	"sync"

	"github.com/mastererik/translator/internal/translator"
	"github.com/mastererik/translator/internal/ui"
)

// questionTimelineCapacity — предел числа хранимых меток (вопросов) таймлайна.
// ≈2 часа интервью при ~1 вопросе/мин: более старые метки бесполезны, а их
// вытеснение не ломает нумерацию — внешние индексы абсолютные (см. base).
const questionTimelineCapacity = 150

// QState — состояние генерации подсказки для одной метки таймлайна вопросов.
type QState int

const (
	// QIdle — вопрос обнаружен, генерация ещё не поставлена в очередь.
	QIdle QState = iota
	// QQueued — вопрос в очереди на генерацию.
	QQueued
	// QGenerating — идёт генерация ответа.
	QGenerating
	// QReady — подсказки сгенерированы и доступны.
	QReady
	// QFailed — генерация завершилась ошибкой.
	QFailed
)

// String возвращает строчное представление состояния (для UI-контракта Answer.State).
func (s QState) String() string {
	switch s {
	case QIdle:
		return "idle"
	case QQueued:
		return "queued"
	case QGenerating:
		return "generating"
	case QReady:
		return "ready"
	case QFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// QEntry — одна метка (вопрос) в таймлайне вместе с её состоянием и ответами.
//
// Step — индекс в цикле команд F1→F2→F3 (0=CommandAnswer, 1=CommandMoreContext,
// 2=CommandSimplerEnglish, далее по модулю 3). Хранится в самой метке, а не
// глобально: каждая метка помнит, на какой команде цикла она остановилась.
type QEntry struct {
	// Idx — абсолютный номер метки (нумерация с 0, не сбрасывается при
	// вытеснении старых меток из кольца). Совпадает с тем, что возвращает Add
	// и принимают SetState/SetAnswer/NextStep/SetMode.
	Idx      int
	Question string
	Answer   []ui.Answer
	State    QState
	Step     int
	// Mode — режим последней генерации для этой метки ("answer", "more context",
	// "simpler"). Заполняется dispatcher'ом при постановке задания/ответе и
	// копируется в Snapshot; нужен, чтобы Prev/Next могли показать шапку
	// существующей метки, не зная команду (F1/F2/F3).
	Mode string
	// Clarifications — стек уточнений к вопросу (в порядке добавления, хвост —
	// последнее). Живёт в самой метке: при вытеснении кольца уезжает вместе с
	// ней, а AbsorbLast переносит стек метки-дубля в предыдущую метку.
	// Наружу отдаётся только копией (Clarifications/Snapshot/Active).
	Clarifications []string
}

// QuestionTimeline — потокобезопасный таймлайн обнаруженных вопросов интервью.
//
// Метки добавляются по мере обнаружения вопросов (Add). Пользователь может
// перемещаться по таймлайну (Prev/Next). Если пользователь находился на
// последней метке (tail-follow), новая метка автоматически становится активной;
// иначе позиция не двигается, но запоминается freshAt для индикатора «новая
// метка».
//
// Внешние индексы АБСОЛЮТНЫЕ и стабильны: base — номер метки entries[0];
// хранимые позиции внутренние (active/freshAt — индекс в entries, а не номер
// метки). При переполнении (questionTimelineCapacity) вытесняется голова
// entries[0], base инкрементируется — номера оставшихся меток не меняются,
// поэтому задания/шапки, поставленные по старому абсолютному idx, продолжают
// указывать на ту же метку (или корректно игнорируются, если метка вытеснена).
//
// Все методы потокобезопасны. Наружу возвращаются только значения/копии —
// внутренние слайсы не алиасятся, поэтому вызывающий не может случайно
// повредить состояние таймлайна.
type QuestionTimeline struct {
	mu      sync.Mutex
	entries []QEntry
	// base — абсолютный номер entries[0] (число вытесненных меток). Внутренняя
	// позиция метки с абсолютным номером idx: idx - base.
	base int
	// totalAsked — АБСОЛЮТНОЕ число вопросов за сессию (счётчик всех Add).
	// НЕ сбрасывается при вытеснении головы evictLocked и поэтому больше либо
	// равно len(entries): внешний UI-контракт Answer.Total не должен «схлопываться»
	// на 150-м вопросе.
	totalAsked int
	active     int
	// freshAt — внутренняя позиция последней добавленной метки, если она ещё не
	// была просмотрена (-1 если индикатора нет). Сбрасывается при переходе на
	// эту метку в Prev/Next или явно методом ClearFresh.
	freshAt int
	// capacity — предел числа хранимых меток; 0 (нулевое значение структуры)
	// означает questionTimelineCapacity.
	capacity int
}

// NewQuestionTimeline создаёт пустой таймлайн вместимости questionTimelineCapacity.
func NewQuestionTimeline() *QuestionTimeline {
	return &QuestionTimeline{
		base:     0,
		active:   0,
		freshAt:  -1,
		capacity: questionTimelineCapacity,
	}
}

// capLimit возвращает эффективную вместимость таймлайна (под удерживаемым
// мьютексом): нулевое поле capacity трактуется как questionTimelineCapacity.
func (t *QuestionTimeline) capLimit() int {
	if t.capacity <= 0 {
		return questionTimelineCapacity
	}
	return t.capacity
}

// rel переводит абсолютный номер метки во внутреннюю позицию.
// ok=false — метка вытеснена из кольца (idx < base) или ещё не существует
// (idx > последний абсолютный номер).
// Вызывается под удерживаемым мьютексом.
func (t *QuestionTimeline) rel(idx int) (int, bool) {
	r := idx - t.base
	if r < 0 || r >= len(t.entries) {
		return 0, false
	}
	return r, true
}

// --- уточнения (фаза 2, часть Б: Б-T1) ---

// AddClarification добавляет уточнение text к метке с АБСОЛЮТНЫМ номером idx.
// Возвращает false (no-op), если метка вытеснена из кольца / не существует либо
// text пуст. Стек уточнений живёт в самой QEntry и уезжает вместе с меткой при
// вытеснении — отдельной синхронизации не требует.
func (t *QuestionTimeline) AddClarification(idx int, text string) bool {
	if text == "" {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	e := t.lookupLocked(idx)
	if e == nil {
		return false
	}
	e.Clarifications = append(e.Clarifications, text)
	return true
}

// RemoveLastClarification убирает последнее уточнение метки idx. Возвращает
// false (no-op), если метки нет в окне или у неё нет уточнений. После удаления
// последнего уточнения срез сбрасывается в nil — контракт Clarifications().
func (t *QuestionTimeline) RemoveLastClarification(idx int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	e := t.lookupLocked(idx)
	if e == nil || len(e.Clarifications) == 0 {
		return false
	}
	e.Clarifications = e.Clarifications[:len(e.Clarifications)-1]
	if len(e.Clarifications) == 0 {
		e.Clarifications = nil
	}
	return true
}

// Clarifications возвращает КОПИЮ стека уточнений метки idx (nil для пустого
// стека и для метки вне окна). Копия не алиасит внутренний срез.
func (t *QuestionTimeline) Clarifications(idx int) []string {
	t.mu.Lock()
	defer t.mu.Unlock()

	e := t.lookupLocked(idx)
	if e == nil || len(e.Clarifications) == 0 {
		return nil
	}
	return cloneClarifications(e.Clarifications)
}

// AbsorbLast — слияние реплики-дубля вопроса. Если ПОСЛЕДНЯЯ метка окна — дубль
// вопроса q (её Question == q), она удаляется, а её текст добавляется уточнением
// к предыдущей метке (idx-1). Активность и индикатор свежести пересчитываются:
// указатель, смотревший на удалённую метку, смещается на слитую.
// Возвращает (новый абсолютный индекс активной метки, true).
//
// Слияние невозможно (no-op, false), если последней метки нет, метка всего одна
// (сливать не с чем) или её Question != q. В этом случае возвращается текущий
// хвостовой абсолютный idx.
func (t *QuestionTimeline) AbsorbLast(q string) (int, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	n := len(t.entries)
	if n == 0 {
		return 0, false
	}
	lastAbs := t.base + n - 1 // абсолютный номер последней метки окна
	if n < 2 || t.entries[n-1].Question != q {
		return lastAbs, false
	}

	// Слияние: текст удаляемой метки становится уточнением предыдущей.
	absorbed := t.entries[n-1]
	prev := &t.entries[n-2]
	if absorbed.Question != "" {
		prev.Clarifications = append(prev.Clarifications, absorbed.Question)
	}

	// Удаляем только хвост буфера: у него внутренняя позиция n-1, поэтому
	// удаление сводится к усечению entries — Clarifications, Step и State
	// остальных меток не сдвигаются (в отличие от evictLocked, который режет
	// голову и сдвигает base).
	t.entries = t.entries[:n-1]
	absorbedPos := n - 1 // внутренняя позиция удалённой метки

	// Активность: указатель, смотревший на удалённую метку, переносится на
	// слитую (та же позиция — последняя оставшаяся); иначе clamp в новое окно,
	// чтобы active не вышел за пределы буфера.
	if t.active == absorbedPos {
		t.active = absorbedPos - 1
	}
	t.clampActiveLocked()

	// Индикатор свежести: если он указывал на удалённую метку, свежести больше
	// нет; прочие значения clamp'ятся в новое окно.
	if t.freshAt == absorbedPos {
		t.freshAt = -1
	}
	t.clampFreshLocked()

	// Нумерация не «схлопывается» и не переиспользует номер удалённой метки:
	// следующий Add получит idx = base + len(entries) == lastAbs, но это уже
	// новый вопрос (totalAsked продолжает расти).
	return t.base + t.active, true
}

// lookupLocked возвращает метку с абсолютным номером idx или nil, если idx
// вытеснен из окна кольца либо ещё не существует. Вызывается под мьютексом.
func (t *QuestionTimeline) lookupLocked(idx int) *QEntry {
	r, ok := t.rel(idx)
	if !ok {
		return nil
	}
	return &t.entries[r]
}

// clampActiveLocked удерживает active в границах буфера. Вызывается под мьютексом.
func (t *QuestionTimeline) clampActiveLocked() {
	if len(t.entries) == 0 {
		return
	}
	if t.active < 0 {
		t.active = 0
	}
	if t.active > len(t.entries)-1 {
		t.active = len(t.entries) - 1
	}
}

// clampFreshLocked удерживает freshAt в границах буфера либо сбрасывает его
// (-1), если индикатора нет. Вызывается под мьютексом.
func (t *QuestionTimeline) clampFreshLocked() {
	if t.freshAt < 0 {
		t.freshAt = -1
		return
	}
	if len(t.entries) == 0 || t.freshAt > len(t.entries)-1 {
		t.freshAt = -1
	}
}

// Add добавляет вопрос в конец таймлайна в состоянии QIdle и возвращает его
// АБСОЛЮТНЫЙ индекс (нумерация не сбрасывается при вытеснении головы).
//
// Активной новая метка становится ТОЛЬКО если до добавления пользователь был на
// последней метке (tail-follow) или таймлайн был пуст. Иначе активная позиция не
// двигается, но запоминается freshAt — для индикатора «появилась новая метка».
//
// При переполнении вытесняется самая старая метка (голова): base++, внутренние
// позиции active/freshAt сдвигаются на -1; если вытеснена активная метка, active
// указывает на новую голову (ближайшую оставшуюся).
func (t *QuestionTimeline) Add(question string) int {
	t.mu.Lock()
	defer t.mu.Unlock()

	idx := t.base + len(t.entries)
	t.totalAsked++
	t.entries = append(t.entries, QEntry{
		Idx:      idx,
		Question: question,
		State:    QIdle,
	})

	if len(t.entries) == 1 || t.active == len(t.entries)-2 {
		// Таймлайн был пуст либо пользователь был на прежней последней метке.
		t.active = len(t.entries) - 1
		t.freshAt = -1
	} else {
		t.freshAt = len(t.entries) - 1
	}

	t.evictLocked()
	return idx
}

// evictLocked вытесняет метки-головы, пока число хранимых меток не уложится в
// вместимость, сдвигая base и внутренние позиции. Вызывается под мьютексом.
func (t *QuestionTimeline) evictLocked() {
	limit := t.capLimit()
	for len(t.entries) > limit {
		t.entries = t.entries[1:]
		t.base++
		if t.active > 0 {
			t.active--
		} else {
			// Вытеснена активная метка: активной становится новая голова —
			// ближайшая оставшаяся (наиболее вероятно связанная с текущим
			// контекстом интервью). Просмотр истории — неявно в начало.
			t.active = 0
		}
		if t.freshAt >= 0 {
			t.freshAt--
		}
	}
}

// Active возвращает АБСОЛЮТНЫЙ индекс активной метки, её копию и ok=false для
// пустого таймлайна.
func (t *QuestionTimeline) Active() (int, QEntry, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.entries) == 0 {
		return 0, QEntry{}, false
	}
	return t.base + t.active, cloneEntry(t.entries[t.active]), true
}

// Prev переходит на предыдущую метку с clamp по границе.
// Возвращает true, если позиция изменилась. State/Step не трогаются.
func (t *QuestionTimeline) Prev() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.entries) == 0 || t.active == 0 {
		return false
	}
	t.active--
	t.clearFreshOn(t.active)
	return true
}

// Next переходит на следующую метку с clamp по границе.
// Возвращает true, если позиция изменилась. State/Step не трогаются.
func (t *QuestionTimeline) Next() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.entries) == 0 || t.active >= len(t.entries)-1 {
		return false
	}
	t.active++
	t.clearFreshOn(t.active)
	return true
}

// OnTail сообщает, является ли активная метка последней.
// Для пустого таймлайна возвращает true (tail-follow включён).
func (t *QuestionTimeline) OnTail() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.active >= len(t.entries)-1
}

// HasFresh сообщает, есть ли индикатор новой метки, и её АБСОЛЮТНЫЙ индекс.
// Индикатор сбрасывается при переходе пользователя на метку freshAt (в Prev/Next)
// или методом ClearFresh.
func (t *QuestionTimeline) HasFresh() (int, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.freshAt < 0 || t.freshAt >= len(t.entries) {
		return 0, false
	}
	return t.base + t.freshAt, true
}

// ClearFresh сбрасывает индикатор новой метки. Активную позицию не меняет.
func (t *QuestionTimeline) ClearFresh() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.freshAt = -1
}

// SetState устанавливает состояние метки по АБСОЛЮТНОМУ idx.
// No-op при неверном/вытесненном idx.
func (t *QuestionTimeline) SetState(idx int, s QState) {
	t.mu.Lock()
	defer t.mu.Unlock()

	r, ok := t.rel(idx)
	if !ok {
		return
	}
	t.entries[r].State = s
}

// SetAnswer сохраняет подсказки для метки по АБСОЛЮТНОМУ idx. Слайс копируется
// внутрь — последующая мутация исходного слайса не влияет на таймлайн.
// No-op при неверном/вытесненном idx.
func (t *QuestionTimeline) SetAnswer(idx int, answers []ui.Answer) {
	t.mu.Lock()
	defer t.mu.Unlock()

	r, ok := t.rel(idx)
	if !ok {
		return
	}
	t.entries[r].Answer = cloneAnswers(answers)
}

// NextStep возвращает команду цикла по текущему Step метки (абсолютный idx) и
// инкрементирует Step по модулю 3 (0=CommandAnswer, 1=CommandMoreContext,
// 2=CommandSimplerEnglish). ok=false при неверном/вытесненном idx.
func (t *QuestionTimeline) NextStep(idx int) (translator.GenerationCommand, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	r, ok := t.rel(idx)
	if !ok {
		return translator.CommandAnswer, false
	}

	cmd := stepCommand(t.entries[r].Step)
	t.entries[r].Step = (t.entries[r].Step + 1) % 3
	return cmd, true
}

// SetMode записывает режим последней генерации для метки по АБСОЛЮТНОМУ idx.
// No-op при неверном/вытесненном idx.
func (t *QuestionTimeline) SetMode(idx int, mode string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	r, ok := t.rel(idx)
	if !ok {
		return
	}
	t.entries[r].Mode = mode
}

// TotalAsked возвращает АБСОЛЮТНОЕ число вопросов, добавленных за сессию.
// Счётчик не сбрасывается при вытеснении меток из кольца, поэтому он всегда
// больше либо равен len(Snapshot()) — на нём строится UI-контракт Answer.Total.
func (t *QuestionTimeline) TotalAsked() int {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.totalAsked
}

// ResetPending переводит все метки в состоянии QQueued/QGenerating обратно в
// QIdle (Esc: очередь заданий дропается). Готовые/упавшие/праздные метки и их
// ответы сохраняются. Step метки тоже сбрасывается в 0: отменённая (или
// дропнутая) постановка задания не должна двигать цикл команд — иначе после Esc
// первый F1 пропустит CommandAnswer/CommandMoreContext (см. handleDemand).
func (t *QuestionTimeline) ResetPending() {
	t.mu.Lock()
	defer t.mu.Unlock()

	for i := range t.entries {
		if t.entries[i].State == QQueued || t.entries[i].State == QGenerating {
			t.entries[i].State = QIdle
			t.entries[i].Step = 0
		}
	}
}

// ResetStep сбрасывает Step метки по АБСОЛЮТНОМУ idx в 0. Вызывается, когда
// задание дропнуто (переполнение очереди / отмена): команда так и не была
// отдана LLM, поэтому цикл F1 не должен считаться продвинутым.
// No-op при неверном/вытесненном idx.
func (t *QuestionTimeline) ResetStep(idx int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	r, ok := t.rel(idx)
	if !ok {
		return
	}
	t.entries[r].Step = 0
}

// Snapshot возвращает глубокую копию всех хранимых меток (окно кольца), включая
// копии слайсов Answer. Idx каждой метки — её абсолютный номер, поэтому сдвиг
// окна (base) не сбивает нумерацию. Активная позиция не меняется.
func (t *QuestionTimeline) Snapshot() []QEntry {
	t.mu.Lock()
	defer t.mu.Unlock()

	out := make([]QEntry, len(t.entries))
	for i, e := range t.entries {
		out[i] = cloneEntry(e)
	}
	return out
}

// clearFreshOn сбрасывает индикатор, если пользователь перешёл на метку freshAt.
// Вызывается под удерживаемым мьютексом.
func (t *QuestionTimeline) clearFreshOn(idx int) {
	if t.freshAt >= 0 && idx == t.freshAt {
		t.freshAt = -1
	}
}

// stepCommand отображает Step (индекс в цикле команд) в GenerationCommand.
func stepCommand(step int) translator.GenerationCommand {
	switch step {
	case 1:
		return translator.CommandMoreContext
	case 2:
		return translator.CommandSimplerEnglish
	default:
		return translator.CommandAnswer
	}
}

// cloneEntry возвращает копию метки с копиями слайсов Answer и Clarifications.
func cloneEntry(e QEntry) QEntry {
	out := e
	out.Answer = cloneAnswers(e.Answer)
	out.Clarifications = cloneClarifications(e.Clarifications)
	return out
}

// cloneClarifications возвращает копию слайса уточнений (nil для пустого входа).
func cloneClarifications(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

// cloneAnswers возвращает копию слайса Answer (nil для пустого входа).
func cloneAnswers(in []ui.Answer) []ui.Answer {
	if len(in) == 0 {
		return nil
	}
	out := make([]ui.Answer, len(in))
	copy(out, in)
	return out
}
