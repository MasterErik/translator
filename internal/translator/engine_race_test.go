package translator

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestEngine_ConcurrentProcessFinalTranscript validates that multiple
// goroutines can call ProcessFinalTranscript concurrently without
// data races on the sliding window or the result.
func TestEngine_ConcurrentProcessFinalTranscript(t *testing.T) {
	mock := &mockLLMProvider{
		translateFn: func(ctx context.Context, text string, history []string) (string, error) {
			return "translated: " + text, nil
		},
	}
	engine := NewEngine(mock, 10)

	var wg sync.WaitGroup
	numGoroutines := 10
	texts := []string{
		"What is Kubernetes?",
		"How does Docker work?",
		"Explain CI/CD pipelines.",
		"I have 5 years of experience.",
		"Tell me about microservices.",
		"My stack includes Go and TypeScript.",
		"What is your approach to testing?",
		"I prefer Agile methodology.",
		"Can you describe CQRS?",
		"We use PostgreSQL and Redis.",
	}

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := engine.ProcessFinalTranscript(context.Background(), texts[idx])
			if err != nil {
				t.Errorf("ProcessFinalTranscript() error in goroutine %d: %v", idx, err)
			}
		}(i)
	}

	wg.Wait()

	// Verify window integrity after concurrent access.
	engine.mu.RLock()
	windowLen := len(engine.window)
	engine.mu.RUnlock()

	if windowLen != 10 {
		t.Errorf("Window length = %d, want 10 after %d concurrent adds", windowLen, numGoroutines)
	}
}

// TestEngine_ConcurrentWindowAccess validates that reads and writes
// to the sliding window do not race.
func TestEngine_ConcurrentWindowAccess(t *testing.T) {
	mock := &mockLLMProvider{}
	engine := NewEngine(mock, 20)

	var wg sync.WaitGroup
	ctx := context.Background()

	// Writers: add items to the window.
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_, err := engine.ProcessFinalTranscript(ctx, "concurrent text")
				if err != nil {
					t.Errorf("ProcessFinalTranscript() error: %v", err)
				}
			}
		}()
	}

	// Readers: read the window.
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				engine.mu.RLock()
				_ = len(engine.window)
				engine.mu.RUnlock()
			}
		}()
	}

	wg.Wait()
}

// TestEngine_ConcurrentQuestionAndAnswers validates that question
// detection runs safely under concurrent access.
func TestEngine_ConcurrentQuestionAndAnswers(t *testing.T) {
	var genMu sync.Mutex
	var genCallCount int

	mock := &mockLLMProvider{
		generateFn: func(ctx context.Context, question string, cvContext string) ([]string, error) {
			genMu.Lock()
			genCallCount++
			genMu.Unlock()
			return []string{"hint 1", "hint 2"}, nil
		},
	}
	engine := NewEngine(mock, 15)

	var wg sync.WaitGroup
	questions := []string{
		"What is Kubernetes?",
		"How does Docker work?",
		"Explain CI/CD.",
		"Can you describe your experience?",
		"What is your approach to TDD?",
	}

	for _, q := range questions {
		wg.Add(1)
		go func(question string) {
			defer wg.Done()
			result, err := engine.ProcessFinalTranscript(context.Background(), question)
			if err != nil {
				t.Errorf("ProcessFinalTranscript() error: %v", err)
				return
			}
			if !result.IsQuestion {
				t.Errorf("Expected %q to be classified as question", question)
			}
		}(q)
	}

	wg.Wait()

	// Асинхронная генерация подсказок запускается в горутинах внутри
	// ProcessFinalTranscript. Ждём до 1 секунды пока хотя бы один вызов
	// GenerateAnswers зарегистрируется.
	for i := 0; i < 20; i++ {
		genMu.Lock()
		calls := genCallCount
		genMu.Unlock()
		if calls > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}

	genMu.Lock()
	calls := genCallCount
	genMu.Unlock()
	if calls == 0 {
		t.Error("Expected at least some GenerateAnswers calls")
	}
}
