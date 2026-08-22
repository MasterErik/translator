package translator

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
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

func TestProcessQuestion_ClassificationPositive(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{"question_mark", "What is Kubernetes?"},
		{"what", "What is your experience with Docker"},
		{"how", "How do you handle race conditions"},
		{"why", "Why did you choose microservices"},
		{"can_you", "Can you explain CQRS"},
		{"could_you", "Could you describe your CI/CD pipeline"},
		{"tell_me", "Tell me about your last project"},
		{"explain", "Explain how mutex works in Go"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockLLMProvider{}
			engine := NewEngine(mock)

			result, err := engine.ProcessQuestion(tt.text)
			if err != nil {
				t.Fatalf("ProcessQuestion() error = %v", err)
			}

			if !result.IsQuestion {
				t.Errorf("Should classify %q as a question", tt.text)
			}
		})
	}
}

func TestProcessQuestion_ClassificationNegative(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{"statement", "I have five years of experience with Kubernetes."},
		{"greeting", "Hello, nice to meet you."},
		{"opinion", "I think microservices are a good fit for this project."},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockLLMProvider{}
			engine := NewEngine(mock)

			result, err := engine.ProcessQuestion(tt.text)
			if err != nil {
				t.Fatalf("ProcessQuestion() error = %v", err)
			}

			if result.IsQuestion {
				t.Errorf("Should NOT classify %q as a question", tt.text)
			}
		})
	}
}

func TestProcessQuestion_AnswerGenerationTriggered(t *testing.T) {
	mock := &mockLLMProvider{
		generateFn: func(ctx context.Context, req AnswerRequest) ([]string, error) {
			return []string{"hint about " + req.Question}, nil
		},
	}
	engine := NewEngine(mock)

	result, err := engine.ProcessQuestion("What is Docker?")
	if err != nil {
		t.Fatalf("ProcessQuestion() error = %v", err)
	}

	if !result.IsQuestion {
		t.Error("Should classify as question")
	}

	// Ждём асинхронной генерации подсказок с таймаутом.
	for i := 0; i < 50; i++ {
		if len(result.GetAnswers()) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if len(result.GetAnswers()) != 1 {
		t.Errorf("Answers length = %d, want 1", len(result.GetAnswers()))
	}
}

func TestProcessQuestion_NoAnswerForNonQuestion(t *testing.T) {
	mock := &mockLLMProvider{
		generateFn: func(ctx context.Context, req AnswerRequest) ([]string, error) {
			t.Error("GenerateAnswers should not be called for non-questions")
			return nil, nil
		},
	}
	engine := NewEngine(mock)

	result, err := engine.ProcessQuestion("I like Kubernetes.")
	if err != nil {
		t.Fatalf("ProcessQuestion() error = %v", err)
	}

	if result.IsQuestion {
		t.Error("\"I like Kubernetes.\" should not be classified as question")
	}

	// Give goroutine time in case it was incorrectly triggered.
	mock.mu.Lock()
	genCalls := len(mock.generateCalls)
	mock.mu.Unlock()
	if genCalls != 0 {
		t.Errorf("GenerateAnswers was called %d times for non-question", genCalls)
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
	}

	for _, tt := range tests {
		got := IsQuestion(tt.text)
		if got != tt.expected {
			t.Errorf("isQuestion(%q) = %v, want %v", tt.text, got, tt.expected)
		}
	}
}

// TestProcessQuestion_LLMError проверяет, что ошибка LLM не паникует.
func TestProcessQuestion_LLMError(t *testing.T) {
	mock := &mockLLMProvider{
		generateFn: func(ctx context.Context, req AnswerRequest) ([]string, error) {
			return nil, fmt.Errorf("simulated error")
		},
	}
	engine := NewEngine(mock)

	result, err := engine.ProcessQuestion("What is Kubernetes?")
	if err != nil {
		t.Fatalf("ProcessQuestion() должен вернуть результат даже при ошибке LLM: %v", err)
	}
	if !result.IsQuestion {
		t.Error("вопрос должен быть классифицирован как вопрос, несмотря на ошибку LLM")
	}
	// Горутина не должна паниковать — просто ждём.
	time.Sleep(100 * time.Millisecond)
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

// TestTranslationResult_GetSetAnswers проверяет конкурентный доступ к answers.
func TestTranslationResult_GetSetAnswers(t *testing.T) {
	r := &TranslationResult{
		IsQuestion: true,
	}

	if answers := r.GetAnswers(); len(answers) != 0 {
		t.Error("GetAnswers на новом результате должен вернуть пустой массив")
	}

	r.SetAnswers([]string{"a1", "a2"})
	answers := r.GetAnswers()
	if len(answers) != 2 {
		t.Errorf("len = %d, want 2", len(answers))
	}

	// Modify returned slice — original should be unaffected.
	answers[0] = "modified"
	answers2 := r.GetAnswers()
	if answers2[0] != "a1" {
		t.Error("GetAnswers должен возвращать копию")
	}
}
