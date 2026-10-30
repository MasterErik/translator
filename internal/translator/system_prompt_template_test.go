package translator

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

// TestFixtureTemplateParses — фикстура тестов обязана парситься, содержать
// формат EN:|RU: и не иметь нераскрытых плейсхолдеров, иначе тесты проверяют
// не тот контракт, что уходит в LLM.
func TestFixtureTemplateParses(t *testing.T) {
	tpl := mustTestSystemPromptTemplate(t)
	rendered := tpl.Render("en", "ru")
	if !strings.Contains(rendered, "- EN:") || !strings.Contains(rendered, "| RU:") {
		t.Errorf("фикстура потеряла формат EN:|RU:, got:\n%s", rendered)
	}
	if strings.Contains(rendered, "{{") {
		t.Errorf("в отрендеренном шаблоне остались плейсхолдеры:\n%s", rendered)
	}
}

// TestEmbeddedDefaultTemplateContract — встроенный (go:embed) дефолт должен
// парситься и содержать контракт первого запуска: оба плейсхолдера и правило
// простого английского первым (оно приоритетнее остальных правил стиля).
func TestEmbeddedDefaultTemplateContract(t *testing.T) {
	tpl, err := ParseSystemPromptTemplate(defaultSystemPromptBody)
	if err != nil {
		t.Fatalf("встроенный дефолт не парсится: %v", err)
	}
	if !strings.Contains(defaultSystemPromptBody, SystemPromptPlaceholderSrcTag) ||
		!strings.Contains(defaultSystemPromptBody, SystemPromptPlaceholderTgtTag) {
		t.Errorf("встроенный дефолт потерял плейсхолдеры языков")
	}

	rendered := tpl.Render("en", "ru")
	firstLine := strings.SplitN(rendered, "\n", 2)[0]
	if !strings.Contains(firstLine, "SIMPLE ENGLISH") {
		t.Errorf("правило простого английского должно идти первым, первая строка: %q", firstLine)
	}
}

// TestLoadSystemPromptTemplate_CreatesFileFromDefault — отсутствующий файл
// создаётся сам при старте из встроенного дефолта (включая каталог), и работа
// продолжается на нём; повторный запуск читает уже файл, а не дефолт.
func TestLoadSystemPromptTemplate_CreatesFileFromDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "system_prompt.md")

	tpl, err := LoadSystemPromptTemplate(path)
	if err != nil {
		t.Fatalf("LoadSystemPromptTemplate должен создать отсутствующий файл: %v", err)
	}
	if got, want := tpl.Render("en", "ru"), mustTestSystemPromptTemplate(t).Render("en", "ru"); got != want {
		t.Errorf("созданный шаблон должен рендериться как дефолт:\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("файл не создан: %v", err)
	}
	if string(data) != defaultSystemPromptBody {
		t.Errorf("созданный файл не совпадает со встроенным дефолтом:\n--- файл ---\n%q", string(data))
	}

	// Правка файла должна подхватываться: источник правды — файл, не бинарь.
	if err := os.WriteFile(path, []byte("Edited rules: {{SRC_TAG}}|{{TGT_TAG}}\n"), 0o600); err != nil {
		t.Fatalf("правка файла: %v", err)
	}
	edited, err := LoadSystemPromptTemplate(path)
	if err != nil {
		t.Fatalf("LoadSystemPromptTemplate после правки: %v", err)
	}
	if got, want := edited.Render("ru", "en"), "Edited rules: RU|EN\n"; got != want {
		t.Errorf("правка файла не применилась: got %q, want %q", got, want)
	}
}

