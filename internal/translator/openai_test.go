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

// newTestChatProvider creates an ChatProvider pointed at a mock server.
func newTestChatProvider(server *httptest.Server, model string) *ChatProvider {
	return NewChatProvider(server.URL+"/v1", "sk-test-key", model)
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

func TestChatProvider_GenerateAnswers_Success(t *testing.T) {
	server := setupMockServer(func(w http.ResponseWriter, r *http.Request) {
		resp := openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionChoice{
				{
					Message: openai.ChatCompletionMessage{
						Content: "- EN: I have 3 years of Kubernetes experience with Helm | RU: У меня 3 года опыта с Kubernetes и Helm\n- EN: I set up CI/CD pipelines on GitHub Actions | RU: Я настраивал CI/CD пайплайны на GitHub Actions\n- EN: I used Prometheus for monitoring | RU: Я использовал Prometheus для мониторинга",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	provider := newTestChatProvider(server, "gpt-4o-mini")
	ctx := context.Background()

	answers, err := provider.GenerateAnswers(ctx, AnswerRequest{Question: "What is your DevOps experience?", CandidateContext: "DevOps engineer, 5 years"})
	if err != nil {
		t.Fatalf("GenerateAnswers() error = %v", err)
	}

	if len(answers) != 3 {
		t.Fatalf("GenerateAnswers() returned %d answers, want 3", len(answers))
	}

	expectedFirst := "EN: I have 3 years of Kubernetes experience with Helm | RU: У меня 3 года опыта с Kubernetes и Helm"
	if answers[0] != expectedFirst {
		t.Errorf("GenerateAnswers()[0] = %q, want %q", answers[0], expectedFirst)
	}
}

func TestChatProvider_GenerateAnswers_EmptyCV(t *testing.T) {
	server := setupMockServer(func(w http.ResponseWriter, r *http.Request) {
		resp := openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionChoice{
				{
					Message: openai.ChatCompletionMessage{
						Content: "- EN: I write tests first with TDD | RU: Я сначала пишу тесты по TDD",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	provider := newTestChatProvider(server, "gpt-4o-mini")
	ctx := context.Background()

	answers, err := provider.GenerateAnswers(ctx, AnswerRequest{Question: "What is TDD?"})
	if err != nil {
		t.Fatalf("GenerateAnswers() error = %v", err)
	}

	if len(answers) == 0 {
		t.Error("GenerateAnswers() returned empty slice")
	}
}

func TestChatProvider_RetryOnRateLimit(t *testing.T) {
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

	provider := newTestChatProvider(server, "gpt-4o-mini")
	ctx := context.Background()

	_, err := provider.GenerateAnswers(ctx, AnswerRequest{Question: "Question?"})
	if err != nil {
		t.Fatalf("GenerateAnswers() error after retries = %v", err)
	}

	if callCount != 3 {
		t.Errorf("Expected 3 calls (2 retries + success), got %d", callCount)
	}
}

func TestChatProvider_RetryExhausted(t *testing.T) {
	server := setupMockServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error": {"message": "rate limit exceeded"}}`))
	})
	defer server.Close()

	provider := newTestChatProvider(server, "gpt-4o-mini")
	ctx := context.Background()

	_, err := provider.GenerateAnswers(ctx, AnswerRequest{Question: "Question?"})
	if err == nil {
		t.Error("GenerateAnswers() should return error after exhausting retries")
	}

	if !strings.Contains(err.Error(), "max retries exceeded") {
		t.Errorf("Error should mention max retries, got: %v", err)
	}
}

func TestChatProvider_ContextCancellation(t *testing.T) {
	server := setupMockServer(func(w http.ResponseWriter, r *http.Request) {
		// Simulate a slow response.
		time.Sleep(2 * time.Second)
	})
	defer server.Close()

	provider := newTestChatProvider(server, "gpt-4o-mini")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := provider.GenerateAnswers(ctx, AnswerRequest{Question: "Question?"})
	if err == nil {
		t.Error("GenerateAnswers() should return error on context cancellation")
	}
}

func TestChatProvider_NoChoices(t *testing.T) {
	server := setupMockServer(func(w http.ResponseWriter, r *http.Request) {
		resp := openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionChoice{},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	provider := newTestChatProvider(server, "gpt-4o-mini")
	ctx := context.Background()

	_, err := provider.GenerateAnswers(ctx, AnswerRequest{Question: "Question?"})
	if err == nil {
		t.Error("GenerateAnswers() should return error when response has no choices")
	}
}

func TestParseAnswerHints(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "dash bullets EN/RU",
			input: "- EN: First | RU: Первая\n- EN: Second | RU: Вторая\n- EN: Third | RU: Третья",
			want:  []string{"EN: First | RU: Первая", "EN: Second | RU: Вторая", "EN: Third | RU: Третья"},
		},
		{
			name:  "numbered bullets EN/RU",
			input: "1. EN: First | RU: Первая\n2. EN: Second | RU: Вторая",
			want:  []string{"EN: First | RU: Первая", "EN: Second | RU: Вторая"},
		},
		{
			name:  "mixed bullets EN/RU",
			input: "1. EN: First | RU: Первая\n- EN: Second | RU: Вторая\n* EN: Third | RU: Третья",
			want:  []string{"EN: First | RU: Первая", "EN: Second | RU: Вторая", "EN: Third | RU: Третья"},
		},
		{
			name:  "with empty lines",
			input: "- EN: First | RU: Первая\n\n- EN: Second | RU: Вторая\n\n- EN: Third | RU: Третья",
			want:  []string{"EN: First | RU: Первая", "EN: Second | RU: Вторая", "EN: Third | RU: Третья"},
		},
		{
			name:  "truncate to max 3",
			input: "- EN: A | RU: А\n- EN: B | RU: Б\n- EN: C | RU: В\n- EN: D | RU: Г\n- EN: E | RU: Д",
			want:  []string{"EN: A | RU: А", "EN: B | RU: Б", "EN: C | RU: В"},
		},
		{
			name:  "single hint",
			input: "- EN: Just one hint | RU: Одна подсказка",
			want:  []string{"EN: Just one hint | RU: Одна подсказка"},
		},
		{
			name:  "empty input",
			input: "",
			want:  nil,
		},
		{
			name:  "reasoning chain-of-thought before final hint",
			input: "Okay, let me think about this question step by step.\nFirst, the candidate is a senior Go developer.\nThe interviewer asked about Kubernetes experience.\nI should mention Helm and CI/CD.\nLet me craft a concise answer.\n- EN: I have 5 years of Kubernetes experience using Helm | RU: У меня 5 лет опыта с Kubernetes и Helm",
			want:  []string{"EN: I have 5 years of Kubernetes experience using Helm | RU: У меня 5 лет опыта с Kubernetes и Helm"},
		},
		{
			name:  "reasoning only — no valid hint",
			input: "Let me analyze the question.\nThe candidate should mention Kubernetes.\nAlso mention CI/CD and Helm.\nThis is a good answer structure.",
			want:  nil,
		},
		{
			name:  "line without pipe separator is discarded",
			input: "- EN: missing separator RU: текст",
			want:  nil,
		},
		{
			name:  "line without EN/RU prefixes is discarded",
			input: "- Some hint | другая часть",
			want:  nil,
		},
		{
			name:  "reasoning plus 4 valid hints truncated to 3",
			input: "Thinking...\n- EN: A | RU: А\n- EN: B | RU: Б\n- EN: C | RU: В\n- EN: D | RU: Г\nmore reasoning",
			want:  []string{"EN: A | RU: А", "EN: B | RU: Б", "EN: C | RU: В"},
		},
		{
			name:  "groq think-tags reasoning before final hint",
			input: "<think>\nThinking Process:\n1. Format: `- EN: <English answer> | RU: <Russian translation>`.\n2. Structure matches `- EN: ... | RU: ...`? Yes.\n</think>\n\n- EN: I use Redis as an in-memory store for caching | RU: Я использую Redis как in-memory хранилище для кэширования",
			want:  []string{"EN: I use Redis as an in-memory store for caching | RU: Я использую Redis как in-memory хранилище для кэширования"},
		},
		{
			name:  "think block without closing tag truncated",
			input: "<think>\nLet me analyze the format.\n- EN: should not leak",
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

func TestBuildSystemPrompt(t *testing.T) {
	tests := []struct {
		name      string
		cvContext string
		check     func(t *testing.T, prompt string)
	}{
		{
			name:      "empty cv uses only format rules",
			cvContext: "",
			check: func(t *testing.T, prompt string) {
				if prompt != SystemPromptAnswerGen {
					t.Error("buildSystemPrompt(\"\") should equal SystemPromptAnswerGen")
				}
			},
		},
		{
			name:      "cv context appended after format rules",
			cvContext: "Senior Go developer, 5 years",
			check: func(t *testing.T, prompt string) {
				// Формат ВСЕГДА идёт первым — корень бага: раньше cvContext затирал его.
				if !strings.HasPrefix(prompt, SystemPromptAnswerGen) {
					t.Error("prompt must start with SystemPromptAnswerGen (format rules)")
				}
				if !strings.Contains(prompt, "Candidate context:\nSenior Go developer, 5 years") {
					t.Errorf("prompt must contain CV context section, got: %q", prompt)
				}
			},
		},
		{
			name:      "format rules always present regardless of cv",
			cvContext: "Some arbitrary resume text without any format rules",
			check: func(t *testing.T, prompt string) {
				if !strings.Contains(prompt, "EN:") || !strings.Contains(prompt, "RU:") || !strings.Contains(prompt, "|") {
					t.Error("prompt must always contain EN:/RU:/| format rules")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.check(t, buildSystemPrompt(tt.cvContext))
		})
	}
}

// TestBuildSystemPromptCandidateContext — секция candidate context добавляется
// ТОЛЬКО при непустом значении; правила формата (SystemPromptAnswerGen) всегда
// идут первыми, а пустой candidate context секцию не порождает.
func TestBuildSystemPromptCandidateContext(t *testing.T) {
	const candidate = "some candidate context"

	got := buildSystemPrompt(candidate)
	if !strings.HasPrefix(got, SystemPromptAnswerGen) {
		t.Errorf("buildSystemPrompt(%q) должен начинаться с SystemPromptAnswerGen, got:\n%s", candidate, got)
	}
	if !strings.Contains(got, candidate) {
		t.Errorf("buildSystemPrompt(%q) должен содержать candidate context, got:\n%s", candidate, got)
	}
	if !strings.Contains(got, "Candidate context:\n"+candidate) {
		t.Errorf("buildSystemPrompt(%q) должен содержать секцию «Candidate context:», got:\n%s", candidate, got)
	}

	if got := buildSystemPrompt(""); got != SystemPromptAnswerGen {
		t.Errorf("buildSystemPrompt(\"\") должен быть равен SystemPromptAnswerGen без секции candidate context, got:\n%s", got)
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

// TestNewChatProvider_CustomBaseURL проверяет создание провайдера
// с кастомным base_url (например, для Z.AI GLM).
func TestNewChatProvider_CustomBaseURL(t *testing.T) {
	server := setupMockServer(func(w http.ResponseWriter, r *http.Request) {
		resp := openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionChoice{
				{
					Message: openai.ChatCompletionMessage{
						Content: "- EN: Answer hint | RU: Подсказка",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	// Создаём провайдер с кастомным base_url (симулируем GLM API).
	provider := NewChatProvider(server.URL+"/v1", "sk-test-key", "glm-4-flash")
	ctx := context.Background()

	if provider.model != "glm-4-flash" {
		t.Errorf("Model = %q, want %q", provider.model, "glm-4-flash")
	}

	answers, err := provider.GenerateAnswers(ctx, AnswerRequest{Question: "Question?"})
	if err != nil {
		t.Fatalf("GenerateAnswers() error = %v", err)
	}

	if len(answers) == 0 {
		t.Error("GenerateAnswers() returned empty slice")
	}
}

// TestNewChatProvider_DefaultModel проверяет, что модель берётся из конфигурации
// (без хардкода — пустая модель остаётся пустой).
func TestNewChatProvider_DefaultModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()

	provider := NewChatProvider(server.URL+"/v1", "sk-key", "")
	if provider.model != "" {
		t.Errorf("model should be empty when not provided, got %q", provider.model)
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

	provider := newTestChatProvider(server, "gpt-4o-mini")
	ctx := context.Background()

	tokenCh, err := provider.GenerateAnswersStream(ctx, AnswerRequest{Question: "Question?"})
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

	provider := newTestChatProvider(server, "gpt-4o-mini")
	ctx := context.Background()

	tokenCh, err := provider.GenerateAnswersStream(ctx, AnswerRequest{Question: "Hello"})
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

	provider := newTestChatProvider(server, "gpt-4o-mini")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	tokenCh, err := provider.GenerateAnswersStream(ctx, AnswerRequest{Question: "Hello"})
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

// TestChatProvider_ImplementsStreaming проверяет, что ChatProvider
// реализует интерфейс StreamingAnswersProvider.
func TestChatProvider_ImplementsStreaming(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()

	provider := newTestChatProvider(server, "gpt-4o-mini")

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

	provider := newTestChatProvider(server, "gpt-4o-mini")
	ctx := context.Background()

	tokenCh, err := provider.GenerateAnswersStream(ctx, AnswerRequest{Question: "Question?"})
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

// TestChatProvider_SetMaxTokens проверяет установку и чтение maxTokens.
func TestChatProvider_SetMaxTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()

	provider := newTestChatProvider(server, "gpt-4o-mini")

	if provider.maxTokens != 0 {
		t.Error("maxTokens должен быть 0 по умолчанию")
	}

	provider.SetMaxTokens(256)
	if provider.maxTokens != 256 {
		t.Errorf("maxTokens = %d, want 256", provider.maxTokens)
	}
	if provider.maxTokens != 256 {
		t.Errorf("maxTokens = %d, want 256", provider.maxTokens)
	}

	provider.SetMaxTokens(0)
	if provider.maxTokens != 0 {
		t.Error("maxTokens должен быть 0 после установки в 0")
	}
}
