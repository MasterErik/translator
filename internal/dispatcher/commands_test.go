package dispatcher

import (
	"context"
	"testing"
	"time"

	"github.com/mastererik/translator/internal/common"
	"github.com/mastererik/translator/internal/translator"
	"github.com/mastererik/translator/internal/ui"
)

// waitFor ожидает, пока условие станет истинным, до таймаута.
func waitFor(t *testing.T, cond func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("условие не выполнено до таймаута")
}

// startDispatcher запускает Dispatcher.Run в фоне и возвращает его,
// textStream и cancel для завершения.
func startDispatcher(t *testing.T, engine *mockEngine) (*Dispatcher, chan common.STTEvent, context.CancelFunc) {
	t.Helper()
	d := New(&mockOverlay{}, engine, &mockLogger{}, DefaultConfig())
	ctx, cancel := context.WithCancel(context.Background())
	textStream := make(chan common.STTEvent, 8)
	done := make(chan struct{}, 1)
	go d.Run(ctx, textStream, done)
	return d, textStream, cancel
}

// startDispatcherWithOverlay — как startDispatcher, но возвращает overlay для
// проверки UI-сообщений (шапок AnswerCandidates).
func startDispatcherWithOverlay(t *testing.T, engine *mockEngine, cfg Config) (*Dispatcher, *mockOverlay, chan common.STTEvent, context.CancelFunc) {
	t.Helper()
	overlay := &mockOverlay{}
	d := New(overlay, engine, &mockLogger{}, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	textStream := make(chan common.STTEvent, 8)
	done := make(chan struct{}, 1)
	go d.Run(ctx, textStream, done)
	return d, overlay, textStream, cancel
}

func question(text string) common.STTEvent {
	return common.STTEvent{
		Event:     common.EventEndOfTurn,
		ChannelID: "speaker",
		Text:      text,
		Timestamp: time.Now(),
	}
}

// lastCandidates возвращает последнее AnswerCandidates-сообщение overlay и ok.
func lastCandidates(overlay *mockOverlay) (ui.UIMessage, bool) {
	var last ui.UIMessage
	found := false
	for _, m := range overlay.GetMessages() {
		if m.Type == ui.AnswerCandidates {
			last = m
			found = true
		}
	}
	return last, found
}

// snapshotOf возвращает снимок таймлайна под тестовым доступом.
func snapshotOf(d *Dispatcher) []QEntry {
	return d.timeline.Snapshot()
}

// ── Авто-ответ (AutoAnswer=true, default) ────────────────────────────────

// TestAutoAnswerF1AfterReadyGoesMoreContext — при включённом авто-ответе
// первый F1 после готового авто-ответа сразу даёт CommandMoreContext (Step уже 1).
func TestAutoAnswerF1AfterReadyGoesMoreContext(t *testing.T) {
	engine := &mockEngine{answers: []string{"EN: A | RU: A"}}
	d, overlay, textStream, cancel := startDispatcherWithOverlay(t, engine, DefaultConfig())
	defer cancel()

	textStream <- question("Tell me about Project X.")
	waitFor(t, func() bool { return len(engine.Calls()) == 1 }, 2*time.Second)
	// Авто-ответ = CommandAnswer (Step метки стал 1).
	if reqs := engine.Reqs(); reqs[0].Command != translator.CommandAnswer {
		t.Fatalf("авто-ответ: Command = %v, ожидался CommandAnswer", reqs[0].Command)
	}

	// F1 после Ready → следующий шаг цикла = CommandMoreContext.
	d.HandleDemand()
	waitFor(t, func() bool { return len(engine.Calls()) == 2 }, 2*time.Second)

	reqs := engine.Reqs()
	if reqs[1].Command != translator.CommandMoreContext {
		t.Errorf("F1 после авто-ответа: Command = %v, ожидался CommandMoreContext", reqs[1].Command)
	}

	// Шапка показывает режим "more context".
	if msg, ok := lastCandidates(overlay); ok {
		if len(msg.Answers) == 0 || msg.Answers[0].Mode != "more context" {
			t.Errorf("Mode шапки = %v, ожидался 'more context'", msg.Answers)
		}
	} else {
		t.Error("AnswerCandidates не найдены в UI")
	}
}

// TestAutoAnswerFalseF1ThenMoreContext — AutoAnswer=false: вопрос создаёт метку
// Idle без задания; F1 даёт CommandAnswer; второй F1 (после Ready) — MoreContext.
func TestAutoAnswerFalseF1ThenMoreContext(t *testing.T) {
	auto := false
	cfg := DefaultConfig()
	cfg.AutoAnswer = &auto

	engine := &mockEngine{answers: []string{"EN: A | RU: A"}}
	d, overlay, textStream, cancel := startDispatcherWithOverlay(t, engine, cfg)
	defer cancel()

	textStream <- question("Tell me about Project X.")
	// Вопрос не должен запускать генерацию.
	time.Sleep(100 * time.Millisecond)
	if calls := engine.Calls(); len(calls) != 0 {
		t.Fatalf("AutoAnswer=false: генерация запущена без F1, вызовов %d", len(calls))
	}

	// Метка существует в состоянии Idle.
	snap := snapshotOf(d)
	if len(snap) != 1 {
		t.Fatalf("метка не создана: %d", len(snap))
	}
	if snap[0].State != QIdle {
		t.Fatalf("состояние метки = %v, ожидалось QIdle", snap[0].State)
	}

	// Первый F1 → CommandAnswer.
	d.HandleDemand()
	waitFor(t, func() bool { return len(engine.Calls()) == 1 }, 2*time.Second)
	if reqs := engine.Reqs(); reqs[0].Command != translator.CommandAnswer {
		t.Errorf("первый F1: Command = %v, ожидался CommandAnswer", reqs[0].Command)
	}
	waitFor(t, func() bool { return snapshotOf(d)[0].State == QReady }, 2*time.Second)

	// Второй F1 (после Ready) → CommandMoreContext.
	d.HandleDemand()
	waitFor(t, func() bool { return len(engine.Calls()) == 2 }, 2*time.Second)
	if reqs := engine.Reqs(); reqs[1].Command != translator.CommandMoreContext {
		t.Errorf("второй F1: Command = %v, ожидался CommandMoreContext", reqs[1].Command)
	}

	// В overlay была хотя бы одна шапка AnswerCandidates.
	if _, ok := lastCandidates(overlay); !ok {
		t.Error("AnswerCandidates не найдены в UI")
	}
}

// ── Навигация Prev/Next и F1 на неактивной метке ─────────────────────────

// TestF1AfterNavigationAnswersQ1 — Q1,Q2,Q3 → Prev,Prev → F1: ответ ложится в
// Q1 (проверяем через Snapshot), и шапка в UI указывает на QuestionIdx=0.
func TestF1AfterNavigationAnswersQ1(t *testing.T) {
	auto := false
	cfg := DefaultConfig()
	cfg.AutoAnswer = &auto

	engine := &mockEngine{answers: []string{"EN: answer-Q1 | RU: ответ"}}
	d, overlay, textStream, cancel := startDispatcherWithOverlay(t, engine, cfg)
	defer cancel()

	for _, q := range []string{"Q1?", "Q2?", "Q3?"} {
		textStream <- question(q)
	}
	waitFor(t, func() bool { return len(snapshotOf(d)) == 3 }, 2*time.Second)

	// Уходим на Q1.
	d.HandlePrev()
	d.HandlePrev()
	waitFor(t, func() bool {
		idx, _, _ := d.timeline.Active()
		return idx == 0
	}, 2*time.Second)

	// F1 отвечает на активную метку Q1.
	d.HandleDemand()

	waitFor(t, func() bool {
		snap := snapshotOf(d)
		return len(snap) == 3 && snap[0].State == QReady
	}, 2*time.Second)

	snap := snapshotOf(d)
	if len(snap[0].Answer) != 1 || snap[0].Answer[0].Source != "EN: answer-Q1" {
		t.Errorf("ответ Q1: %+v, ожидался answer-Q1", snap[0].Answer)
	}
	// Q2/Q3 не должны иметь ответов.
	if len(snap[1].Answer) != 0 || len(snap[2].Answer) != 0 {
		t.Errorf("Q2/Q3 получили ответы: %+v / %+v", snap[1].Answer, snap[2].Answer)
	}

	// Шапка в UI указывает на Q1 (QuestionIdx=0).
	if msg, ok := lastCandidates(overlay); ok {
		if len(msg.Answers) == 0 {
			t.Fatalf("шапка без Answers: %+v", msg)
		}
		if msg.Answers[0].QuestionIdx != 0 {
			t.Errorf("QuestionIdx шапки = %d, ожидался 0", msg.Answers[0].QuestionIdx)
		}
		if msg.Answers[0].Total != 3 {
			t.Errorf("Total шапки = %d, ожидался 3", msg.Answers[0].Total)
		}
	} else {
		t.Error("AnswerCandidates не найдены в UI")
	}
}

// TestStaleAnswerDoesNotOverwriteHeader — ответ Q1 приходит, когда пользователь
// уже на Q3: шапка Q3 НЕ перезаписана (последнее AnswerCandidates — про Q3).
func TestStaleAnswerDoesNotOverwriteHeader(t *testing.T) {
	auto := false
	cfg := DefaultConfig()
	cfg.AutoAnswer = &auto

	engine := &mockEngine{answers: []string{"EN: answer | RU: ответ"}, delay: 150 * time.Millisecond}
	d, overlay, textStream, cancel := startDispatcherWithOverlay(t, engine, cfg)
	defer cancel()

	// Создаём Q1, Q2, Q3.
	for _, q := range []string{"Q1?", "Q2?", "Q3?"} {
		textStream <- question(q)
	}
	waitFor(t, func() bool { return len(snapshotOf(d)) == 3 }, 2*time.Second)

	// Уходим на Q1 и запускаем долгую генерацию.
	d.HandlePrev()
	d.HandlePrev()
	waitFor(t, func() bool { idx, _, _ := d.timeline.Active(); return idx == 0 }, 2*time.Second)
	d.HandleDemand()
	// Даём worker'у взять задание Q1 (начинается длинная генерация).
	waitFor(t, func() bool { return snapshotOf(d)[0].State == QGenerating }, 2*time.Second)

	// Пока Q1 генерируется, пользователь уходит вперёд на Q3.
	d.HandleNext()
	d.HandleNext()
	waitFor(t, func() bool { idx, _, _ := d.timeline.Active(); return idx == 2 }, 2*time.Second)

	// Prev/Next успели отправить шапку Q3 — запомним её (последнее сообщение).
	waitFor(t, func() bool {
		msg, ok := lastCandidates(overlay)
		return ok && len(msg.Answers) > 0 && msg.Answers[0].QuestionIdx == 2
	}, 2*time.Second)

	// Ждём завершения генерации Q1.
	waitFor(t, func() bool { return snapshotOf(d)[0].State == QReady }, 2*time.Second)

	// Последнее AnswerCandidates должно по-прежнему указывать на Q3 (idx 2),
	// а не быть перезаписанным устаревшим ответом Q1.
	msg, ok := lastCandidates(overlay)
	if !ok || len(msg.Answers) == 0 {
		t.Fatalf("AnswerCandidates не найдены: %+v", msg)
	}
	if msg.Answers[0].QuestionIdx != 2 {
		t.Errorf("шапку перезаписал устаревший ответ: QuestionIdx = %d, ожидался 2 (Q3)", msg.Answers[0].QuestionIdx)
	}
}

// ── Коалесинг F1 во время генерации ─────────────────────────────────────

// TestF1DuringGenerationIgnored — F1 в состоянии QGenerating игнорируется,
// новых заданий в LLM не появляется.
func TestF1DuringGenerationIgnored(t *testing.T) {
	auto := false
	cfg := DefaultConfig()
	cfg.AutoAnswer = &auto

	engine := &mockEngine{answers: []string{"EN: answer | RU: ответ"}, delay: 300 * time.Millisecond}
	d, _, textStream, cancel := startDispatcherWithOverlay(t, engine, cfg)
	defer cancel()

	textStream <- question("Q1?")
	waitFor(t, func() bool { return len(snapshotOf(d)) == 1 }, 2*time.Second)

	d.HandleDemand()
	// Ждём начала генерации.
	waitFor(t, func() bool { return snapshotOf(d)[0].State == QGenerating }, 2*time.Second)

	// Несколько F1 во время генерации — игнор.
	for i := 0; i < 5; i++ {
		d.HandleDemand()
		time.Sleep(10 * time.Millisecond)
	}

	// Даём завершиться единственной генерации.
	waitFor(t, func() bool { return snapshotOf(d)[0].State == QReady }, 2*time.Second)
	time.Sleep(100 * time.Millisecond)

	if calls := engine.Calls(); len(calls) != 1 {
		t.Errorf("F1 во время генерации не должен создавать заданий: вызовов %d, ожидалось 1", len(calls))
	}
}

// ── Esc ─────────────────────────────────────────────────────────────────

// TestEscKeepsLabelsResetsPending — Esc: метки целы, Queued/Generating → Idle,
// очередь пуста, новый вопрос снова генерит.
func TestEscKeepsLabelsResetsPending(t *testing.T) {
	engine := &mockEngine{answers: []string{"EN: A | RU: A"}, delay: 200 * time.Millisecond}
	d, _, textStream, cancel := startDispatcherWithOverlay(t, engine, DefaultConfig())
	defer cancel()

	// Q1 берётся в работу (генерируется), Q2..Q4 стоят в очереди.
	for _, q := range []string{"Q1?", "Q2?", "Q3?", "Q4?"} {
		textStream <- question(q)
	}
	// Ждём, пока Q1 начнёт генерироваться (очередь заполнится).
	waitFor(t, func() bool {
		snap := snapshotOf(d)
		return len(snap) > 0 && snap[0].State == QGenerating
	}, 2*time.Second)

	d.Cancel()

	// Ждём обработки отмены.
	waitFor(t, func() bool {
		for _, e := range snapshotOf(d) {
			if e.State == QQueued || e.State == QGenerating {
				return false
			}
		}
		return true
	}, 2*time.Second)

	// Метки целы (4 штуки), очередь пуста.
	snap := snapshotOf(d)
	if len(snap) != 4 {
		t.Fatalf("после Esc меток %d, ожидалось 4 (метки должны сохраняться)", len(snap))
	}
	if n := len(d.answerCh); n != 0 {
		t.Errorf("очередь после Esc не пуста: %d заданий", n)
	}

	callsAfterEsc := len(engine.Calls())

	// Новый вопрос снова запускает генерацию.
	textStream <- question("Q5?")
	waitFor(t, func() bool { return len(engine.Calls()) > callsAfterEsc }, 2*time.Second)
}

// ── Пустой таймлайн ─────────────────────────────────────────────────────

// TestHotkeysEmptyTimelineNoPanic — F1/F2/F3 на пустом таймлайне — no-op без паники.
func TestHotkeysEmptyTimelineNoPanic(t *testing.T) {
	engine := &mockEngine{answers: []string{"EN: A | RU: A"}}
	d, _, _, cancel := startDispatcherWithOverlay(t, engine, DefaultConfig())
	defer cancel()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("паника на пустом таймлайне: %v", r)
		}
	}()

	d.HandleDemand()
	d.HandlePrev()
	d.HandleNext()
	time.Sleep(100 * time.Millisecond)

	if calls := engine.Calls(); len(calls) != 0 {
		t.Errorf("на пустом таймлайне вызовов %d, ожидалось 0", len(calls))
	}
}

