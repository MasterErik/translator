package dispatcher

import (
	"testing"
	"time"
)

// ── F4: интеграция handleQuestion (дедуп A6 + коалесинг A7) через route ───

// TestFuzzyDuplicateQuestionDropped — фаззи-дубликат вопроса не создаёт
// вторую метку и не запускает генерацию (A6).
func TestFuzzyDuplicateQuestionDropped(t *testing.T) {
	engine := &mockEngine{answers: []string{"EN: A | RU: A"}}
	d, _, textStream, cancel := startDispatcherWithOverlay(t, engine, DefaultConfig())
	defer cancel()

	textStream <- question("How do you scale?")
	waitFor(t, func() bool { return len(engine.Calls()) == 1 }, 2*time.Second)

	// STT переиздал вопрос с вставными стоп-фразами и добавкой «it»:
	// Jaccard {how,do,you,scale,it} x {how,do,you,scale} = 0.8 >= 0.75 → дроп.
	textStream <- question("So, ok, how do you scale it?")

	waitIdle(t, d)
	if got := len(snapshotOf(d)); got != 1 {
		t.Fatalf("фаззи-дубликат создал метку: меток %d, ожидалась 1", got)
	}
	if got := len(engine.Calls()); got != 1 {
		t.Errorf("дубликат запустил генерацию: вызовов LLM %d, ожидался 1", got)
	}
}

// TestCoalesceQuestionWhileGenerating — пока метка генерируется (QGenerating),
// фрагмент-дополнение («And what about latency?») дописывается к её вопросу и
// задание перегенерируется полным текстом; отдельная метка НЕ создаётся (A7).
func TestCoalesceQuestionWhileGenerating(t *testing.T) {
	release := make(chan struct{})
	engine := &blockingEngine{answers: []string{"EN: A | RU: A"}, release: release}
	d, _, textStream, cancel := startDispatcherWithOverlay(t, engine, DefaultConfig())
	defer cancel()

	textStream <- question("How do you scale?")
	waitFor(t, func() bool { return len(engine.Calls()) == 1 }, 2*time.Second)

	// Фрагмент приходит, пока генерация блокирована.
	textStream <- question("And what about latency?")

	// Допишется к вопросу метки; новой метки не будет.
	waitFor(t, func() bool {
		snap := snapshotOf(d)
		if len(snap) != 1 {
			return false
		}
		return snap[0].Question == "How do you scale? And what about latency?"
	}, 2*time.Second)

	close(release)

	// Двойной вызов: старое задание (короткий вопрос) + перегенерация полная.
	waitFor(t, func() bool {
		calls := engine.Calls()
		return len(calls) == 2 && calls[1] == "How do you scale? And what about latency?"
	}, 2*time.Second)

	waitIdle(t, d)
	if got := len(snapshotOf(d)); got != 1 {
		t.Errorf("фрагмент породил лишнюю метку: меток %d, ожидалась 1", got)
	}
}

// TestCoalesceLateFragmentBecomesClarification — фрагмент, пришедший ПОСЛЕ
// готового ответа (QReady) и всё ещё в coalesceWindow, прикрепляется как
// уточнение метки; новой метки нет (A7, «late-фрагменты идут как уточнение»).
func TestCoalesceLateFragmentBecomesClarification(t *testing.T) {
	engine := &mockEngine{answers: []string{"EN: A | RU: A"}}
	d, _, textStream, cancel := startDispatcherWithOverlay(t, engine, DefaultConfig())
	defer cancel()

	textStream <- question("How do you scale?")
	waitFor(t, func() bool {
		snap := snapshotOf(d)
		return len(snap) == 1 && snap[0].State == QReady
	}, 2*time.Second)

	textStream <- question("What about the latency?")

	waitFor(t, func() bool {
		return len(d.timeline.Clarifications(0)) == 1
	}, 2*time.Second)

	if got := len(snapshotOf(d)); got != 1 {
		t.Fatalf("поздний фрагмент породил метку: меток %d, ожидалась 1", got)
	}
}

// TestDistinctQuickQuestionStaysSeparate — самостоятельный вопрос, заданный
// сразу после предыдущего (даже within coalesceWindow), НЕ склеивается:
// guard followUpFragment отсекает ложные склейки разных вопросов.
func TestDistinctQuickQuestionStaysSeparate(t *testing.T) {
	engine := &mockEngine{answers: []string{"EN: A | RU: A"}}
	d, _, textStream, cancel := startDispatcherWithOverlay(t, engine, DefaultConfig())
	defer cancel()

	textStream <- question("How do you scale?")
	textStream <- question("What is your experience?")

	waitFor(t, func() bool { return len(snapshotOf(d)) == 2 }, 2*time.Second)
	waitIdle(t, d)

	reqs := engine.Reqs()
	if len(reqs) != 2 {
		t.Fatalf("вызовов генерации %d, ожидалось 2", len(reqs))
	}
	if reqs[0].Question != "How do you scale?" || reqs[1].Question != "What is your experience?" {
		t.Errorf("генерации: %q, %q — вопросы не должны вмешиваться друг в друга", reqs[0].Question, reqs[1].Question)
	}
}

// TestCoalesceConsumesFragmentRemark — реплика-фрагмент, вошедшая в текст
// вопроса при склейке, потребляется в SpeechLog: она не возвращается уточнением
// и не остаётся unconsumed для Shift+F2.
func TestCoalesceConsumesFragmentRemark(t *testing.T) {
	release := make(chan struct{})
	engine := &blockingEngine{answers: []string{"EN: A | RU: A"}, release: release}
	d, _, textStream, cancel := startDispatcherWithOverlay(t, engine, DefaultConfig())
	defer cancel()
	defer close(release)

	textStream <- question("How do you scale?")
	waitFor(t, func() bool { return len(engine.Calls()) == 1 }, 2*time.Second)

	textStream <- question("And what about latency?")
	// Потребление в SpeechLog идёт в route синхронно со склейкой: ждём сам
	// факт склейки (полный вопрос в метке), реплика уже покет — состояние
	// SpeechLog в этот момент детерминировано конвейером.route.
	waitFor(t, func() bool {
		snap := snapshotOf(d)
		return len(snap) == 1 && snap[0].Question == "How do you scale? And what about latency?"
	}, 2*time.Second)

	// В SpeechLog из unconsumed остаётся только сам вопрос метки; фрагмент
	// потреблён ConsumeText.
	waitFor(t, func() bool {
		unconsumed := d.speechLog.SnapshotAfter(time.Time{})
		return len(unconsumed) == 1 && unconsumed[0].Text == "How do you scale?"
	}, 2*time.Second)
}
