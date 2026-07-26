package translator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

const maxRetries = 3

type OpenAIProvider struct {
	client          *openai.Client
	baseURL         string
	apiKey          string
	model           string
	maxTokens       int
	disableThinking bool
	mu              sync.Mutex
}

func (p *OpenAIProvider) DisableThinking() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.disableThinking = true
}

func NewOpenAIProviderWithConfig(baseURL, apiKey, model string) *OpenAIProvider {
	if model == "" {
		model = "gpt-4o-mini"
	}
	cfg := openai.DefaultConfig(apiKey)
	cfg.BaseURL = baseURL
	return &OpenAIProvider{
		client:  openai.NewClientWithConfig(cfg),
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
	}
}

func (p *OpenAIProvider) SetMaxTokens(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.maxTokens = n
}

func (p *OpenAIProvider) maxTokensOrZero() int {
	if p.maxTokens > 0 {
		return p.maxTokens
	}
	return 0
}

func (p *OpenAIProvider) GenerateAnswers(ctx context.Context, question string, cvContext string) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	userPrompt := BuildAnswerPrompt(question, cvContext)

	if p.disableThinking {
		return p.generateAnswersWithoutThinking(ctx, userPrompt)
	}

	req := openai.ChatCompletionRequest{
		Model: p.model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: SystemPromptAnswerGen},
			{Role: openai.ChatMessageRoleUser, Content: userPrompt},
		},
		Temperature: 0.3,
		MaxTokens:   p.maxTokens,
		N:           1, // explicit: exactly 1 completion choice
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

// generateAnswersWithoutThinking builds and sends a raw HTTP request with
// thinking disabled for GLM models (GLM-4.7-Flash etc.) where thinking is
// enabled by default and consumes the token budget.
func (p *OpenAIProvider) generateAnswersWithoutThinking(ctx context.Context, userPrompt string) ([]string, error) {
	body := map[string]any{
		"model": p.model,
		"messages": []map[string]string{
			{"role": "system", "content": SystemPromptAnswerGen},
			{"role": "user", "content": userPrompt},
		},
		"temperature": 0.3,
		"n":           1,
		"thinking": map[string]string{
			"type": "disabled",
		},
	}
	if p.maxTokens > 0 {
		body["max_tokens"] = p.maxTokens
	}

	resp, err := p.rawChatCompletionWithRetry(ctx, body)
	if err != nil {
		return nil, fmt.Errorf("generate answers (no thinking): %w", err)
	}
	return parseAnswerHints(resp), nil
}

// rawChatCompletionWithRetry sends a raw JSON body to the chat completions
// endpoint with retry logic. Returns the content field from the first choice.
func (p *OpenAIProvider) rawChatCompletionWithRetry(ctx context.Context, body map[string]any) (string, error) {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		content, err := p.rawChatCompletion(ctx, body)
		if err == nil {
			return content, nil
		}
		lastErr = err
		if !isRateLimitError(err) {
			return "", err
		}
		backoff := time.Duration(1<<uint(attempt)) * time.Second
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("retry cancelled: %w", ctx.Err())
		case <-time.After(backoff):
		}
	}
	return "", fmt.Errorf("max retries exceeded: %w", lastErr)
}

// rawChatCompletion sends a raw JSON body to the chat completions endpoint
// and returns the content of the first choice's message.
func (p *OpenAIProvider) rawChatCompletion(ctx context.Context, body map[string]any) (string, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := strings.TrimRight(p.baseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBytes))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	return result.Choices[0].Message.Content, nil
}

func (p *OpenAIProvider) createChatCompletionWithRetry(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		resp, err := p.client.CreateChatCompletion(ctx, req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !isRateLimitError(err) {
			return openai.ChatCompletionResponse{}, err
		}
		backoff := time.Duration(1<<uint(attempt)) * time.Second
		select {
		case <-ctx.Done():
			return openai.ChatCompletionResponse{}, fmt.Errorf("retry cancelled: %w", ctx.Err())
		case <-time.After(backoff):
		}
	}
	return openai.ChatCompletionResponse{}, fmt.Errorf("max retries exceeded: %w", lastErr)
}

func (p *OpenAIProvider) GenerateAnswersStream(ctx context.Context, question string, cvContext string) (<-chan string, error) {
	tokenCh := make(chan string, 64)
	userPrompt := BuildAnswerPrompt(question, cvContext)

	p.mu.Lock()
	maxTok := p.maxTokens
	p.mu.Unlock()

	req := openai.ChatCompletionRequest{
		Model: p.model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: SystemPromptAnswerGen},
			{Role: openai.ChatMessageRoleUser, Content: userPrompt},
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

func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "429") ||
		strings.Contains(errStr, "rate limit") ||
		strings.Contains(errStr, "too many requests")
}

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
	return hints
}

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

var _ LLMProvider = (*OpenAIProvider)(nil)
var _ StreamingAnswersProvider = (*OpenAIProvider)(nil)
