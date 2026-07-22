package translator

import (
	"context"
	"sync"
	"testing"
	"time"
)

// mockLLMProvider is a test stub implementing LLMProvider.
type mockLLMProvider struct {
	mu             sync.Mutex
	translateCalls []translateCall
	translateFn    func(ctx context.Context, text string, history []string) (string, error)
	generateCalls  []generateCall
	generateFn     func(ctx context.Context, question string, cvContext string) ([]string, error)
	translateDelay chan struct{} // if set, Translate blocks until this channel is closed
}

type translateCall struct {
	text    string
	history []string
}

type generateCall struct {
	question  string
	cvContext string
}

func (m *mockLLMProvider) Translate(ctx context.Context, text string, history []string) (string, error) {
	m.mu.Lock()
	m.translateCalls = append(m.translateCalls, translateCall{text, history})
	fn := m.translateFn
	delay := m.translateDelay
	m.mu.Unlock()

	if delay != nil {
		select {
		case <-delay:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	if fn != nil {
		return fn(ctx, text, history)
	}
	return "[translated] " + text, nil
}

func (m *mockLLMProvider) GenerateAnswers(ctx context.Context, question string, cvContext string) ([]string, error) {
	m.mu.Lock()
	m.generateCalls = append(m.generateCalls, generateCall{question, cvContext})
	fn := m.generateFn
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, question, cvContext)
	}
	return []string{"hint 1", "hint 2"}, nil
}

func TestNewEngineDefaultWindow(t *testing.T) {
	mock := &mockLLMProvider{}
	engine := NewEngine(mock, 0)

	if engine.maxWindow != 5 {
		t.Errorf("Default maxWindow = %d, want 5", engine.maxWindow)
	}
}

func TestNewEngineCustomWindow(t *testing.T) {
	mock := &mockLLMProvider{}
	engine := NewEngine(mock, 10)

	if engine.maxWindow != 10 {
		t.Errorf("maxWindow = %d, want 10", engine.maxWindow)
	}
}

func TestProcessFinalTranscript_Translation(t *testing.T) {
	mock := &mockLLMProvider{
		translateFn: func(ctx context.Context, text string, history []string) (string, error) {
			return "Привет, мир", nil
		},
	}
	engine := NewEngine(mock, 3)

	result, err := engine.ProcessFinalTranscript(context.Background(), "Hello, world")
	if err != nil {
		t.Fatalf("ProcessFinalTranscript() error = %v", err)
	}

	if result.Translation != "Привет, мир" {
		t.Errorf("Translation = %q, want %q", result.Translation, "Привет, мир")
	}

	if result.IsQuestion {
		t.Error("Should not classify 'Hello, world' as a question")
	}
}

func TestProcessFinalTranscript_QuestionClassification_Positive(t *testing.T) {
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
			engine := NewEngine(mock, 3)

			result, err := engine.ProcessFinalTranscript(context.Background(), tt.text)
			if err != nil {
				t.Fatalf("ProcessFinalTranscript() error = %v", err)
			}

			if !result.IsQuestion {
				t.Errorf("Should classify %q as a question", tt.text)
			}
		})
	}
}

func TestProcessFinalTranscript_QuestionClassification_Negative(t *testing.T) {
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
			engine := NewEngine(mock, 3)

			result, err := engine.ProcessFinalTranscript(context.Background(), tt.text)
			if err != nil {
				t.Fatalf("ProcessFinalTranscript() error = %v", err)
			}

			if result.IsQuestion {
				t.Errorf("Should NOT classify %q as a question", tt.text)
			}
		})
	}
}

func TestSlidingWindow_FIFO(t *testing.T) {
	mock := &mockLLMProvider{}
	engine := NewEngine(mock, 3)

	ctx := context.Background()

	// Add 5 items; window should keep only the last 3.
	texts := []string{"A", "B", "C", "D", "E"}
	for _, text := range texts {
		_, err := engine.ProcessFinalTranscript(ctx, text)
		if err != nil {
			t.Fatalf("ProcessFinalTranscript() error = %v", err)
		}
	}

	engine.mu.RLock()
	window := engine.window
	engine.mu.RUnlock()

	if len(window) != 3 {
		t.Fatalf("Window length = %d, want 3", len(window))
	}

	expected := []string{"C", "D", "E"}
	for i, exp := range expected {
		if window[i] != exp {
			t.Errorf("Window[%d] = %q, want %q", i, window[i], exp)
		}
	}
}

func TestSlidingWindow_HistoryContext(t *testing.T) {
	var capturedHistory []string
	mock := &mockLLMProvider{
		translateFn: func(ctx context.Context, text string, history []string) (string, error) {
			// Capture the history passed to Translate.
			capturedHistory = make([]string, len(history))
			copy(capturedHistory, history)
			return "translated", nil
		},
	}
	engine := NewEngine(mock, 5)

	ctx := context.Background()

	// First call: no history.
	engine.ProcessFinalTranscript(ctx, "First")
	if len(capturedHistory) != 0 {
		t.Errorf("First call history = %v, want empty", capturedHistory)
	}

	// Second call: history should contain "First".
	engine.ProcessFinalTranscript(ctx, "Second")
	if len(capturedHistory) != 1 || capturedHistory[0] != "First" {
		t.Errorf("Second call history = %v, want [First]", capturedHistory)
	}

	// Third call: history should contain "First", "Second".
	engine.ProcessFinalTranscript(ctx, "Third")
	if len(capturedHistory) != 2 {
		t.Errorf("Third call history length = %d, want 2. Got: %v", len(capturedHistory), capturedHistory)
	}
}

func TestSlidingWindow_MaxSize(t *testing.T) {
	mock := &mockLLMProvider{}
	engine := NewEngine(mock, 2)

	ctx := context.Background()

	for i := 0; i < 10; i++ {
		_, err := engine.ProcessFinalTranscript(ctx, "text")
		if err != nil {
			t.Fatalf("ProcessFinalTranscript() error = %v", err)
		}
	}

	engine.mu.RLock()
	length := len(engine.window)
	engine.mu.RUnlock()

	if length != 2 {
		t.Errorf("Window length after 10 adds = %d, want 2", length)
	}
}

func TestProcessFinalTranscript_AnswerGenerationTriggered(t *testing.T) {
	mock := &mockLLMProvider{
		generateFn: func(ctx context.Context, question string, cvContext string) ([]string, error) {
			return []string{"hint about " + question}, nil
		},
	}
	engine := NewEngine(mock, 5)

	result, err := engine.ProcessFinalTranscript(context.Background(), "What is Docker?")
	if err != nil {
		t.Fatalf("ProcessFinalTranscript() error = %v", err)
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

func TestProcessFinalTranscript_NoAnswerForNonQuestion(t *testing.T) {
	mock := &mockLLMProvider{
		generateFn: func(ctx context.Context, question string, cvContext string) ([]string, error) {
			t.Error("GenerateAnswers should not be called for non-questions")
			return nil, nil
		},
	}
	engine := NewEngine(mock, 5)

	result, err := engine.ProcessFinalTranscript(context.Background(), "I like Kubernetes.")
	if err != nil {
		t.Fatalf("ProcessFinalTranscript() error = %v", err)
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
		got := isQuestion(tt.text)
		if got != tt.expected {
			t.Errorf("isQuestion(%q) = %v, want %v", tt.text, got, tt.expected)
		}
	}
}
