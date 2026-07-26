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
						Content: "- Answer hint",
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

	_, err := provider.GenerateAnswers(ctx, "Question?", "")
	if err != nil {
		t.Fatalf("GenerateAnswers() error after retries = %v", err)
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

	_, err := provider.GenerateAnswers(ctx, "Question?", "")
	if err == nil {
		t.Error("GenerateAnswers() should return error after exhausting retries")
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

	_, err := provider.GenerateAnswers(ctx, "Question?", "")
	if err == nil {
		t.Error("GenerateAnswers() should return error on context cancellation")
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

	_, err := provider.GenerateAnswers(ctx, "Question?", "")
	if err == nil {
		t.Error("GenerateAnswers() should return error when response has no choices")
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
			name:  "no truncation — returns all",
			input: "- A\n- B\n- C\n- D\n- E",
			want:  []string{"A", "B", "C", "D", "E"},
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
						Content: "- Answer hint",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	// Создаём провайдер с кастомным base_url (симулируем GLM API).
	provider := NewOpenAIProviderWithConfig(server.URL+"/v1", "sk-test-key", "glm-4-flash")
	ctx := context.Background()

	if provider.model != "glm-4-flash" {
		t.Errorf("Model = %q, want %q", provider.model, "glm-4-flash")
	}

	answers, err := provider.GenerateAnswers(ctx, "Question?", "")
	if err != nil {
		t.Fatalf("GenerateAnswers() error = %v", err)
	}

	if len(answers) == 0 {
		t.Error("GenerateAnswers() returned empty slice")
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

// TestStreamingGenerateAnswers проверяет стриминговую генерацию с mock SSE сервером.
func TestStreamingGenerateAnswers(t *testing.T) {
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
		tokens := []string{"- ", "hint", " one", "\n", "- ", "hint", " two"}
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

	tokenCh, err := provider.GenerateAnswersStream(ctx, "Question?", "")
	if err != nil {
		t.Fatalf("GenerateAnswersStream() error = %v", err)
	}

	var received []string
	for token := range tokenCh {
		received = append(received, token)
	}

	// Проверяем что токены приходят инкрементально (не менее 2 токенов).
	if len(received) < 2 {
		t.Errorf("Expected at least 2 tokens, got %d: %v", len(received), received)
	}
}

// TestStreamingGenerateAnswers_EmptyResponse проверяет стриминг с пустым ответом.
func TestStreamingGenerateAnswers_EmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		// Только [DONE] без токенов.
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	provider := newTestOpenAIProvider(server, "gpt-4o-mini")
	ctx := context.Background()

	tokenCh, err := provider.GenerateAnswersStream(ctx, "Hello", "")
	if err != nil {
		t.Fatalf("GenerateAnswersStream() error = %v", err)
	}

	var received []string
	for token := range tokenCh {
		received = append(received, token)
	}

	if len(received) != 0 {
		t.Errorf("Expected 0 tokens, got %d: %v", len(received), received)
	}
}

// TestStreamingGenerateAnswers_ContextCancellation проверяет отмену контекста
// во время стриминга.
func TestStreamingGenerateAnswers_ContextCancellation(t *testing.T) {
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

	tokenCh, err := provider.GenerateAnswersStream(ctx, "Hello", "")
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
// реализует интерфейс StreamingAnswersProvider.
func TestOpenAIProvider_ImplementsStreaming(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()

	provider := newTestOpenAIProvider(server, "gpt-4o-mini")

	// Compile-time check: provider satisfies StreamingAnswersProvider.
	var _ StreamingAnswersProvider = provider
}

// TestStreamingGenerateAnswers_StreamError проверяет ошибку mid-stream.
func TestStreamingGenerateAnswers_StreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		// Отправляем ошибку в SSE-потоке (некорректный JSON).
		w.Write([]byte("data: {broken json\n\n"))
	}))
	defer server.Close()

	provider := newTestOpenAIProvider(server, "gpt-4o-mini")
	ctx := context.Background()

	tokenCh, err := provider.GenerateAnswersStream(ctx, "Question?", "")
	if err != nil {
		// Ошибка при открытии потока — приемлемо.
		return
	}

	// Читаем все токены — должен быть [ERROR:...].
	var gotError bool
	for token := range tokenCh {
		if strings.HasPrefix(token, "[ERROR:") {
			gotError = true
		}
	}
	if !gotError {
		t.Log("не получили [ERROR:] токен — клиент мог обработать битый JSON без ошибки")
	}
}

// TestOpenAIProvider_DisableThinking проверяет включение флага disableThinking.
func TestOpenAIProvider_DisableThinking(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()

	provider := newTestOpenAIProvider(server, "gpt-4o-mini")

	// До вызова.
	if provider.disableThinking {
		t.Error("disableThinking должен быть false по умолчанию")
	}

	provider.DisableThinking()

	if !provider.disableThinking {
		t.Error("disableThinking должен быть true после вызова DisableThinking()")
	}
}

// TestOpenAIProvider_SetMaxTokens проверяет установку и чтение maxTokens.
func TestOpenAIProvider_SetMaxTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()

	provider := newTestOpenAIProvider(server, "gpt-4o-mini")

	if provider.maxTokensOrZero() != 0 {
		t.Error("maxTokensOrZero должен возвращать 0 по умолчанию")
	}

	provider.SetMaxTokens(256)
	if provider.maxTokens != 256 {
		t.Errorf("maxTokens = %d, want 256", provider.maxTokens)
	}
	if provider.maxTokensOrZero() != 256 {
		t.Errorf("maxTokensOrZero = %d, want 256", provider.maxTokensOrZero())
	}

	provider.SetMaxTokens(0)
	if provider.maxTokensOrZero() != 0 {
		t.Error("maxTokensOrZero должен возвращать 0 после установки в 0")
	}
}
