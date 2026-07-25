package translator

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
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
	client          *openai.Client
	model           string
	maxTokens       int
	disableThinking bool
	mu              sync.Mutex
}

// thinkingTransport injects "thinking":{"type":"disabled"} into JSON request bodies.
// GLM-4.7-Flash defaults to reasoning mode; this disables it for plain translation.
type thinkingTransport struct {
	base http.RoundTripper
}

func (t *thinkingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		body, err := io.ReadAll(req.Body)
		req.Body.Close()
		if err == nil && strings.Contains(req.URL.Host, "api.z.ai") {
			// Inject "thinking":{"type":"disabled"} after the opening brace.
			body = bytes.Replace(body, []byte(`{`), []byte(`{"thinking":{"type":"disabled"},`), 1)
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
	}
	return t.base.RoundTrip(req)
}

// DisableThinking disables reasoning/thinking mode for models that default to it
// (e.g. GLM-4.7-Flash). This ensures all tokens go to content, not reasoning_content.
func (p *OpenAIProvider) DisableThinking() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.disableThinking = true
}

// NewOpenAIProviderWithConfig creates a new LLM provider with a custom
// base URL and API key. Supports any OpenAI-compatible API.
//
// baseURL — e.g. "https://api.openai.com/v1" or "https://api.deepinfra.com/v1/openai".
// apiKey  — the API key for the provider.
// model   — model name; defaults to "gpt-4o-mini" if empty.
func NewOpenAIProviderWithConfig(baseURL, apiKey, model string) *OpenAIProvider {
	if model == "" {
		model = "gpt-4o-mini"
	}
	cfg := openai.DefaultConfig(apiKey)
	cfg.BaseURL = baseURL
	// Inject thinking-disabled transport for Z.AI (GLM-4.7-Flash reasoning mode).
	cfg.HTTPClient = &http.Client{
		Transport: &thinkingTransport{base: http.DefaultTransport},
	}
	return &OpenAIProvider{
		client: openai.NewClientWithConfig(cfg),
		model:  model,
	}
}

// SetMaxTokens sets the maximum number of output tokens for all requests.
// 0 means provider default (unlimited).
func (p *OpenAIProvider) SetMaxTokens(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.maxTokens = n
}

// maxTokensOrZero returns MaxTokens only if > 0 (0 means "not set" in API).
func (p *OpenAIProvider) maxTokensOrZero() int {
	if p.maxTokens > 0 {
		return p.maxTokens
	}
	return 0
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
		MaxTokens:   p.maxTokens,
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

// GenerateAnswersStream генерирует подсказки потоково через SSE.
// Токены доставляются по одному через возвращаемый канал.
// Канал закрывается по завершении генерации или при ошибке.
func (p *OpenAIProvider) GenerateAnswersStream(ctx context.Context, question string, cvContext string) (<-chan string, error) {
	tokenCh := make(chan string, 64)

	userPrompt := BuildAnswerPrompt(question, cvContext)

	p.mu.Lock()
	maxTok := p.maxTokens
	p.mu.Unlock()

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
		MaxTokens:   maxTok,
		Stream:      true,
	}

	stream, err := p.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		close(tokenCh)
		return nil, fmt.Errorf("generate answers stream: %w", err)
	}

	go func() {
		defer close(tokenCh)
		defer stream.Close()

		for {
			response, recvErr := stream.Recv()
			if recvErr != nil {
				if recvErr.Error() == "EOF" {
					break
				}
				if ctx.Err() != nil {
					return
				}
				select {
				case tokenCh <- "[ERROR: " + recvErr.Error() + "]":
				case <-ctx.Done():
				}
				return
			}

			if len(response.Choices) > 0 {
				delta := response.Choices[0].Delta.Content
				if delta != "" {
					select {
					case tokenCh <- delta:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()

	return tokenCh, nil
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

		clean := stripBulletPrefix(trimmed)
		if clean != "" {
			hints = append(hints, clean)
		}
	}

	if len(hints) > 3 {
		hints = hints[:3]
	}

	return hints
}

// stripBulletPrefix removes common list markers from the beginning of a line.
// Supported formats: "1. ", "1) ", "- ", "* ", "• ".
func stripBulletPrefix(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			continue
		}
		if i > 0 && (s[i] == '.' || s[i] == ')') {
			if i+1 < len(s) && s[i+1] == ' ' {
				return strings.TrimSpace(s[i+2:])
			}
			return ""
		}
		break
	}

	if len(s) >= 2 && s[1] == ' ' {
		switch s[0] {
		case '-', '*':
			return strings.TrimSpace(s[2:])
		}
	}
	if len(s) == 1 && (s[0] == '-' || s[0] == '*') {
		return ""
	}

	if strings.HasPrefix(s, "• ") {
		return strings.TrimSpace(s[3:])
	}
	if s == "•" {
		return ""
	}

	return s
}

// Compile-time interface check.
var _ LLMProvider = (*OpenAIProvider)(nil)
var _ StreamingAnswersProvider = (*OpenAIProvider)(nil)
