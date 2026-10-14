package translator

import (
	"context"
	"sync"
	"testing"
)

// TestEngine_ConcurrentGenerateAnswers validates that multiple goroutines
// can call GenerateAnswers (the live dispatcher contract) concurrently
// without data races.
func TestEngine_ConcurrentGenerateAnswers(t *testing.T) {
	mock := &mockLLMProvider{}
	engine := NewEngine(mock)

	var wg sync.WaitGroup
	questions := []string{
		"What is Kubernetes?",
		"How does Docker work?",
		"Explain CI/CD pipelines.",
		"Tell me about microservices.",
		"Can you describe CQRS?",
		"What is your approach to testing?",
		"How do you handle race conditions?",
		"Why did you choose Go?",
		"What are your weaknesses?",
		"Where do you see yourself in 5 years?",
	}

	for _, q := range questions {
		wg.Add(1)
		go func(question string) {
			defer wg.Done()
			answers, err := engine.GenerateAnswers(context.Background(), AnswerRequest{Question: question})
			if err != nil {
				t.Errorf("GenerateAnswers() error: %v", err)
				return
			}
			if len(answers) == 0 {
				t.Errorf("GenerateAnswers(%q) returned no answers", question)
			}
		}(q)
	}

	wg.Wait()
}

// TestEngine_ConcurrentIsQuestion validates that the pure IsQuestion
// classifier runs safely under concurrent access.
func TestEngine_ConcurrentIsQuestion(t *testing.T) {
	texts := []string{
		"What is Kubernetes?",
		"How does Docker work?",
		"Explain CI/CD.",
		"Can you describe your experience?",
		"What is your approach to TDD?",
		"I have 5 years of experience.",
		"however, I did it differently",
		"whatever you say",
	}

	var wg sync.WaitGroup
	for _, txt := range texts {
		wg.Add(1)
		go func(s string) {
			defer wg.Done()
			_ = IsQuestion(s)
		}(txt)
	}
	wg.Wait()
}