// TestSystemPromptTemplateRender_Placeholders — языки подставляются тегами в
// верхнем регистре; пустые значения дают дефолт en/ru.
func TestSystemPromptTemplateRender_Placeholders(t *testing.T) {
	tpl, err := ParseSystemPromptTemplate("Rules: {{SRC_TAG}} -> {{TGT_TAG}}\n")
	if err != nil {
		t.Fatalf("ParseSystemPromptTemplate: %v", err)
	}

	cases := []struct {
		name       string
		src, tgt   string
		wantRender string
	}{
		{"lowercase", "en", "de", "Rules: EN -> DE\n"},
		{"uppercase input", "EN", "RU", "Rules: EN -> RU\n"},
		{"mixed case", "En", "Ru", "Rules: EN -> RU\n"},
		{"empty languages fall back", "", "", "Rules: EN -> RU\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tpl.Render(tc.src, tc.tgt); got != tc.wantRender {
				t.Errorf("Render(%q,%q) = %q, want %q", tc.src, tc.tgt, got, tc.wantRender)
			}
		})
	}
}

// TestSystemPromptTemplateRender_NormalizesText — CRLF из Windows-редактора и
// лишние пустые строки по краям не должны менять контракт: результат всегда
// с одним завершающим "\n" (к нему приклеивается секция candidate context).
func TestSystemPromptTemplateRender_NormalizesText(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"crlf", "line one\r\nline two\r\n", "line one\nline two\n"},
		{"leading blank lines", "\n\nline one\n", "line one\n"},
		{"trailing blank lines", "line one\n\n\n", "line one\n"},
		{"no trailing newline", "line one", "line one\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tpl, err := ParseSystemPromptTemplate(tc.body)
			if err != nil {
				t.Fatalf("ParseSystemPromptTemplate: %v", err)
			}
			if got := tpl.Render("en", "ru"); got != tc.want {
				t.Errorf("Render() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestParseSystemPromptTemplate_Errors — пустой шаблон и неизвестный
// плейсхолдер отвергаются на старте, а не уезжают в LLM.
func TestParseSystemPromptTemplate_Errors(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string // подстрока ожидаемой ошибки
	}{
		{"empty", "", "пустой шаблон"},
		{"whitespace only", "  \n\t\n ", "пустой шаблон"},
		{"unknown placeholder", "rules {{SOURCE_LANG}}", "{{SOURCE_LANG}}"},
		{"typo placeholder", "rules {{TGT_TAGS}}", "{{TGT_TAGS}}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tpl, err := ParseSystemPromptTemplate(tc.body)
			if err == nil {
				t.Fatalf("ожидалась ошибка, получен шаблон %+v", tpl)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("ошибка %q не содержит %q", err.Error(), tc.want)
			}
		})
	}
}

// TestLoadSystemPromptTemplate_EmptyPathFails — встроенного дефолта нет:
// пустой SYSTEM_PROMPT_FILE обязан валить старт, а не работать «как-то».
func TestLoadSystemPromptTemplate_EmptyPathFails(t *testing.T) {
	for _, path := range []string{"", "   "} {
		tpl, err := LoadSystemPromptTemplate(path)
		if err == nil {
			t.Fatalf("LoadSystemPromptTemplate(%q) должен вернуть ошибку, got %+v", path, tpl)
		}
		if !strings.Contains(err.Error(), "SYSTEM_PROMPT_FILE") {
			t.Errorf("ошибка должна называть SYSTEM_PROMPT_FILE, got: %v", err)
		}
	}
}

// TestLoadSystemPromptTemplate_FileErrors — битый шаблон и физически
// невозможный путь дают ошибку (fail-fast), а не тихую подмену дефолтом.
func TestLoadSystemPromptTemplate_FileErrors(t *testing.T) {
	// Родитель пути — обычный файл: создать там каталог нельзя, значит и файл
	// шаблона создать не получится.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("not a dir"), 0o600); err != nil {
		t.Fatalf("фикстура: %v", err)
	}
	if _, err := LoadSystemPromptTemplate(filepath.Join(blocker, "system_prompt.md")); err == nil {
		t.Fatal("ожидалась ошибка создания файла по невозможному пути")
	}

	broken := filepath.Join(t.TempDir(), "broken.md")
	if err := os.WriteFile(broken, []byte("rules {{NOPE}}"), 0o600); err != nil {
		t.Fatalf("запись фикстуры: %v", err)
	}
	if _, err := LoadSystemPromptTemplate(broken); err == nil {
		t.Fatal("ожидалась ошибка неизвестного плейсхолдера")
	}
}

