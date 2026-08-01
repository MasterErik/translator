//go:build integration

package translator

import (
	"context"
	"os"
	"testing"
)

// TestIntegration_ChatProvider тестирует базовую генерацию через ChatProvider
// с реальным API (настраивается через LLM_API_KEY/LLM_BASE_URL/LLM_MODEL).
func TestIntegration_ChatProvider(t *testing.T) {
	apiKey := os.Getenv("LLM_API_KEY")
	baseURL := os.Getenv("LLM_BASE_URL")
	model := os.Getenv("LLM_MODEL")

	if apiKey == "" || baseURL == "" {
		t.Skip("LLM_API_KEY or LLM_BASE_URL not set, skipping integration test")
	}
	if model == "" {
		model = "llama-3.3-70b-versatile"
	}

	t.Logf("Model: %s, base URL: %s", model, baseURL)

	t.Run("raw_api", func(t *testing.T) {
		rawJSON := sendChatRequest(t, baseURL, apiKey, model, "What is Redis?", false)
		parsed := parseChatResponse(t, rawJSON)
		t.Logf("content:           %q", truncate(parsed.Content, 200))
		t.Logf("reasoning_content: %q", truncate(parsed.ReasoningContent, 200))
		if parsed.Content == "" {
			t.Fatal("ChatProvider returned empty content")
		}
	})

	t.Run("generate_answers", func(t *testing.T) {
		provider := NewChatProvider(baseURL, apiKey, model)
		answers, err := provider.GenerateAnswers(context.Background(), "What is Redis?", "")
		if err != nil {
			t.Fatalf("GenerateAnswers error: %v", err)
		}
		t.Logf("Answers: %d", len(answers))
		for i, a := range answers {
			t.Logf("  [%d] %q", i, a)
		}
		if len(answers) == 0 {
			t.Fatal("ChatProvider returned empty answers")
		}
	})
}
