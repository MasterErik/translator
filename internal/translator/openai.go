package translator

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

const maxRetries = 3

type ChatProvider struct {
	client    *openai.Client
	baseURL   string
	apiKey    string
	model     string
	maxTokens int
	mu        sync.Mutex
}

func NewChatProvider(baseURL, apiKey, model string) *ChatProvider {
	cfg := openai.DefaultConfig(apiKey)
	cfg.BaseURL = baseURL
	return &ChatProvider{
		client:  openai.NewClientWithConfig(cfg),
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
	}
}

func (p *ChatProvider) SetMaxTokens(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.maxTokens = n
}

func (p *ChatProvider) GenerateAnswers(ctx context.Context, question string, cvContext string) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	userPrompt := BuildAnswerPrompt(question)

	systemPrompt := cvContext
	if systemPrompt == "" {
		systemPrompt = SystemPromptAnswerGen
	}

	req := openai.ChatCompletionRequest{
		Model: p.model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
			{Role: openai.ChatMessageRoleUser, Content: userPrompt},
		},
		Temperature: 0.3,
		MaxTokens:   p.maxTokens,
		N:           1,
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

func (p *ChatProvider) GenerateAnswersStream(ctx context.Context, question string, cvContext string) (<-chan string, error) {
	tokenCh := make(chan string, 64)
	userPrompt := BuildAnswerPrompt(question)

	systemPrompt := cvContext
	if systemPrompt == "" {
		systemPrompt = SystemPromptAnswerGen
	}

	p.mu.Lock()
	maxTok := p.maxTokens
	p.mu.Unlock()

	req := openai.ChatCompletionRequest{
		Model: p.model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
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

func (p *ChatProvider) createChatCompletionWithRetry(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
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

var _ LLMProvider = (*ChatProvider)(nil)
var _ StreamingAnswersProvider = (*ChatProvider)(nil)
