package translator

import (
	"context"
	"sync"
	"testing"
)

// mockLLMProvider is a test stub implementing LLMProvider.
type mockLLMProvider struct {
	mu            sync.Mutex
	generateCalls []AnswerRequest
	generateFn    func(ctx context.Context, req AnswerRequest) ([]string, error)
}

func (m *mockLLMProvider) GenerateAnswers(ctx context.Context, req AnswerRequest) ([]string, error) {
	m.mu.Lock()
	m.generateCalls = append(m.generateCalls, req)
	fn := m.generateFn
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, req)
	}
	return []string{"hint 1", "hint 2"}, nil
}

func TestNewEngine(t *testing.T) {
	mock := &mockLLMProvider{}
	engine := NewEngine(mock)

	if engine.llm == nil {
		t.Error("engine.llm should not be nil")
	}
}

func TestIsQuestion_EdgeCases(t *testing.T) {
	tests := []struct {
		text     string
		expected bool
	}{
		// Question marks anywhere.
		{"Is this a question?", true},
		{"Really? I didn't know that.", true},
		// Leading question words.
		{"what is docker", true},
		{"What about testing", true},
		{"how do you scale", true},
		{"why use go", true},
		{"when to use nosql", true},
		{"where to deploy", true},
		{"who is responsible", true},
		{"which framework", true},
		{"can you help", true},
		{"could you explain", true},
		{"would you agree", true},
		{"will you join", true},
		{"do you know", true},
		{"have you tried", true},
		{"did you see", true},
		{"are you sure", true},
		{"is it good", true},
		{"is there a way", true},
		{"explain the architecture", true},
		{"describe your setup", true},
		{"tell me more", true},
		{"elaborate on that", true},
		{"clarify the point", true},
		{"share your experience", true},
		{"walk me through", true},
		{"talk about the design", true},
		{"give me an example", true},
		// False positives prevention.
		{"Question: how to handle errors", false}, // doesn't start with question word
		{"I know what to do", false},
		{"how", true}, // single question word
		// Word boundary: prefix words that are not questions.
		{"however, I did it differently", false},
		{"whatever you say", false},
		{"whoever comes first", false},
		{"whatsapp is a messenger", false},
		// Regression: positive cases still detected.
		{"how do you scale", true},
		{"what's your experience", true},    // apostrophe boundary
		{"what, exactly, went wrong", true}, // comma boundary
	}

	for _, tt := range tests {
		got := IsQuestion(tt.text)
		if got != tt.expected {
			t.Errorf("isQuestion(%q) = %v, want %v", tt.text, got, tt.expected)
		}
	}
}

// TestIsQuestion_TableDriven полный набор табличных тестов.
func TestIsQuestion_TableDriven(t *testing.T) {
	tests := []struct {
		text     string
		expected bool
	}{
		// Empty / whitespace.
		{"", false},
		{"   ", false},
		{"	\n", false},

		// Questions with question mark.
		{"What is Go?", true},
		{"Really?", true},
		{"go?", true},

		// Question words at start.
		{"what is docker", true},
		{"What about testing", true},
		{"how do you scale", true},
		{"why use go", true},
		{"when to use nosql", true},
		{"where to deploy", true},
		{"who is responsible", true},
		{"which framework", true},
		{"can you help", true},
		{"could you explain", true},
		{"would you agree", true},
		{"will you join", true},
		{"do you know", true},
		{"have you tried", true},
		{"did you see", true},
		{"are you sure", true},
		{"is it good", true},
		{"is there a way", true},
		{"explain the architecture", true},
		{"describe your setup", true},
		{"tell me more", true},
		{"elaborate on that", true},
		{"clarify the point", true},
		{"share your experience", true},
		{"walk me through", true},
		{"talk about the design", true},
		{"give me an example", true},

		// Single question words.
		{"how", true},
		{"what", true},
		{"why", true},

		// Not questions (statements).
		{"I have five years of experience.", false},
		{"Hello, nice to meet you.", false},
		{"I think microservices are a good fit.", false},
		{"The weather is nice today.", false},
		{"Question: how to handle errors", false},
	}

	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			got := IsQuestion(tt.text)
			if got != tt.expected {
				t.Errorf("IsQuestion(%q) = %v, want %v", tt.text, got, tt.expected)
			}
		})
	}
}

// TestEngine_GenerateAnswers проверяет делегирование GenerateAnswers.
func TestEngine_GenerateAnswers(t *testing.T) {
	mock := &mockLLMProvider{
		generateFn: func(ctx context.Context, req AnswerRequest) ([]string, error) {
			return []string{"answer1", "answer2"}, nil
		},
	}
	engine := NewEngine(mock)

	answers, err := engine.GenerateAnswers(context.Background(), AnswerRequest{Question: "question"})
	if err != nil {
		t.Fatalf("GenerateAnswers error: %v", err)
	}
	if len(answers) != 2 {
		t.Errorf("len = %d, want 2", len(answers))
	}
}

// TestEngine_GenerateAnswersStream проверяет делегирование стриминга (с fallback).
func TestEngine_GenerateAnswersStream(t *testing.T) {
	// mockLLMProvider НЕ реализует StreamingAnswersProvider — должен быть fallback.
	mock := &mockLLMProvider{
		generateFn: func(ctx context.Context, req AnswerRequest) ([]string, error) {
			return []string{"a", "b", "c"}, nil
		},
	}
	engine := NewEngine(mock)

	ch, err := engine.GenerateAnswersStream(context.Background(), AnswerRequest{Question: "q"})
	if err != nil {
		t.Fatalf("GenerateAnswersStream error: %v", err)
	}

	var tokens []string
	for t := range ch {
		tokens = append(tokens, t)
	}
	if len(tokens) != 3 {
		t.Errorf("len = %d, want 3", len(tokens))
	}
}

// Test 4 — неявные запросы на генерацию ответа распознаются как вопросы.
func TestIsQuestion_ImplicitRequests(t *testing.T) {
	for _, phrase := range []string{
		"Tell me about your latest project.",
		"Describe your latest project.",
		"Your latest project?",
		"Let's talk about your latest project.",
	} {
		if !IsQuestion(phrase) {
			t.Errorf("IsQuestion(%q) = false, want true", phrase)
		}
	}
}

// mockStreamingProvider — мок, реализующий StreamingAnswersProvider.
type mockStreamingProvider struct {
	mockLLMProvider
	tokens []string
}

func (m *mockStreamingProvider) GenerateAnswersStream(ctx context.Context, req AnswerRequest) (<-chan string, error) {
	ch := make(chan string, len(m.tokens))
	for _, t := range m.tokens {
		ch <- t
	}
	close(ch)
	return ch, nil
}

var _ StreamingAnswersProvider = (*mockStreamingProvider)(nil)

// TestEngine_GenerateAnswersStream_DirectStreaming проверяет прямой стриминг (без fallback).
func TestEngine_GenerateAnswersStream_DirectStreaming(t *testing.T) {
	mock := &mockStreamingProvider{
		tokens: []string{"tok1", "tok2"},
	}
	engine := NewEngine(mock)

	ch, err := engine.GenerateAnswersStream(context.Background(), AnswerRequest{Question: "q"})
	if err != nil {
		t.Fatalf("GenerateAnswersStream error: %v", err)
	}

	var tokens []string
	for t := range ch {
		tokens = append(tokens, t)
	}
	if len(tokens) != 2 {
		t.Errorf("len = %d, want 2", len(tokens))
	}
}
