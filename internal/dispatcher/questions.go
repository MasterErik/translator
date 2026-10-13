package dispatcher

import (
	"sync"

	"github.com/mastererik/translator/internal/translator"
	"github.com/mastererik/translator/internal/ui"
)

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
	Question string
	Answer   []ui.Answer
	State    QState
	Step     int
	// Mode — режим последней генерации для этой метки ("answer", "more context",
	// "simpler"). Заполняется dispatcher'ом при постановке задания/ответе и
	// копируется в Snapshot; нужен, чтобы Prev/Next могли показать шапку
	// существующей метки, не зная команду (F1/F2/F3).
	Mode string
}

// QuestionTimeline — потокобезопасный таймлайн обнаруженных вопросов интервью.
//
// Метки добавляются по мере обнаружения вопросов (Add). Пользователь может
// перемещаться по таймлайну (Prev/Next). Если пользователь находился на
// последней метке (tail-follow), новая метка автоматически становится активной;
// иначе позиция не двигается, но запоминается freshAt для индикатора «новая
// метка».
//
// Все методы потокобезопасны. Наружу возвращаются только значения/копии —
// внутренние слайсы не алиасятся, поэтому вызывающий не может случайно
// повредить состояние таймлайна.
type QuestionTimeline struct {
	mu      sync.Mutex
	entries []QEntry
	active  int
	// freshAt — индекс последней добавленной метки, если она ещё не была
	// просмотрена (-1 если индикатора нет). Сбрасывается при переходе на эту
	// метку в Prev/Next или явно методом ClearFresh.
	freshAt int
}

// NewQuestionTimeline создаёт пустой таймлайн.
func NewQuestionTimeline() *QuestionTimeline {
	return &QuestionTimeline{
		active:  0,
		freshAt: -1,
	}
}

// Add добавляет вопрос в конец таймлайна в состоянии QIdle и возвращает его индекс.
//
// Активной новая метка становится ТОЛЬКО если до добавления пользователь был на
// последней метке (tail-follow) или таймлайн был пуст. Иначе активная позиция не
// двигается, но запоминается freshAt — для индикатора «появилась новая метка».
func (t *QuestionTimeline) Add(question string) int {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.entries = append(t.entries, QEntry{
		Question: question,
		State:    QIdle,
	})
	idx := len(t.entries) - 1

	if idx == 0 || t.active == len(t.entries)-2 {
		// Таймлайн был пуст либо пользователь был на прежней последней метке.
		t.active = idx
		t.freshAt = -1
	} else {
		t.freshAt = idx
	}

	return idx
}

// Active возвращает индекс, копию метки и ok=false для пустого таймлайна.
func (t *QuestionTimeline) Active() (int, QEntry, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.entries) == 0 {
		return 0, QEntry{}, false
	}
	return t.active, cloneEntry(t.entries[t.active]), true
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

// HasFresh сообщает, есть ли индикатор новой метки, и её индекс.
// Индикатор сбрасывается при переходе пользователя на метку freshAt (в Prev/Next)
// или методом ClearFresh.
func (t *QuestionTimeline) HasFresh() (int, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.freshAt < 0 {
		return 0, false
	}
	return t.freshAt, true
}

// ClearFresh сбрасывает индикатор новой метки. Активную позицию не меняет.
func (t *QuestionTimeline) ClearFresh() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.freshAt = -1
}

// SetState устанавливает состояние метки. No-op при неверном idx.
func (t *QuestionTimeline) SetState(idx int, s QState) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if idx < 0 || idx >= len(t.entries) {
		return
	}
	t.entries[idx].State = s
}

// SetAnswer сохраняет подсказки для метки. Слайс копируется внутрь —
// последующая мутация исходного слайса не влияет на таймлайн.
// No-op при неверном idx.
func (t *QuestionTimeline) SetAnswer(idx int, answers []ui.Answer) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if idx < 0 || idx >= len(t.entries) {
		return
	}
	t.entries[idx].Answer = cloneAnswers(answers)
}

// NextStep возвращает команду цикла по текущему Step метки и инкрементирует Step
// по модулю 3 (0=CommandAnswer, 1=CommandMoreContext, 2=CommandSimplerEnglish).
// ok=false при неверном idx.
func (t *QuestionTimeline) NextStep(idx int) (translator.GenerationCommand, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if idx < 0 || idx >= len(t.entries) {
		return translator.CommandAnswer, false
	}

	cmd := stepCommand(t.entries[idx].Step)
	t.entries[idx].Step = (t.entries[idx].Step + 1) % 3
	return cmd, true
}

// SetMode записывает режим последней генерации для метки. No-op при неверном idx.
func (t *QuestionTimeline) SetMode(idx int, mode string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if idx < 0 || idx >= len(t.entries) {
		return
	}
	t.entries[idx].Mode = mode
}

// ResetPending переводит все метки в состоянии QQueued/QGenerating обратно в
// QIdle (Esc: очередь заданий дропается). Готовые/упавшие/праздные метки и их
// ответы сохраняются.
func (t *QuestionTimeline) ResetPending() {
	t.mu.Lock()
	defer t.mu.Unlock()

	for i := range t.entries {
		if t.entries[i].State == QQueued || t.entries[i].State == QGenerating {
			t.entries[i].State = QIdle
		}
	}
}

// Snapshot возвращает глубокую копию всех меток, включая копии слайсов Answer.
// Активная позиция не меняется.
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

// cloneEntry возвращает копию метки с копией слайса Answer.
func cloneEntry(e QEntry) QEntry {
	out := e
	out.Answer = cloneAnswers(e.Answer)
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
