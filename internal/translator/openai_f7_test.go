package translator

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// ── F7: JSON-парсинг ответа LLM ──────────────────────────────────────────────

func TestParseAnswerJSON(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		srcLang string
		tgtLang string
		want    []string
	}{
		{
			name: "валидный JSON с target",
			raw:  `{"answers":[{"source":"Redis is an in-memory cache","target":"Редис — это кэш"}]}`,
			want: []string{"EN: Redis is an in-memory cache | RU: Редис — это кэш"},
		},
		{
			name: "markdown-ограждения срезаются",
			raw:  "```json\n{\"answers\":[{\"source\":\"Answer\",\"target\":\"Ответ\"}]}\n```",
			want: []string{"EN: Answer | RU: Ответ"},
		},
		{
			name: "пустой target — source целиком",
			raw:  `{"answers":[{"source":"Only source"}]}`,
			want: []string{"Only source"},
		},
		{
			name: "элемент без source пропускается",
			raw:  `{"answers":[{"target":"только перевод"},{"source":"Ok","target":"Ок"}]}`,
			want: []string{"EN: Ok | RU: Ок"},
		},
		{
			name: "более 3 вариантов — cap",
			raw:  `{"answers":[{"source":"1"},{"source":"2"},{"source":"3"},{"source":"4"},{"source":"5"}]}`,
			want: []string{"1", "2", "3"},
		},
		{
			name: "не-JSON — nil (fallback на строковый парсер)",
			raw:  "- EN: A | RU: Б",
			want: nil,
		},
		{
			name: "битый JSON — nil",
			raw:  `{"answers":[{"source":"A"}`,
			want: nil,
		},
		{
			name: "пустой массив",
			raw:  `{"answers":[]}`,
			want: nil,
		},
		{
			name:    "другая языковая пара",
			raw:     `{"answers":[{"source":"A cache","target":"Ein Cache"}]}`,
			srcLang: "en",
			tgtLang: "de",
			want:    []string{"EN: A cache | DE: Ein Cache"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src, tgt := tt.srcLang, tt.tgtLang
			if src == "" {
				src = "en"
			}
			if tgt == "" {
				tgt = "ru"
			}
			got := parseAnswerJSON(tt.raw, src, tgt)
			if len(got) != len(tt.want) {
				t.Fatalf("parseAnswerJSON() = %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("parseAnswerJSON()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestParseAnswersJSONFirst — JSON-ответ разбирается по схеме, строковый
// формат остаётся fallback'ом.
func TestParseAnswersJSONFirst(t *testing.T) {
	jsonRaw := `{"answers":[{"source":"A","target":"Б"}]}`
	got := ParseAnswers(jsonRaw, "en", "ru")
	if len(got) != 1 || got[0] != "EN: A | RU: Б" {
		t.Errorf("ParseAnswers(JSON) = %v, want [EN: A | RU: Б]", got)
	}

	stringRaw := "- EN: A | RU: Б"
	got = ParseAnswers(stringRaw, "en", "ru")
	if len(got) != 1 || got[0] != "EN: A | RU: Б" {
		t.Errorf("ParseAnswers(string) = %v, want [EN: A | RU: Б]", got)
	}
}

// ── F7: частичный показ стрима ───────────────────────────────────────────────

func TestPartialAnswerDisplay(t *testing.T) {
	tests := []struct {
		name string
		buf  string
		want string
	}{
		{
			name: "JSON — читается source до закрытия кавычки",
			buf:  `{"answers":[{"source":"Redis is a fast","target":"Ре`,
			want: "Redis is a fast",
		},
		{
			name: "JSON с закрытым source",
			buf:  `{"answers":[{"source":"Done","target":"Готово"}]}`,
			want: "Done",
		},
		{
			name: "JSON скелет без source — пусто",
			buf:  `{"answers":[{"`,
			want: "",
		},
		{
			name: "не-JSON — сырой буфер",
			buf:  "- EN: partial hint | RU: част",
			want: "- EN: partial hint | RU: част",
		},
		{
			name: "не-JSON с think-блоком — блок вырезан",
			buf:  "<think>рассуждения</think>- EN: hint",
			want: "- EN: hint",
		},
		{
			name: "пустой буфер",
			buf:  "",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PartialAnswerDisplay(tt.buf); got != tt.want {
				t.Errorf("PartialAnswerDisplay(%q) = %q, want %q", tt.buf, got, tt.want)
			}
		})
	}
}

func TestExtractJSONSourcePartial(t *testing.T) {
	tests := []struct {
		name string
		buf  string
		want string
	}{
		{
			name: "значение без закрывающей кавычки",
			buf:  `{"answers":[{"source":"Hel`,
			want: "Hel",
		},
		{
			name: "закрытый escape-последовательностью",
			buf:  `{"answers":[{"source":"a \"b\" c"}]}`,
			want: `a "b" c`,
		},
		{
			name: "обрыв посреди escape",
			buf:  `{"answers":[{"source":"a\`,
			want: "a",
		},
		{
			name: "обрыв посреди unicode-escape",
			buf:  `{"answers":[{"source":"прив\u0`,
			want: "прив",
		},
		{
			name: "полный unicode-escape",
			buf:  `{"answers":[{"source":"\u0041BC"}]}`,
			want: "ABC",
		},
		{
			name: "ключ source ещё не начался",
			buf:  `{"answers":[{"tar`,
			want: "",
		},
		{
			name: "нет ключа source вовсе",
			buf:  `not json`,
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractJSONSourcePartial(tt.buf); got != tt.want {
				t.Errorf("extractJSONSourcePartial(%q) = %q, want %q", tt.buf, got, tt.want)
			}
		})
	}
}

// ── F7: fallback-цепочка моделей ─────────────────────────────────────────────

// TestChatProvider_FallbackCrossProvider — резерв на ДРУГОМ провайдере
// (F10.1): основной endpoint отдаёт 500 → запрос уходит на
// SetFallbackEndpoints (свой клиент/baseURL) и успешен.
func TestChatProvider_FallbackCrossProvider(t *testing.T) {
	mainCalls, reserveCalls := 0, 0
	mainServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mainCalls++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error": {"message": "groq down"}}`))
	}))
	defer mainServer.Close()

	reserveServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reserveCalls++
		resp := openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionChoice{
				{Message: openai.ChatCompletionMessage{Content: `{"answers":[{"source":"reserve ok","target":"резерв ок"}]}`}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer reserveServer.Close()

	provider := NewChatProvider(mainServer.URL+"/v1", "groq-key", "main-model")
	provider.SetSystemPromptTemplate(testSystemPromptTemplate())
	provider.SetFallbackEndpoints([]FallbackEndpoint{{
		BaseURL: reserveServer.URL + "/v1",
		APIKey:  "openrouter-key",
		Model:   "reserve-model",
	}})

	answers, err := provider.GenerateAnswers(context.Background(), AnswerRequest{Question: "Q?"})
	if err != nil {
		t.Fatalf("GenerateAnswers() error = %v", err)
	}
	if len(answers) != 1 || !strings.Contains(answers[0], "reserve ok") {
		t.Errorf("GenerateAnswers() = %v, want ответ резервного провайдера", answers)
	}
	if mainCalls != 1 || reserveCalls != 1 {
		t.Errorf("вызовы: main=%d reserve=%d, want 1/1", mainCalls, reserveCalls)
	}
}

// TestChatProvider_FallbackChain_RateLimit — основная модель всегда 429 →
// после исчерпания ретраев запрос уходит на fallback-модель и успешен.
func TestChatProvider_FallbackChain_RateLimit(t *testing.T) {
	modelCalls := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		modelCalls[body.Model]++

		if body.Model == "main-model" {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error": {"message": "rate limit exceeded"}}`))
			return
		}
		resp := openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionChoice{
				{Message: openai.ChatCompletionMessage{Content: `{"answers":[{"source":"fallback ok","target":"фоллбэк ок"}]}`}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider := newTestChatProvider(server, "main-model")
	provider.SetFallbackModels([]string{"fallback-model"})

	answers, err := provider.GenerateAnswers(context.Background(), AnswerRequest{Question: "Q?"})
	if err != nil {
		t.Fatalf("GenerateAnswers() error = %v", err)
	}
	if len(answers) != 1 || !strings.Contains(answers[0], "fallback ok") {
		t.Errorf("GenerateAnswers() = %v, want ответ fallback-модели", answers)
	}
	if modelCalls["main-model"] != maxRetries {
		t.Errorf("main-model вызывалась %d раз, want %d", modelCalls["main-model"], maxRetries)
	}
	if modelCalls["fallback-model"] != 1 {
		t.Errorf("fallback-model вызывалась %d раз, want 1", modelCalls["fallback-model"])
	}
}

// TestChatProvider_FallbackChain_NonRateLimitError — неустранимая ошибка
// (например 500) переводит на следующую модель без ретраев — быстро.
func TestChatProvider_FallbackChain_NonRateLimitError(t *testing.T) {
	modelCalls := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		modelCalls[body.Model]++

		if body.Model == "main-model" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error": {"message": "boom"}}`))
			return
		}
		resp := openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionChoice{
				{Message: openai.ChatCompletionMessage{Content: `{"answers":[{"source":"ok"}]}`}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider := newTestChatProvider(server, "main-model")
	provider.SetFallbackModels([]string{"fallback-model"})

	start := time.Now()
	answers, err := provider.GenerateAnswers(context.Background(), AnswerRequest{Question: "Q?"})
	if err != nil {
		t.Fatalf("GenerateAnswers() error = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("неустранимая ошибка должна переключать модель без backoff, заняло %v", elapsed)
	}
	if len(answers) != 1 || !strings.Contains(answers[0], "ok") {
		t.Errorf("GenerateAnswers() = %v, want ответ fallback-модели", answers)
	}
	if modelCalls["main-model"] != 1 || modelCalls["fallback-model"] != 1 {
		t.Errorf("вызовы = %v, want main:1 fallback:1", modelCalls)
	}
}

// TestChatProvider_FallbackNotUsedOnCancel — отмена контекста не гонит
// fallback-модели.
func TestChatProvider_FallbackNotUsedOnCancel(t *testing.T) {
	modelCalls := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		modelCalls[body.Model]++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error": {"message": "boom"}}`))
	}))
	defer server.Close()

	provider := newTestChatProvider(server, "main-model")
	provider.SetFallbackModels([]string{"fallback-model"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := provider.GenerateAnswers(ctx, AnswerRequest{Question: "Q?"}); err == nil {
		t.Fatal("ожидалась ошибка при отменённом контексте")
	}
	if modelCalls["fallback-model"] != 0 {
		t.Errorf("fallback-модель не должна вызываться при отмене, вызывалась %d раз", modelCalls["fallback-model"])
	}
}

// ── F7: Retry-After через transport ─────────────────────────────────────────

// TestRetryAfterTransport — 429 с заголовком Retry-After запоминается
// провайдером и доступен через pendingRetryAfter.
func TestRetryAfterTransport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	provider := NewChatProvider(server.URL, "key", "model")
	// Прямой запрос через client (транспорт срабатывает на 429).
	_, _ = provider.client.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model: "m", Messages: []openai.ChatCompletionMessage{{Role: "user", Content: "x"}},
	})

	if got := provider.pendingRetryAfter(); got <= 0 {
		t.Errorf("pendingRetryAfter() = %v, want > 0 (Retry-After: 120)", got)
	}
}

// TestRetryAfterTransportNoHeader — 429 без заголовка не двигает pendingRetryAfter.
func TestRetryAfterTransportNoHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	provider := NewChatProvider(server.URL, "key", "model")
	_, _ = provider.client.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model: "m", Messages: []openai.ChatCompletionMessage{{Role: "user", Content: "x"}},
	})

	if got := provider.pendingRetryAfter(); got != 0 {
		t.Errorf("pendingRetryAfter() = %v, want 0 (заголовка нет)", got)
	}
}

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  time.Duration
	}{
		{"пустой", "", 0},
		{"секунды", "30", 30 * time.Second},
		{"ноль", "0", 0},
		{"отрицательный", "-5", 0},
		{"мусор", "soon", 0},
		{"HTTP-date в будущем", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat), time.Hour},
		{"HTTP-date в прошлом", time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseRetryAfter(tt.input)
			if tt.want == time.Hour {
				if got <= 0 || got > time.Hour {
					t.Errorf("parseRetryAfter(%q) = %v, want (0; %v]", tt.input, got, tt.want)
				}
				return
			}
			if got != tt.want {
				t.Errorf("parseRetryAfter(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// ── F7: reasoning_effort и JSON-режим в запросе ─────────────────────────────

// TestChatProvider_ReasoningEffortInRequest — SetReasoningEffort доходит до
// тела запроса; пустое значение — параметр не отправляется.
func TestChatProvider_ReasoningEffortInRequest(t *testing.T) {
	var gotEffort string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ReasoningEffort string `json:"reasoning_effort"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		gotEffort = body.ReasoningEffort

		resp := openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionChoice{
				{Message: openai.ChatCompletionMessage{Content: `{"answers":[{"source":"ok"}]}`}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider := newTestChatProvider(server, "main-model")
	provider.SetReasoningEffort("LOW") // нормализуется в нижний регистр

	if _, err := provider.GenerateAnswers(context.Background(), AnswerRequest{Question: "Q?"}); err != nil {
		t.Fatalf("GenerateAnswers() error = %v", err)
	}
	if gotEffort != "low" {
		t.Errorf("reasoning_effort = %q, want %q", gotEffort, "low")
	}

	// Пустое значение — параметр не отправляется.
	provider2 := newTestChatProvider(server, "main-model")
	if _, err := provider2.GenerateAnswers(context.Background(), AnswerRequest{Question: "Q?"}); err != nil {
		t.Fatalf("GenerateAnswers() error = %v", err)
	}
	if gotEffort != "" {
		t.Errorf("reasoning_effort = %q, want пусто (параметр не отправлен)", gotEffort)
	}
}

// TestChatProvider_JSONModeResponseFormat — jsonMode=response_format кладёт
// response_format в тело запроса; jsonMode=prompt — нет.
func TestChatProvider_JSONModeResponseFormat(t *testing.T) {
	var gotFormat string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ResponseFormat *struct {
				Type string `json:"type"`
			} `json:"response_format"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		if body.ResponseFormat != nil {
			gotFormat = body.ResponseFormat.Type
		}

		resp := openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionChoice{
				{Message: openai.ChatCompletionMessage{Content: `{"answers":[{"source":"ok"}]}`}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider := newTestChatProvider(server, "main-model")
	provider.SetJSONMode(JSONModeResponseFormat)
	if _, err := provider.GenerateAnswers(context.Background(), AnswerRequest{Question: "Q?"}); err != nil {
		t.Fatalf("GenerateAnswers() error = %v", err)
	}
	if gotFormat != string(openai.ChatCompletionResponseFormatTypeJSONObject) {
		t.Errorf("response_format.type = %q, want json_object", gotFormat)
	}

	// Режим prompt — response_format не отправляется.
	gotFormat = ""
	provider2 := newTestChatProvider(server, "main-model")
	provider2.SetJSONMode(JSONModePrompt)
	if _, err := provider2.GenerateAnswers(context.Background(), AnswerRequest{Question: "Q?"}); err != nil {
		t.Fatalf("GenerateAnswers() error = %v", err)
	}
	if gotFormat != "" {
		t.Errorf("response_format.type = %q, want пусто в режиме prompt", gotFormat)
	}
}

// ── F7: валидация конфигурации ───────────────────────────────────────────────

func TestValidateJSONMode(t *testing.T) {
	for _, ok := range []string{"", JSONModePrompt, JSONModeResponseFormat, JSONModeOff} {
		if err := ValidateJSONMode(ok); err != nil {
			t.Errorf("ValidateJSONMode(%q) = %v, want nil", ok, err)
		}
	}
	if err := ValidateJSONMode("strict"); err == nil {
		t.Error("ValidateJSONMode(\"strict\") = nil, want ошибка")
	}
}

func TestValidateReasoningEffort(t *testing.T) {
	for _, ok := range []string{"", "low", "medium", "high", "HIGH"} {
		if err := ValidateReasoningEffort(ok); err != nil {
			t.Errorf("ValidateReasoningEffort(%q) = %v, want nil", ok, err)
		}
	}
	if err := ValidateReasoningEffort("ultra"); err == nil {
		t.Error("ValidateReasoningEffort(\"ultra\") = nil, want ошибка")
	}
}
