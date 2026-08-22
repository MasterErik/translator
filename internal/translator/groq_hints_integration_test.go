//go:build integration

package translator

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/joho/godotenv"
	openai "github.com/sashabaranov/go-openai"
)

func envInt(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// TestGroqHints_RealAnswers вызывает Groq напрямую и логирует СЫРОЙ content
// ответа — это главное для диагностики пустых подсказок.
func TestGroqHints_RealAnswers(t *testing.T) {
	_ = godotenv.Load("../../.env")

	apiKey := os.Getenv("LLM_API_KEY")
	if apiKey == "" {
		t.Skip("LLM_API_KEY not set — skipping Groq integration test")
	}
	baseURL := os.Getenv("LLM_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.groq.com/openai/v1"
	}
	model := os.Getenv("LLM_MODEL")
	if model == "" {
		model = "openai/gpt-oss-20b"
	}
	maxTokens := envInt("LLM_MAX_TOKENS", 1024)

	prov := NewChatProvider(baseURL, apiKey, model)
	prov.SetMaxTokens(maxTokens)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	t.Logf("provider: model=%s baseURL=%s maxTokens=%d", model, baseURL, maxTokens)

	t.Run("no_cv_context", func(t *testing.T) {
		for _, q := range []string{
			"What is your experience with Kubernetes?",
			"How do you handle a deadlock in Go?",
		} {
			t.Run(q, func(t *testing.T) {
				raw := rawContent(t, ctx, prov, model, q, "", maxTokens)
				hints := parseAnswerHints(raw)
				t.Logf("parseAnswerHints: %d подсказок: %v", len(hints), hints)

				answers, err := prov.GenerateAnswers(ctx, AnswerRequest{Question: q})
				if err != nil {
					t.Fatalf("GenerateAnswers() error = %v", err)
				}
				t.Logf("GenerateAnswers: %d подсказок: %v", len(answers), answers)

				if len(answers) == 0 {
					t.Errorf("пустой ответ для вопроса %q — сырой content выше", q)
				}
			})
		}
	})

	t.Run("with_cv_context", func(t *testing.T) {
		// CV-контекст — просто текст резюме БЕЗ правил формата. Раньше он
		// полностью заменял SystemPromptAnswerGen → модель отвечала без
		// «EN: … | RU: …» → parseAnswerHints отбрасывал всё (регрессия).
		cv := "Senior Go developer, 5+ years, expert in concurrency patterns and microservices."
		q := "Tell me about a project where you used microservices."

		raw := rawContent(t, ctx, prov, model, q, cv, maxTokens)
		hints := parseAnswerHints(raw)
		t.Logf("parseAnswerHints: %d подсказок: %v", len(hints), hints)

		answers, err := prov.GenerateAnswers(ctx, AnswerRequest{Question: q, CandidateContext: cv})
		if err != nil {
			t.Fatalf("GenerateAnswers() error = %v", err)
		}
		t.Logf("GenerateAnswers: %d подсказок: %v", len(answers), answers)

		if len(answers) == 0 {
			t.Errorf("пустой ответ с cvContext=%q — сырой content выше", cv)
		}
	})
}

// rawContent делает прямой вызов CreateChatCompletion с тем же промптом, что
// строит GenerateAnswers, и логирует сырой content + finish_reason.
func rawContent(t *testing.T, ctx context.Context, prov *ChatProvider, model, question, cvContext string, maxTokens int) string {
	t.Helper()

	req := openai.ChatCompletionRequest{
		Model: model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: buildSystemPrompt(cvContext)},
			{Role: openai.ChatMessageRoleUser, Content: BuildAnswerPrompt(AnswerRequest{Question: question, CandidateContext: cvContext})},
		},
		Temperature: 0.3,
		MaxTokens:   maxTokens,
		N:           1,
	}

	resp, err := prov.client.CreateChatCompletion(ctx, req)
	if err != nil {
		t.Fatalf("CreateChatCompletion() error = %v", err)
	}
	if len(resp.Choices) == 0 {
		t.Fatal("нет choices в ответе")
	}

	raw := resp.Choices[0].Message.Content
	finish := resp.Choices[0].FinishReason

	t.Logf("RAW CONTENT (%d chars, finish_reason=%s):\n%s\n---", len(raw), finish, raw)
	return raw
}
