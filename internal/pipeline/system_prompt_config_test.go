package pipeline

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mastererik/translator/internal/translator"
)

// testSystemPromptFile — фикстура шаблона system-промпта для тестов pipeline:
// пишет свой файл в TempDir, чтобы тесты не зависели от candidate_context/
// (каталог данных, в git его нет).
func testSystemPromptFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "system_prompt.md")
	if err := os.WriteFile(path, []byte("Pipeline rules: {{SRC_TAG}} -> {{TGT_TAG}}.\n"), 0o600); err != nil {
		t.Fatalf("запись фикстуры шаблона: %v", err)
	}
	return path
}

// TestPipelineNew_UncreatableSystemPromptFile — если файл шаблона нельзя ни
// прочитать, ни создать, старт падает (fail-fast) с внятной ошибкой.
func TestPipelineNew_UncreatableSystemPromptFile(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("not a dir"), 0o600); err != nil {
		t.Fatalf("фикстура: %v", err)
	}

	_, err := New(Config{
		LLMAPIKey:        "test-key",
		LLMBaseURL:       "https://example.invalid/v1",
		LLMModel:         "test-model",
		SystemPromptFile: filepath.Join(blocker, "system_prompt.md"),
	})
	if err == nil {
		t.Fatal("ожидалась ошибка старта: файл шаблона невозможно создать")
	}
	if !strings.Contains(err.Error(), "system prompt") {
		t.Errorf("ошибка должна называть system prompt, got: %v", err)
	}
}

// TestPipelineNew_CreatesMissingSystemPromptFile — файла шаблона нет? New создаёт
// его из встроенного дефолта (вместе с каталогом) и продолжает работу: первый
// запуск после клонирования не требует ручных шагов. В LLM идут правила дефолта.
func TestPipelineNew_CreatesMissingSystemPromptFile(t *testing.T) {
	systemCh := make(chan string, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil && len(body.Messages) > 0 {
			systemCh <- body.Messages[0].Content
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"- EN: ok | RU: ок"}}]}`))
	}))
	defer srv.Close()

	// Путь вместе с каталогом, которого ещё нет.
	promptPath := filepath.Join(t.TempDir(), "candidate_context", "system_prompt.md")

	p, err := New(Config{
		LLMAPIKey:        "test-key",
		LLMBaseURL:       srv.URL + "/v1",
		LLMModel:         "test-model",
		SystemPromptFile: promptPath,
	})
	if err != nil {
		t.Fatalf("New должен создать отсутствующий файл шаблона: %v", err)
	}
	if _, statErr := os.Stat(promptPath); statErr != nil {
		t.Fatalf("файл шаблона не создан: %v", statErr)
	}

	if _, err := p.engine.GenerateAnswers(context.Background(), translator.AnswerRequest{
		Question:   "What is Go?",
		SourceLang: "en",
		TargetLang: "de",
	}); err != nil {
		t.Fatalf("GenerateAnswers: %v", err)
	}

	select {
	case got := <-systemCh:
		for _, want := range []string{"first person", "EN:", "| DE:"} {
			if !strings.Contains(got, want) {
				t.Errorf("system-сообщение должно содержать %q (правила дефолта), got:\n%s", want, got)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("LLM-сервер не получил запрос")
	}
}

// TestPipelineNew_SystemPromptFileApplied — путь из Config доходит до реального
// тела запроса к LLM: первое сообщение — system с отрендеренным шаблоном из файла.
func TestPipelineNew_SystemPromptFileApplied(t *testing.T) {
	systemCh := make(chan string, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil && len(body.Messages) > 0 {
			systemCh <- body.Messages[0].Content
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"- EN: ok | RU: ок"}}]}`))
	}))
	defer srv.Close()

	promptPath := filepath.Join(t.TempDir(), "prompt.md")
	if err := os.WriteFile(promptPath, []byte("Pipeline template {{SRC_TAG}}/{{TGT_TAG}}.\n"), 0o600); err != nil {
		t.Fatalf("запись шаблона: %v", err)
	}

	p, err := New(Config{
		LLMAPIKey:        "test-key",
		LLMBaseURL:       srv.URL + "/v1",
		LLMModel:         "test-model",
		SystemPromptFile: promptPath,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.engine == nil {
		t.Fatal("engine не должен быть nil")
	}

	if _, err := p.engine.GenerateAnswers(context.Background(), translator.AnswerRequest{
		Question:   "What is Go?",
		SourceLang: "en",
		TargetLang: "de",
	}); err != nil {
		t.Fatalf("GenerateAnswers: %v", err)
	}

	select {
	case got := <-systemCh:
		if want := "Pipeline template EN/DE.\n"; got != want {
			t.Errorf("system-сообщение = %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("LLM-сервер не получил запрос")
	}
}
