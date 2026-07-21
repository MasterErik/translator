package translator

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// maxRetries is the maximum number of retry attempts for rate-limited requests.
const maxRetries = 3

// OpenAIProvider implements LLMProvider using the OpenAI API (GPT-4o-mini).
// It wraps the go-openai client and adds rate-limit retry logic with
// exponential backoff.
//
// All exported methods are safe for concurrent use.
type OpenAIProvider struct {
	client *openai.Client
	model  string
	mu     sync.Mutex
}

// NewOpenAIProvider creates a new OpenAI-backed LLM provider.
// If model is empty, it defaults to "gpt-4o-mini".
func NewOpenAIProvider(apiKey, model string) *OpenAIProvider {
	if model == "" {
		model = "gpt-4o-mini"
	}
	return &OpenAIProvider{
		client: openai.NewClient(apiKey),
		model:  model,
	}
}

// Translate sends the text and conversation history to the OpenAI API
// and returns the Russian translation. It uses a low temperature (0.1)
// for deterministic output and preserves IT terminology as instructed
// by the system prompt.
//
// The call respects context deadlines and retries on HTTP 429
// with exponential backoff (1s, 2s, 4s).
func (p *OpenAIProvider) Translate(ctx context.Context, text string, history []string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	userPrompt := BuildTranslationPrompt(text, history)

	req := openai.ChatCompletionRequest{
		Model: p.model,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: SystemPromptTranslation,
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: userPrompt,
			},
		},
		Temperature: 0.1,
	}

	resp, err := p.createChatCompletionWithRetry(ctx, req)
	if err != nil {
		return "", fmt.Errorf("translate: %w", err)
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("translate: no choices in response")
	}

	return strings.TrimSpace(resp.Choices[0].Message.Content), nil
}

// GenerateAnswers sends the detected question and CV context to the OpenAI
// API and returns 2-3 candidate answer hints parsed from the response.
// It uses temperature 0.3 for slightly varied output.
//
// The response is split by newlines and bullet markers are stripped.
// The call respects context deadlines and retries on HTTP 429
// with exponential backoff (1s, 2s, 4s).
func (p *OpenAIProvider) GenerateAnswers(ctx context.Context, question string, cvContext string) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	userPrompt := BuildAnswerPrompt(question, cvContext)

	req := openai.ChatCompletionRequest{
		Model: p.model,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: SystemPromptAnswerGen,
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: userPrompt,
			},
		},
		Temperature: 0.3,
	}

	resp, err := p.createChatCompletionWithRetry(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("generate answers: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("generate answers: no choices in response")
	}

	return parseAnswerHints(resp.Choices[0].Message.Content), nil
}

// createChatCompletionWithRetry sends the request to OpenAI and retries
// on HTTP 429 (Too Many Requests) with exponential backoff: 1s, 2s, 4s.
// Other errors are returned immediately.
func (p *OpenAIProvider) createChatCompletionWithRetry(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		resp, err := p.client.CreateChatCompletion(ctx, req)
		if err == nil {
			return resp, nil
		}

		lastErr = err

		// Check if the error is a rate-limit error (429).
		if !isRateLimitError(err) {
			return openai.ChatCompletionResponse{}, err
		}

		// Exponential backoff: 1s, 2s, 4s.
		backoff := time.Duration(1<<uint(attempt)) * time.Second

		select {
		case <-ctx.Done():
			return openai.ChatCompletionResponse{}, fmt.Errorf("retry cancelled: %w", ctx.Err())
		case <-time.After(backoff):
			// Continue to next retry attempt.
		}
	}

	return openai.ChatCompletionResponse{}, fmt.Errorf("max retries exceeded: %w", lastErr)
}

// isRateLimitError checks if the error message indicates an HTTP 429
// rate-limit response from the OpenAI API.
func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "429") ||
		strings.Contains(errStr, "rate limit") ||
		strings.Contains(errStr, "too many requests")
}

// parseAnswerHints splits the raw LLM response into individual answer
// hints. It strips leading numbering (e.g., "1. " or "- ") and filters
// out empty lines.
func parseAnswerHints(raw string) []string {
	lines := strings.Split(raw, "\n")
	var hints []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Strip common bullet/numbering prefixes.
		clean := stripBulletPrefix(trimmed)
		if clean != "" {
			hints = append(hints, clean)
		}
	}

	// Limit to at most 3 hints.
	if len(hints) > 3 {
		hints = hints[:3]
	}

	return hints
}

// stripBulletPrefix removes common list markers from the beginning of a line.
// Supported formats: "1. ", "1) ", "- ", "* ", "• ".
// If the entire string is just a bullet marker (e.g., "-" or "1."), returns "".
func stripBulletPrefix(s string) string {
	// Try numbered prefixes like "1. " or "1) ".
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			continue
		}
		if i > 0 && (s[i] == '.' || s[i] == ')') {
			if i+1 < len(s) && s[i+1] == ' ' {
				return strings.TrimSpace(s[i+2:])
			}
			// Bare numbered marker like "1." or "1)" with nothing after.
			return ""
		}
		break
	}

	// Try bullet markers.
	if len(s) >= 2 && s[1] == ' ' {
		switch s[0] {
		case '-', '*':
			return strings.TrimSpace(s[2:])
		}
	}
	// Bare bullet marker like "-" or "*" with nothing after.
	if len(s) == 1 && (s[0] == '-' || s[0] == '*') {
		return ""
	}

	// Handle Unicode bullet (•) which is 3 bytes in UTF-8.
	if strings.HasPrefix(s, "• ") {
		return strings.TrimSpace(s[3:])
	}
	// Bare Unicode bullet.
	if s == "•" {
		return ""
	}

	return s
}
