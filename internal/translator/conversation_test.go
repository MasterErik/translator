package translator

import (
	"strings"
	"testing"
)

// Test 1 — независимые вопросы: история сохраняется, BuildContext содержит оба.
func TestConversationHistoryRecordsTurns(t *testing.T) {
	h := NewConversationHistory(6, 4000)
	h.RecordAnswer("Tell me about your experience with Go?", "I used Go for microservices.")
	h.RecordAnswer("Why did you choose PostgreSQL?", "For transactional integrity.")

	recent := h.Recent()
	if len(recent) != 2 {
		t.Fatalf("expected 2 turns, got %d", len(recent))
	}
	if recent[0].Question != "Tell me about your experience with Go?" {
		t.Errorf("unexpected first question: %q", recent[0].Question)
	}
	if recent[1].Answer != "For transactional integrity." {
		t.Errorf("unexpected second answer: %q", recent[1].Answer)
	}

	ctx := h.BuildContext()
	if !strings.Contains(ctx, "Tell me about your experience with Go?") {
		t.Errorf("context missing Q1: %q", ctx)
	}
	if !strings.Contains(ctx, "Why did you choose PostgreSQL?") {
		t.Errorf("context missing Q2: %q", ctx)
	}
}

// Test 2 — follow-up: контекст сохраняет Project X из Q1.
func TestConversationHistoryFollowUpKeepsProjectX(t *testing.T) {
	h := NewConversationHistory(6, 4000)
	h.RecordAnswer("Tell me about Project X.", "I built Project X as a pricing engine.")

	ctx := h.BuildContext()
	if !strings.Contains(ctx, "Project X") {
		t.Errorf("context must keep Project X for follow-up question: %q", ctx)
	}
}

// Test 3 — несколько follow-up: контекст сохраняется через несколько turns.
func TestConversationHistoryMultipleFollowUps(t *testing.T) {
	h := NewConversationHistory(6, 4000)
	questions := []string{
		"Tell me about Project X.",
		"What was the main challenge?",
		"How did you solve it?",
		"What was the result?",
	}
	for i, q := range questions {
		h.RecordAnswer(q, "answer "+string(rune('A'+i)))
	}

	recent := h.Recent()
	if len(recent) != len(questions) {
		t.Fatalf("expected %d turns, got %d", len(questions), len(recent))
	}

	ctx := h.BuildContext()
	for _, q := range questions {
		if !strings.Contains(ctx, q) {
			t.Errorf("context missing %q", q)
		}
	}
}

// Test 5 — ограничение по количеству turns: старые turns удаляются.
func TestConversationHistoryTrimsToRecentTurns(t *testing.T) {
	h := NewConversationHistory(2, 4000)
	h.RecordAnswer("Q1", "A1")
	h.RecordAnswer("Q2", "A2")
	h.RecordAnswer("Q3", "A3")
	h.RecordAnswer("Q4", "A4")

	recent := h.Recent()
	if len(recent) != 2 {
		t.Fatalf("expected 2 turns, got %d", len(recent))
	}
	if recent[0].Question != "Q3" || recent[1].Question != "Q4" {
		t.Errorf("expected Q3,Q4 got %q,%q", recent[0].Question, recent[1].Question)
	}

	ctx := h.BuildContext()
	if strings.Contains(ctx, "Q1") || strings.Contains(ctx, "Q2") {
		t.Errorf("old turns should be trimmed: %q", ctx)
	}
	if !strings.Contains(ctx, "Q3") || !strings.Contains(ctx, "Q4") {
		t.Errorf("recent turns should be kept: %q", ctx)
	}
}

// Test 5 — ограничение по размеру контекста: при превышении лимита
// самые свежие turns сохраняются, старые удаляются.
func TestConversationHistoryTrimsByMaxTokens(t *testing.T) {
	h := NewConversationHistory(100, 12) // маленький лимит размера
	h.RecordAnswer("First question with a fairly long wording to consume tokens", "First answer also fairly long wording here")
	h.RecordAnswer("Second question with a fairly long wording to consume tokens", "Second answer also fairly long wording here")
	h.RecordAnswer("Third question here", "Third answer here")

	ctx := h.BuildContext()
	if !strings.Contains(ctx, "Third question here") {
		t.Errorf("latest turn must always be kept: %q", ctx)
	}
	if strings.Contains(ctx, "First question") {
		t.Errorf("oldest turn should be trimmed: %q", ctx)
	}
}

// Test 12 — regeneration: повторная генерация не создаёт новый turn,
// последняя успешная версия становится текущим ответом.
func TestConversationHistoryRegenerationDoesNotAddTurn(t *testing.T) {
	h := NewConversationHistory(6, 4000)
	h.RecordAnswer("Tell me about Project X.", "first version")
	h.RecordAnswer("Tell me about Project X.", "simpler version") // F4 regeneration

	recent := h.Recent()
	if len(recent) != 1 {
		t.Fatalf("regeneration must not add a turn, got %d", len(recent))
	}
	if recent[0].Answer != "simpler version" {
		t.Errorf("last successful version should win, got %q", recent[0].Answer)
	}
}

// GenerationCommand.String покрывает все команды F1–F4.
func TestGenerationCommandString(t *testing.T) {
	cases := map[GenerationCommand]string{
		CommandAnswer:         "Answer",
		CommandThinkDeeper:    "ThinkDeeper",
		CommandMoreContext:    "MoreContext",
		CommandSimplerEnglish: "SimplerEnglish",
	}
	for cmd, want := range cases {
		if got := cmd.String(); got != want {
			t.Errorf("Command %d: got %q, want %q", cmd, got, want)
		}
	}
}
