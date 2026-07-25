package translator

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestEngine_ConcurrentProcessQuestion validates that multiple
// goroutines can call ProcessQuestion concurrently without
// data races on the result.
func TestEngine_ConcurrentProcessQuestion(t *testing.T) {
	mock := &mockLLMProvider{}
	engine := NewEngine(mock)

	var wg sync.WaitGroup
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

	for _, text := range texts {
		wg.Add(1)
		go func(txt string) {
			defer wg.Done()
			_, err := engine.ProcessQuestion(txt)
			if err != nil {
				t.Errorf("ProcessQuestion() error: %v", err)
			}
		}(text)
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
	engine := NewEngine(mock)

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
			result, err := engine.ProcessQuestion(question)
			if err != nil {
				t.Errorf("ProcessQuestion() error: %v", err)
				return
			}
			if !result.IsQuestion {
				t.Errorf("Expected %q to be classified as question", question)
			}
		}(q)
	}

	wg.Wait()

	// Асинхронная генерация подсказок запускается в горутинах внутри
	// ProcessQuestion. Ждём до 1 секунды пока хотя бы один вызов
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
