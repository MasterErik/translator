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

// maxAnswerHints ограничивает число возвращаемых подсказок.
// Защита от «мусорных» ответов reasoning-моделей, которые генерируют
// десятки строк вместо одной.
const maxAnswerHints = 3

func parseAnswerHints(raw string) []string {
	// Удаляем reasoning-блок Groq (<think>…</think>) целиком. Внутри него
	// строки могут содержать «EN:», «RU:» и «|» (модель рассуждает о формате),
	// поэтому построчная фильтрация по формату не справится — блок вырезаем.
	raw = stripThinking(raw)

	lines := strings.Split(raw, "\n")
	var hints []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		clean := stripBulletPrefix(trimmed)
		if clean == "" {
			continue
		}
		// Отбрасываем reasoning-мусор (chain-of-thought): оставляем только
		// строки в формате подсказки «EN: <English> | RU: <Russian>».
		if !isHintLine(clean) {
			continue
		}
		hints = append(hints, clean)
	}
	// Ограничиваем количество подсказок (защита от «83 подсказок»-мусора).
	if len(hints) > maxAnswerHints {
		hints = hints[:maxAnswerHints]
	}
	return hints
}

// stripThinking удаляет из ответа все блоки <think>…</think>. Groq для
// qwen3.6-27b кладёт chain-of-thought прямо в content именно в таких тегах
// (поля reasoning/reasoning_content при этом пустые). Удаляем блоки целиком,
// так как отдельные строки внутри могут выглядеть как валидные подсказки.
func stripThinking(s string) string {
	for {
		start := strings.Index(s, "<think>")
		if start == -1 {
			return s
		}
		end := strings.Index(s[start:], "</think>")
		if end == -1 {
			// Нет закрывающего тега — вырезаем всё до конца строки.
			return s[:start]
		}
		end += start + len("</think>")
		s = s[:start] + s[end:]
	}
}

// isHintLine определяет, соответствует ли строка формату подсказки
// «EN: <English> | RU: <Russian>» (см. SystemPromptAnswerGen и CV_CONTEXT).
// Reasoning-строки (chain-of-thought) такого формата не содержат и отбрасываются.
func isHintLine(line string) bool {
	// Groq для qwen3.6-27b кладёт reasoning (chain-of-thought) прямо в
	// content внутри тегов <think>…</think>. Отбрасываем такие строки явно —
	// они точно не являются подсказкой, даже если случайно содержат «|».
	if strings.Contains(line, "<think>") || strings.Contains(line, "</think>") {
		return false
	}
	if !strings.Contains(line, "|") {
		return false
	}
	upper := strings.ToUpper(line)
	return strings.Contains(upper, "EN:") && strings.Contains(upper, "RU:")
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