// TestLoadSystemPromptTemplate_FromFile — шаблон читается из файла и рендерится
// с подставленной языковой парой.
func TestLoadSystemPromptTemplate_FromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prompt.md")
	body := "Custom rule for {{SRC_TAG}} to {{TGT_TAG}}.\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("запись фикстуры: %v", err)
	}

	tpl, err := LoadSystemPromptTemplate(path)
	if err != nil {
		t.Fatalf("LoadSystemPromptTemplate: %v", err)
	}
	if got, want := tpl.Render("de", "en"), "Custom rule for DE to EN.\n"; got != want {
		t.Errorf("Render() = %q, want %q", got, want)
	}
}

// TestChatProvider_NoTemplateFails — провайдер без шаблона не должен отправлять
// запрос в LLM: иначе в system уходит пустая строка и ответ деградирует молча.
func TestChatProvider_NoTemplateFails(t *testing.T) {
	server := setupMockServer(func(w http.ResponseWriter, r *http.Request) {
		t.Error("запрос к LLM не должен уходить без шаблона system-промпта")
		w.WriteHeader(http.StatusInternalServerError)
	})
	defer server.Close()

	prov := NewChatProvider(server.URL+"/v1", "sk-test-key", "test-model")

	if _, err := prov.GenerateAnswers(context.Background(), AnswerRequest{Question: "What is Go?"}); err == nil {
		t.Fatal("ожидалась ошибка: шаблон system-промпта не задан")
	} else if !strings.Contains(err.Error(), "SYSTEM_PROMPT_FILE") {
		t.Errorf("ошибка должна называть SYSTEM_PROMPT_FILE, got: %v", err)
	}

	if _, err := prov.GenerateAnswersStream(context.Background(), AnswerRequest{Question: "What is Go?"}); err == nil {
		t.Fatal("ожидалась ошибка стрима: шаблон system-промпта не задан")
	}
}

// TestChatProvider_SystemPromptBodyFromTemplate проверяет реальный контракт
// запроса: system-сообщение — это отрендеренный шаблон из файла плюс секция
// candidate context, и оно идёт первым в messages.
func TestChatProvider_SystemPromptBodyFromTemplate(t *testing.T) {
	systemCh := make(chan string, 1)
	roleCh := make(chan string, 1)

	server := setupMockServer(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Messages) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		roleCh <- body.Messages[0].Role
		systemCh <- body.Messages[0].Content

		resp := openai.ChatCompletionResponse{Choices: []openai.ChatCompletionChoice{
			{Message: openai.ChatCompletionMessage{Content: "- EN: hello | RU: привет"}},
		}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	prov := newTestChatProvider(server, "test-model")
	tpl, err := ParseSystemPromptTemplate("Custom rules for {{SRC_TAG}} → {{TGT_TAG}}.\n")
	if err != nil {
		t.Fatalf("ParseSystemPromptTemplate: %v", err)
	}
	prov.SetSystemPromptTemplate(tpl)

	if _, err := prov.GenerateAnswers(context.Background(), AnswerRequest{
		Question:         "What is Go?",
		CandidateContext: "5 years of Go",
		SourceLang:       "en",
		TargetLang:       "de",
	}); err != nil {
		t.Fatalf("GenerateAnswers: %v", err)
	}

	if role := <-roleCh; role != openai.ChatMessageRoleSystem {
		t.Errorf("первое сообщение должно быть system, got %q", role)
	}
	want := "Custom rules for EN → DE.\n\nCandidate context:\n5 years of Go"
	if got := <-systemCh; got != want {
		t.Errorf("system-сообщение = %q, want %q", got, want)
	}
}
