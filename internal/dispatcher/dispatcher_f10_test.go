package dispatcher

import (
	"context"
	"testing"
	"time"

	"github.com/mastererik/translator/internal/common"
	"github.com/mastererik/translator/internal/translator"
	"github.com/mastererik/translator/internal/ui"
)

// ── F10: надёжность (C1 — тихие пропуски, C2 — shutdown отменяет генерацию) ─

// TestMissTaskQueueOverflow — переполнение answerCh: задание дропается, метка
// возвращается в QIdle, в overlay уходит шапка «MISSED — press F1», дроп
// попадает в sessLog (ReportDrop-агрегат) (C1).
func TestMissTaskQueueOverflow(t *testing.T) {
	engine := &mockEngine{answers: []string{"EN: a | RU: а"}}
	overlay := &mockOverlay{}
	logger := &mockLogger{}
	d := New(overlay, engine, logger, DefaultConfig())

	// Реальные метки в таймлайне (pushHeader ищет метку в снапшоте).
	idx := d.timeline.Add("Q?")

	// Забиваем очередь под завязку (AnswerQueueSize=16).
	for i := 0; i < DefaultConfig().AnswerQueueSize; i++ {
		d.answerCh <- answerTask{idx: 100 + i, cmd: translator.CommandAnswer, question: "queued"}
	}

	d.enqueueAnswer(answerTask{idx: idx, cmd: translator.CommandAnswer, question: "Q?"})

	// Метка — QIdle в таймлайне.
	for _, e := range d.timeline.Snapshot() {
		if e.Idx == idx && e.State != QIdle {
			t.Errorf("метка %d: state = %v, want QIdle", idx, e.State)
		}
	}

	// Шапка с MISSED ушла в overlay.
	msgs := overlay.GetMessages()
	found := false
	for _, m := range msgs {
		if m.Type != ui.AnswerCandidates || len(m.Answers) == 0 {
			continue
		}
		if m.Answers[0].Source == missedAnswerText {
			found = true
		}
	}
	if !found {
		t.Errorf("ожидалась шапка с текстом %q, msgs=%+v", missedAnswerText, msgs)
	}

	// Дроп залогирован: причина + агрегат ReportDrop.
	if !logger.HasDebug("вопрос пропущен") {
		t.Errorf("ожидался debug-лог о пропуске, logs=%+v", logger.DebugLogs())
	}
	if !logger.HasDebug("переполнение буфера") {
		t.Errorf("ожидался агрегатный лог ReportDrop, logs=%+v", logger.DebugLogs())
	}
}

// TestMissTaskOnEscDrop — Esc: задания из очереди дропаются с шапкой MISSED,
// метки возвращаются в QIdle.
func TestMissTaskOnEscDrop(t *testing.T) {
	engine := &mockEngine{answers: []string{"EN: a | RU: а"}}
	overlay := &mockOverlay{}
	logger := &mockLogger{}
	d := New(overlay, engine, logger, DefaultConfig())

	idx0 := d.timeline.Add("Q1?")
	idx1 := d.timeline.Add("Q2?")

	d.answerCh <- answerTask{idx: idx0, cmd: translator.CommandAnswer, question: "Q1?"}
	d.answerCh <- answerTask{idx: idx1, cmd: translator.CommandAnswer, question: "Q2?"}

	d.cancelled.Store(true)
	d.dropQueue()

	states := map[int]QState{}
	for _, e := range d.timeline.Snapshot() {
		states[e.Idx] = e.State
	}
	for _, idx := range []int{idx0, idx1} {
		if got := states[idx]; got != QIdle {
			t.Errorf("метка %d: state = %v, want QIdle", idx, got)
		}
	}
	msgs := overlay.GetMessages()
	missed := 0
	for _, m := range msgs {
		if m.Type == ui.AnswerCandidates && len(m.Answers) > 0 && m.Answers[0].Source == missedAnswerText {
			missed++
		}
	}
	if missed != 2 {
		t.Errorf("ожидалось 2 MISSED-шапки, получено %d (msgs=%+v)", missed, msgs)
	}
	if !logger.HasDebug("очередь сброшена (Esc)") {
		t.Errorf("ожидался debug-лог о сбросе очереди, logs=%+v", logger.DebugLogs())
	}
}

// TestShutdownCancelsInFlightGeneration — F10/C2: cancel workerCtx обрывает
// активную генерацию (не дожидаясь её завершения) — Run завершается быстро.
func TestShutdownCancelsInFlightGeneration(t *testing.T) {
	engine := &blockingEngine{
		answers: []string{"EN: a | RU: а"},
		release: make(chan struct{}), // никогда не освобождаем
	}
	overlay := &mockOverlay{}
	d := New(overlay, engine, &mockLogger{}, DefaultConfig())

	ctx, cancel := context.WithCancel(context.Background())
	textStream := make(chan common.STTEvent, 8)
	done := make(chan struct{}, 1)
	go d.Run(ctx, textStream, done)

	textStream <- common.STTEvent{
		Event:     common.EventEndOfTurn,
		ChannelID: "speaker",
		Text:      "Blocking question?",
		Timestamp: time.Now(),
	}
	// Ждём, пока генерация начнётся (движок заблокирован внутри GenerateAnswers).
	deadline := time.Now().Add(2 * time.Second)
	for len(engine.Calls()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(engine.Calls()) == 0 {
		t.Fatal("генерация не началась за 2s")
	}

	start := time.Now()
	cancel()
	select {
	case <-done:
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Errorf("shutdown должен обрывать активную генерацию мгновенно, заняло %v", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("dispatcher не завершился при активной (заблокированной) генерации")
	}
}
