package dispatcher

import (
	"context"
	"testing"
	"time"

	"github.com/mastererik/translator/internal/common"
)

// ── F12.2: debounce постановки заданий (1 вопрос = 1 LLM-вызов) ─────────────

// pendingCount — thread-safe чтение размера pending (тестовая горутина
// не должна читать map без pendingMu — иначе race detector ругается).
func pendingCount(d *Dispatcher) int {
	d.pendingMu.Lock()
	defer d.pendingMu.Unlock()
	return len(d.pending)
}

// newDebounceTestDispatcher — диспетчер с коротким окном дебаунса и полным
// production-путём (Run + textStream).
func newDebounceTestDispatcher(t *testing.T, debounce time.Duration) (*Dispatcher, *mockEngine, *mockOverlay, chan common.STTEvent, chan struct{}, context.CancelFunc) {
	t.Helper()
	engine := &mockEngine{answers: []string{"EN: a | RU: а"}}
	overlay := &mockOverlay{}
	d := New(overlay, engine, &mockLogger{}, Config{
		AnswerTimeout:  12 * time.Second,
		AnswerDebounce: debounce,
	})
	ctx, cancel := context.WithCancel(context.Background())
	textStream := make(chan common.STTEvent, 8)
	done := make(chan struct{}, 1)
	go d.Run(ctx, textStream, done)
	t.Cleanup(cancel)
	return d, engine, overlay, textStream, done, cancel
}

func sendQuestion(t *testing.T, textStream chan common.STTEvent, text string) {
	t.Helper()
	textStream <- common.STTEvent{
		Event:     common.EventEndOfTurn,
		ChannelID: "speaker",
		Text:      text,
		Timestamp: time.Now(),
	}
}

// TestDebounceRefinedVersionSingleGeneration — refined-версия вопроса (fuzzy
// дубликат, повторная транскрипция того же высказывания), прибывшая в окне
// дебаунса, ОБНОВЛЯЕТ задание вместо второй генерации: один LLM-вызов с
// финальным текстом.
func TestDebounceRefinedVersionSingleGeneration(t *testing.T) {
	d, engine, _, textStream, _, _ := newDebounceTestDispatcher(t, 150*time.Millisecond)

	sendQuestion(t, textStream, "What is your experience with Go?")
	// Refined-версия через 30мс: Jaccard = 0.75 ≥ порога дедупа → раньше этот
	// путь ДРОПАЛ уточнение, теперь обновляет задание под дебаунсом.
	time.Sleep(30 * time.Millisecond)
	sendQuestion(t, textStream, "What is your experience with Go in production?")

	// Ждём истечения дебаунса и генерации.
	deadline := time.Now().Add(2 * time.Second)
	for len(engine.Calls()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	calls := engine.Calls()
	if len(calls) != 1 {
		t.Fatalf("ожидается 1 LLM-вызов (1 вопрос = 1 вызов), получено %d: %v", len(calls), calls)
	}
	if calls[0] != "What is your experience with Go in production?" {
		t.Errorf("генерация должна получить refined-текст, получено %q", calls[0])
	}
	if pendingCount(d) != 0 {
		t.Errorf("pending не пуст после постановки: %+v", d.pending)
	}
}

// TestDebounceFragmentNoSecondGeneration — фрагмент-продолжение (коалесинг)
// при задании под дебаунсом обновляет его текст и НЕ создаёт вторую
// генерацию (раньше: перегенерация вторым заданием — ×2 вызова).
func TestDebounceFragmentNoSecondGeneration(t *testing.T) {
	d, engine, _, textStream, _, _ := newDebounceTestDispatcher(t, 150*time.Millisecond)

	sendQuestion(t, textStream, "Do you have experience with Go?")
	time.Sleep(30 * time.Millisecond)
	sendQuestion(t, textStream, "and what about latency?")

	deadline := time.Now().Add(2 * time.Second)
	for len(engine.Calls()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	calls := engine.Calls()
	if len(calls) != 1 {
		t.Fatalf("ожидается 1 LLM-вызов, получено %d: %v", len(calls), calls)
	}
	want := "Do you have experience with Go? and what about latency?"
	if calls[0] != want {
		t.Errorf("генерация должна получить полный текст %q, получено %q", want, calls[0])
	}
	if pendingCount(d) != 0 {
		t.Errorf("pending не пуст после постановки: %+v", d.pending)
	}
}

// TestDebounceShutdownDropsPendingQuietly — shutdown с заданием под дебаунсом:
// генерация не запускается, метка тихо возвращается в QIdle.
func TestDebounceShutdownDropsPendingQuietly(t *testing.T) {
	d, engine, _, textStream, done, cancel := newDebounceTestDispatcher(t, 5*time.Second)

	sendQuestion(t, textStream, "What is Redis?")

	// Ждём, пока задание встанет под дебаунс.
	deadline := time.Now().Add(2 * time.Second)
	for pendingCount(d) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if pendingCount(d) != 1 {
		t.Fatalf("задание не встало под дебаунс: %+v", d.pending)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatcher не завершился с заданием под дебаунсом")
	}

	if calls := engine.Calls(); len(calls) != 0 {
		t.Errorf("shutdown не должен генерировать, вызовы: %v", calls)
	}
	states := map[int]QState{}
	for _, e := range d.timeline.Snapshot() {
		states[e.Idx] = e.State
	}
	if got := states[0]; got != QIdle {
		t.Errorf("метка 0: state = %v, want QIdle", got)
	}
}

// TestDebounceEscDropsPendingWithMissed — Esc с заданием под дебаунсом:
// задание дропается как очередь — метка QIdle, шапка «MISSED — press F1».
func TestDebounceEscDropsPendingWithMissed(t *testing.T) {
	d, engine, overlay, textStream, _, cancel := newDebounceTestDispatcher(t, 5*time.Second)
	defer cancel()

	sendQuestion(t, textStream, "What is Redis?")

	deadline := time.Now().Add(2 * time.Second)
	for pendingCount(d) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if pendingCount(d) != 1 {
		t.Fatalf("задание не встало под дебаунс: %+v", d.pending)
	}

	d.cancelCh <- struct{}{}

	deadline = time.Now().Add(2 * time.Second)
	for pendingCount(d) != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if pendingCount(d) != 0 {
		t.Fatalf("pending не очищен по Esc: %+v", d.pending)
	}
	if calls := engine.Calls(); len(calls) != 0 {
		t.Errorf("Esc не должен генерировать, вызовы: %v", calls)
	}
	missed := 0
	for _, m := range overlay.GetMessages() {
		if len(m.Answers) > 0 && m.Answers[0].Source == missedAnswerText {
			missed++
		}
	}
	if missed != 1 {
		t.Errorf("ожидалась 1 MISSED-шапка, получено %d", missed)
	}
}