// ── RecordAnswer только для хвоста ──────────────────────────────────────

// TestRecordAnswerOnlyOnTail — RecordAnswer пишется только для хвостовой метки.
func TestRecordAnswerOnlyOnTail(t *testing.T) {
	auto := false
	cfg := DefaultConfig()
	cfg.AutoAnswer = &auto

	engine := &mockEngine{answers: []string{"EN: tail-answer | RU: ответ"}}
	d, _, textStream, cancel := startDispatcherWithOverlay(t, engine, cfg)
	defer cancel()

	// Q1, Q2, Q3.
	for _, q := range []string{"Q1?", "Q2?", "Q3?"} {
		textStream <- question(q)
	}
	waitFor(t, func() bool { return len(snapshotOf(d)) == 3 }, 2*time.Second)

	// Уходим на Q2 (не хвост: хвост — Q3) и отвечаем.
	d.HandlePrev()
	waitFor(t, func() bool { idx, _, _ := d.timeline.Active(); return idx == 1 }, 2*time.Second)
	d.HandleDemand()

	waitFor(t, func() bool { return snapshotOf(d)[1].State == QReady }, 2*time.Second)

	// Ответ на не-хвостовую метку НЕ должен попасть в историю.
	if recent := d.history.Recent(); len(recent) != 0 {
		t.Errorf("ответ на не-хвостовую метку записан в историю: %+v", recent)
	}

	// Возвращаемся на хвост (Q3) и отвечаем — вот теперь история пишется.
	d.HandleNext()
	waitFor(t, func() bool { idx, _, _ := d.timeline.Active(); return idx == 2 }, 2*time.Second)
	callsBefore := len(engine.Calls())
	d.HandleDemand()
	waitFor(t, func() bool { return len(engine.Calls()) > callsBefore }, 2*time.Second)
	waitFor(t, func() bool { return len(d.history.Recent()) == 1 }, 2*time.Second)

	recent := d.history.Recent()
	if recent[0].Question != "Q3?" {
		t.Errorf("в историю записан неверный вопрос: %q, ожидался Q3?", recent[0].Question)
	}
}

