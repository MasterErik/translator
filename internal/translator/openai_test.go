package translator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// newTestOpenAIProvider creates an OpenAIProvider pointed at a mock server.
func newTestOpenAIProvider(server *httptest.Server, model string) *OpenAIProvider {
	cfg := openai.DefaultConfig("sk-test-key")
	cfg.BaseURL = server.URL + "/v1"
	return &OpenAIProvider{
		client: openai.NewClientWithConfig(cfg),
		model:  modelOrDefault(model),
	}
}

func modelOrDefault(model string) string {
	if model == "" {
		return "gpt-4o-mini"
	}
	return model
}

func setupMockServer(handler http.HandlerFunc) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the request path.
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		handler(w, r)
	}))
}

func TestOpenAIProvider_Translate_Success(t *testing.T) {
	server := setupMockServer(func(w http.ResponseWriter, r *http.Request) {
		resp := openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionChoice{
				{
					Message: openai.ChatCompletionMessage{
						Content: "Привет, рад познакомиться.",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	provider := newTestOpenAIProvider(server, "gpt-4o-mini")
	ctx := context.Background()

	result, err := provider.Translate(ctx, "Hello, nice to meet you.", nil)
	if err != nil {
		t.Fatalf("Translate() error = %v", err)
	}

	if result != "Привет, рад познакомиться." {
		t.Errorf("Translate() = %q, want %q", result, "Привет, рад познакомиться.")
	}
}

func TestOpenAIProvider_Translate_WithHistory(t *testing.T) {
	server := setupMockServer(func(w http.ResponseWriter, r *http.Request) {
		resp := openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionChoice{
				{
					Message: openai.ChatCompletionMessage{
						Content: "У меня 5 лет опыта с Docker.",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	provider := newTestOpenAIProvider(server, "gpt-4o-mini")
	ctx := context.Background()

	history := []string{"Hello, nice to meet you.", "What is your experience?"}
	result, err := provider.Translate(ctx, "I have 5 years of Docker experience.", history)
	if err != nil {
		t.Fatalf("Translate() error = %v", err)
	}

	if result != "У меня 5 лет опыта с Docker." {
		t.Errorf("Translate() = %q, want %q", result, "У меня 5 лет опыта с Docker.")
	}
}

func TestOpenAIProvider_GenerateAnswers_Success(t *testing.T) {
	server := setupMockServer(func(w http.ResponseWriter, r *http.Request) {
		resp := openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionChoice{
				{
					Message: openai.ChatCompletionMessage{
						Content: "- Расскажи про свой опыт с Kubernetes: 3 года, Helm\n- Упомяни про CI/CD пайплайны на GitHub Actions\n- Опиши мониторинг через Prometheus",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	provider := newTestOpenAIProvider(server, "gpt-4o-mini")
	ctx := context.Background()

	answers, err := provider.GenerateAnswers(ctx, "What is your DevOps experience?", "DevOps engineer, 5 years")
	if err != nil {
		t.Fatalf("GenerateAnswers() error = %v", err)
	}

	if len(answers) != 3 {
		t.Fatalf("GenerateAnswers() returned %d answers, want 3", len(answers))
	}

	expectedFirst := "Расскажи про свой опыт с Kubernetes: 3 года, Helm"
	if answers[0] != expectedFirst {
		t.Errorf("GenerateAnswers()[0] = %q, want %q", answers[0], expectedFirst)
	}
}

func TestOpenAIProvider_GenerateAnswers_EmptyCV(t *testing.T) {
	server := setupMockServer(func(w http.ResponseWriter, r *http.Request) {
		resp := openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionChoice{
				{
					Message: openai.ChatCompletionMessage{
						Content: "- Объясни концепцию TDD: сначала тесты, потом код",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	provider := newTestOpenAIProvider(server, "gpt-4o-mini")
	ctx := context.Background()

	answers, err := provider.GenerateAnswers(ctx, "What is TDD?", "")
	if err != nil {
		t.Fatalf("GenerateAnswers() error = %v", err)
	}

	if len(answers) == 0 {
		t.Error("GenerateAnswers() returned empty slice")
	}
}

func TestOpenAIProvider_RetryOnRateLimit(t *testing.T) {
	callCount := 0
	server := setupMockServer(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error": {"message": "rate limit exceeded"}}`))
			return
		}
		resp := openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionChoice{
				{
					Message: openai.ChatCompletionMessage{
						Content: "Translated text.",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	provider := newTestOpenAIProvider(server, "gpt-4o-mini")
	ctx := context.Background()

	result, err := provider.Translate(ctx, "Hello", nil)
	if err != nil {
		t.Fatalf("Translate() error after retries = %v", err)
	}

	if result != "Translated text." {
		t.Errorf("Translate() = %q, want %q", result, "Translated text.")
	}

	if callCount != 3 {
		t.Errorf("Expected 3 calls (2 retries + success), got %d", callCount)
	}
}

func TestOpenAIProvider_RetryExhausted(t *testing.T) {
	server := setupMockServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error": {"message": "rate limit exceeded"}}`))
	})
	defer server.Close()

	provider := newTestOpenAIProvider(server, "gpt-4o-mini")
	ctx := context.Background()

	_, err := provider.Translate(ctx, "Hello", nil)
	if err == nil {
		t.Error("Translate() should return error after exhausting retries")
	}

	if !strings.Contains(err.Error(), "max retries exceeded") {
		t.Errorf("Error should mention max retries, got: %v", err)
	}
}

func TestOpenAIProvider_ContextCancellation(t *testing.T) {
	server := setupMockServer(func(w http.ResponseWriter, r *http.Request) {
		// Simulate a slow response.
		time.Sleep(2 * time.Second)
	})
	defer server.Close()

	provider := newTestOpenAIProvider(server, "gpt-4o-mini")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := provider.Translate(ctx, "Hello", nil)
	if err == nil {
		t.Error("Translate() should return error on context cancellation")
	}
}

func TestOpenAIProvider_NoChoices(t *testing.T) {
	server := setupMockServer(func(w http.ResponseWriter, r *http.Request) {
		resp := openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionChoice{},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	provider := newTestOpenAIProvider(server, "gpt-4o-mini")
	ctx := context.Background()

	_, err := provider.Translate(ctx, "Hello", nil)
	if err == nil {
		t.Error("Translate() should return error when response has no choices")
	}
}

func TestNewOpenAIProvider_DefaultModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()

	provider := newTestOpenAIProvider(server, "")
	if provider.model != "gpt-4o-mini" {
		t.Errorf("Default model = %q, want %q", provider.model, "gpt-4o-mini")
	}
}

func TestParseAnswerHints(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "dash bullets",
			input: "- First hint\n- Second hint\n- Third hint",
			want:  []string{"First hint", "Second hint", "Third hint"},
		},
		{
			name:  "numbered bullets",
			input: "1. First hint\n2. Second hint",
			want:  []string{"First hint", "Second hint"},
		},
		{
			name:  "mixed bullets",
			input: "1. First\n- Second\n* Third",
			want:  []string{"First", "Second", "Third"},
		},
		{
			name:  "with empty lines",
			input: "- First\n\n- Second\n\n- Third",
			want:  []string{"First", "Second", "Third"},
		},
		{
			name:  "truncate to 3",
			input: "- A\n- B\n- C\n- D\n- E",
			want:  []string{"A", "B", "C"},
		},
		{
			name:  "single hint",
			input: "- Just one hint",
			want:  []string{"Just one hint"},
		},
		{
			name:  "empty input",
			input: "",
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseAnswerHints(tt.input)

			if len(got) != len(tt.want) {
				t.Fatalf("parseAnswerHints() length = %d, want %d. Got: %v", len(got), len(tt.want), got)
			}

			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("parseAnswerHints()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestStripBulletPrefix(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"- hello", "hello"},
		{"* hello", "hello"},
		{"• hello", "hello"},
		{"1. hello", "hello"},
		{"1) hello", "hello"},
		{"12. hello world", "hello world"},
		{"hello", "hello"},
		{"-", ""},
		{"1.", ""},
	}

	for _, tt := range tests {
		got := stripBulletPrefix(tt.input)
		if got != tt.want {
			t.Errorf("stripBulletPrefix(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestNewOpenAIProviderWithConfig_CustomBaseURL проверяет создание провайдера
// с кастомным base_url (например, для Z.AI GLM).
func TestNewOpenAIProviderWithConfig_CustomBaseURL(t *testing.T) {
	server := setupMockServer(func(w http.ResponseWriter, r *http.Request) {
		resp := openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionChoice{
				{
					Message: openai.ChatCompletionMessage{
						Content: "Привет, мир",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	// Создаём провайдер с кастомным base_url (симулируем GLM API).
	provider := NewOpenAIProviderWithConfig(server.URL+"/v1", "sk-glm-test-key", "glm-4-flash")
	ctx := context.Background()

	if provider.model != "glm-4-flash" {
		t.Errorf("Model = %q, want %q", provider.model, "glm-4-flash")
	}

	result, err := provider.Translate(ctx, "Hello, world", nil)
	if err != nil {
		t.Fatalf("Translate() error = %v", err)
	}

	if result != "Привет, мир" {
		t.Errorf("Translate() = %q, want %q", result, "Привет, мир")
	}
}

// TestNewOpenAIProviderWithConfig_DefaultModel проверяет, что пустая модель
// даёт значение по умолчанию.
func TestNewOpenAIProviderWithConfig_DefaultModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()

	provider := NewOpenAIProviderWithConfig(server.URL+"/v1", "sk-key", "")
	if provider.model != "gpt-4o-mini" {
		t.Errorf("Default model = %q, want %q", provider.model, "gpt-4o-mini")
	}
}

// TestStreamingTranslate проверяет стриминговый перевод с mock SSE сервером.
func TestStreamingTranslate(t *testing.T) {
	// Создаём mock сервер, возвращающий SSE-поток.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("streaming not supported")
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		// Отправляем токены по одному.
		tokens := []string{"При", "вет", ", ", "м", "ир"}
		for _, token := range tokens {
			data := fmt.Sprintf(`data: {"choices":[{"delta":{"content":"%s"}}]}`, token)
			w.Write([]byte(data + "\n\n"))
			flusher.Flush()
		}
		// Финальное сообщение с finish_reason.
		w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	provider := newTestOpenAIProvider(server, "gpt-4o-mini")
	ctx := context.Background()

	tokenCh, err := provider.TranslateStream(ctx, "Hello, world", nil)
	if err != nil {
		t.Fatalf("TranslateStream() error = %v", err)
	}

	var received []string
	for token := range tokenCh {
		received = append(received, token)
	}

	// Собираем полный текст.
	fullText := strings.Join(received, "")
	if fullText != "Привет, мир" {
		t.Errorf("Full translation = %q, want %q", fullText, "Привет, мир")
	}

	// Проверяем что токены приходят инкрементально (не менее 2 токенов).
	if len(received) < 2 {
		t.Errorf("Expected at least 2 tokens, got %d: %v", len(received), received)
	}
}

// TestStreamingTranslate_EmptyResponse проверяет стриминг с пустым ответом.
func TestStreamingTranslate_EmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		// Только [DONE] без токенов.
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	provider := newTestOpenAIProvider(server, "gpt-4o-mini")
	ctx := context.Background()

	tokenCh, err := provider.TranslateStream(ctx, "Hello", nil)
	if err != nil {
		t.Fatalf("TranslateStream() error = %v", err)
	}

	var received []string
	for token := range tokenCh {
		received = append(received, token)
	}

	if len(received) != 0 {
		t.Errorf("Expected 0 tokens, got %d: %v", len(received), received)
	}
}

// TestStreamingTranslate_ContextCancellation проверяет отмену контекста
// во время стриминга.
func TestStreamingTranslate_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// Отправляем один токен и зависаем — имитируем медленный стрим.
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"A\"}}]}\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Ждём пока клиент не отменит контекст (закроет соединение).
		<-r.Context().Done()
	}))
	defer server.Close()

	provider := newTestOpenAIProvider(server, "gpt-4o-mini")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	tokenCh, err := provider.TranslateStream(ctx, "Hello", nil)
	if err != nil {
		// Это приемлемо — ошибка при открытии потока.
		return
	}

	// Читаем один токен, потом контекст отменяется.
	<-tokenCh
	// Канал должен закрыться при отмене контекста.
	// Ждём закрытия или таймаута.
	for range tokenCh {
		// drain
	}
}

// TestOpenAIProvider_ImplementsStreaming проверяет, что OpenAIProvider
// реализует интерфейс StreamingTranslator.
func TestOpenAIProvider_ImplementsStreaming(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()

	provider := newTestOpenAIProvider(server, "gpt-4o-mini")

	// Compile-time check: provider satisfies StreamingTranslator.
	var _ StreamingTranslator = provider
}
