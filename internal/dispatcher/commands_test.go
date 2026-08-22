package dispatcher

import (
	"context"
	"testing"
	"time"

	"github.com/mastererik/translator/internal/common"
	"github.com/mastererik/translator/internal/translator"
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

func question(text string) common.STTEvent {
	return common.STTEvent{
		Event:     common.EventEndOfTurn,
		ChannelID: "speaker",
		Text:      text,
		Timestamp: time.Now(),
	}
}

// Test 7 — F1 запускает обычную генерацию ответа.
func TestCommandF1GiveAnswer(t *testing.T) {
	engine := &mockEngine{answers: []string{"EN: A | RU: A"}}
	d, textStream, cancel := startDispatcher(t, engine)
	defer cancel()

	textStream <- question("Tell me about Project X.")

	waitFor(t, func() bool { return len(engine.Calls()) == 1 }, 2*time.Second)

	reqs := engine.Reqs()
	if len(reqs) != 1 {
		t.Fatalf("ожидался 1 вызов, получено %d", len(reqs))
	}
	if reqs[0].Command != translator.CommandAnswer {
		t.Errorf("F1 должен быть CommandAnswer, получено %v", reqs[0].Command)
	}

	if recent := d.history.Recent(); len(recent) != 1 {
		t.Errorf("ожидался 1 turn в истории, получено %d", len(recent))
	}
}

// Test 8 — F2 повторная генерация с ThinkDeeper.
func TestCommandF2ThinkDeeper(t *testing.T) {
	engine := &mockEngine{answers: []string{"EN: A | RU: A"}}
	d, textStream, cancel := startDispatcher(t, engine)
	defer cancel()

	textStream <- question("Tell me about Project X.")
	waitFor(t, func() bool { return len(engine.Calls()) == 1 }, 2*time.Second)

	d.HandleCommand(translator.CommandThinkDeeper)
	waitFor(t, func() bool { return len(engine.Calls()) == 2 }, 2*time.Second)

	reqs := engine.Reqs()
	if reqs[1].Command != translator.CommandThinkDeeper {
		t.Errorf("F2 должен быть CommandThinkDeeper, получено %v", reqs[1].Command)
	}
}

// Test 9 — F3 повторная генерация с большим контекстом.
func TestCommandF3MoreContext(t *testing.T) {
	engine := &mockEngine{answers: []string{"EN: A | RU: A"}}
	d, textStream, cancel := startDispatcher(t, engine)
	defer cancel()

	textStream <- question("Tell me about Project X.")
	waitFor(t, func() bool { return len(engine.Calls()) == 1 }, 2*time.Second)

	d.HandleCommand(translator.CommandMoreContext)
	waitFor(t, func() bool { return len(engine.Calls()) == 2 }, 2*time.Second)

	reqs := engine.Reqs()
	if reqs[1].Command != translator.CommandMoreContext {
		t.Errorf("F3 должен быть CommandMoreContext, получено %v", reqs[1].Command)
	}
}

// Test 10 — F4 упрощает английский.
func TestCommandF4SimplerEnglish(t *testing.T) {
	engine := &mockEngine{answers: []string{"EN: A | RU: A"}}
	d, textStream, cancel := startDispatcher(t, engine)
	defer cancel()

	textStream <- question("Tell me about Project X.")
	waitFor(t, func() bool { return len(engine.Calls()) == 1 }, 2*time.Second)

	d.HandleCommand(translator.CommandSimplerEnglish)
	waitFor(t, func() bool { return len(engine.Calls()) == 2 }, 2*time.Second)

	reqs := engine.Reqs()
	if reqs[1].Command != translator.CommandSimplerEnglish {
		t.Errorf("F4 должен быть CommandSimplerEnglish, получено %v", reqs[1].Command)
	}
}

// Test 11 — Esc отменяет очередь: Q2/Q3/Q4 не уходят в LLM.
func TestCommandEscCancelsQueue(t *testing.T) {
	engine := &mockEngine{answers: []string{"EN: A | RU: A"}, delay: 200 * time.Millisecond}
	d, textStream, cancel := startDispatcher(t, engine)
	defer cancel()

	for _, q := range []string{"Q1", "Q2", "Q3", "Q4"} {
		textStream <- question(q)
	}

	// Даём worker'у взять Q1 (начать генерацию с задержкой).
	time.Sleep(50 * time.Millisecond)

	d.Cancel()

	// Ждём, пока отмена обработается и очередь будет дропнута.
	time.Sleep(500 * time.Millisecond)

	calls := engine.Calls()
	if len(calls) > 1 {
		t.Errorf("после Esc обработано больше одного вопроса: %v", calls)
	}
	for _, q := range []string{"Q2", "Q3", "Q4"} {
		for _, c := range calls {
			if c == q {
				t.Errorf("вопрос %q не должен обрабатываться после Esc", q)
			}
		}
	}
}

// Test 12 — regeneration не создаёт лишних conversation turns.
func TestCommandRegenerationDoesNotAddTurn(t *testing.T) {
	engine := &mockEngine{answers: []string{"EN: A | RU: A"}}
	d, textStream, cancel := startDispatcher(t, engine)
	defer cancel()

	textStream <- question("Tell me about Project X.")
	waitFor(t, func() bool { return len(engine.Calls()) == 1 }, 2*time.Second)

	// F4 — regeneration того же вопроса.
	d.HandleCommand(translator.CommandSimplerEnglish)
	waitFor(t, func() bool { return len(engine.Calls()) == 2 }, 2*time.Second)

	recent := d.history.Recent()
	if len(recent) != 1 {
		t.Fatalf("regeneration не должна создавать turn, получено %d", len(recent))
	}
	if recent[0].Question != "Tell me about Project X." {
		t.Errorf("вопрос в истории не совпадает: %q", recent[0].Question)
	}
}