// TestAutoAnswerFalseRegenerationDoesNotAddTurn — повторный F1 (regeneration) на
// хвостовую метку не создаёт новый turn в истории.
func TestAutoAnswerFalseRegenerationDoesNotAddTurn(t *testing.T) {
	auto := false
	cfg := DefaultConfig()
	cfg.AutoAnswer = &auto

	engine := &mockEngine{answers: []string{"EN: A | RU: A"}}
	d, _, textStream, cancel := startDispatcherWithOverlay(t, engine, cfg)
	defer cancel()

	textStream <- question("Tell me about Project X.")
	waitFor(t, func() bool { return len(snapshotOf(d)) == 1 }, 2*time.Second)

	d.HandleDemand()
	waitFor(t, func() bool { return len(engine.Calls()) == 1 }, 2*time.Second)
	waitFor(t, func() bool { return len(d.history.Recent()) == 1 }, 2*time.Second)

	// Второй F1 — regeneration (MoreContext) на хвостовую метку.
	d.HandleDemand()
	waitFor(t, func() bool { return len(engine.Calls()) == 2 }, 2*time.Second)
	time.Sleep(50 * time.Millisecond)

	recent := d.history.Recent()
	if len(recent) != 1 {
		t.Fatalf("regeneration создала turn: %d, ожидалось 1", len(recent))
	}
	if recent[0].Question != "Tell me about Project X." {
		t.Errorf("вопрос в истории: %q", recent[0].Question)
	}
}

// ── Совместимость: GenerateAnswers (shutdown/тесты) ──────────────────────

func TestGenerateAnswersAddsLabelAndAnswers(t *testing.T) {
	engine := &mockEngine{answers: []string{"EN: A | RU: A"}}
	overlay := &mockOverlay{}
	cfg := DefaultConfig()
	d := New(overlay, engine, &mockLogger{}, cfg)

	d.GenerateAnswers("What is Redis?")

	snap := snapshotOf(d)
	if len(snap) != 1 {
		t.Fatalf("GenerateAnswers не создал метку: %d", len(snap))
	}
	if snap[0].State != QReady {
		t.Errorf("состояние метки = %v, ожидалось QReady", snap[0].State)
	}
	if _, ok := lastCandidates(overlay); !ok {
		t.Error("GenerateAnswers не отправил AnswerCandidates в UI")
	}
}
