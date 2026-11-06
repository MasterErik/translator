package translator

import (
	"strings"
	"testing"
)

// ── F8: system-промпт — graceful-смягчение и режимы формата ─────────────────

// TestSystemPromptGracefulFallback — B6: правило graceful-смягчения
// (generic knowledge с явной пометкой, без атрибуции кандидату) и
// correct/incorrect-пример границ стеков присутствуют в рендере.
func TestSystemPromptGracefulFallback(t *testing.T) {
	tpl := mustTestSystemPromptTemplate(t)
	rendered := tpl.Render("en", "ru")

	for _, want := range []string{
		"clearly marked generic knowledge",
		"do NOT attribute it to the candidate's experience",
		"Incorrect:",
		"haven't used Terraform",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("system-промпт должен содержать %q, got:\n%s", want, rendered)
		}
	}

	// Правило не зависит от языковой пары.
	if got := tpl.Render("en", "de"); !strings.Contains(got, "clearly marked generic knowledge") {
		t.Error("graceful-правило должно присутствовать при любой паре языков")
	}
}

// TestRenderFormatModes — {{RESPONSE_FORMAT}} раскрывается согласно режиму:
// prompt/response_format — JSON-схема, off — строковый формат.
func TestRenderFormatModes(t *testing.T) {
	tpl := mustTestSystemPromptTemplate(t)

	for _, mode := range []string{JSONModePrompt, JSONModeResponseFormat} {
		got := tpl.RenderFormat("en", "ru", mode)
		if !strings.Contains(got, `{"answers":[`) || !strings.Contains(got, "into RU") {
			t.Errorf("режим %q: ожидалась JSON-схема с тегами, got:\n%s", mode, got)
		}
		if strings.Contains(got, SystemPromptPlaceholderFormat) {
			t.Errorf("режим %q: плейсхолдер формата не раскрыт, got:\n%s", mode, got)
		}
	}

	off := tpl.RenderFormat("en", "de", JSONModeOff)
	if !strings.Contains(off, "- EN:") || !strings.Contains(off, "| DE:") {
		t.Errorf("режим off: ожидался строковый формат «- EN: … | DE: …», got:\n%s", off)
	}
	if strings.Contains(off, `{"answers":[`) {
		t.Errorf("режим off: JSON-схема не должна присутствовать, got:\n%s", off)
	}
}

// TestRenderLegacyTemplateWithoutFormatPlaceholder — шаблон без
// {{RESPONSE_FORMAT}} (старый файл пользователя) рендерится без подмены
// формата: текст файла остаётся единственным источником инструкций.
func TestRenderLegacyTemplateWithoutFormatPlaceholder(t *testing.T) {
	legacy := "Rules here. Answer as {{SRC_TAG}} -> {{TGT_TAG}}."
	tpl, err := ParseSystemPromptTemplate(legacy)
	if err != nil {
		t.Fatalf("ParseSystemPromptTemplate() error = %v", err)
	}
	got := tpl.Render("en", "ru")
	want := normalizeSystemPromptText("Rules here. Answer as EN -> RU.")
	if got != want {
		t.Errorf("legacy-шаблон:\n got %q\nwant %q", got, want)
	}
}

// TestParseSystemPromptTemplateUnknownPlaceholder — неизвестные плейсхолдеры
// по-прежнему отвергаются (fail-fast), включая опечатки в новом.
func TestParseSystemPromptTemplateUnknownPlaceholder(t *testing.T) {
	for _, bad := range []string{"Ответ: {{TYPO}}", "{{Response_Format}}"} {
		if _, err := ParseSystemPromptTemplate("x " + bad + " y"); err == nil {
			t.Errorf("ParseSystemPromptTemplate(%q) = nil, want ошибка", bad)
		}
	}
}

// TestBuildSystemPromptWithModeUsesProviderMode — провайдер собирает
// system-промпт с раскрытием формата по своему jsonMode (off → строковый).
func TestBuildSystemPromptWithModeUsesProviderMode(t *testing.T) {
	tpl := mustTestSystemPromptTemplate(t)

	jsonPrompt := buildSystemPromptWithMode(tpl, "", "en", "ru", JSONModePrompt)
	if !strings.Contains(jsonPrompt, `{"answers":[`) {
		t.Error("режим prompt: ожидалась JSON-схема в system-промпте")
	}

	offPrompt := buildSystemPromptWithMode(tpl, "", "en", "ru", JSONModeOff)
	if strings.Contains(offPrompt, `{"answers":[`) || !strings.Contains(offPrompt, "- EN:") {
		t.Error("режим off: ожидался строковый формат в system-промпте")
	}

	// Секция candidate context не мешает раскрытию формата.
	withCtx := buildSystemPromptWithMode(tpl, "cv facts", "en", "ru", JSONModeOff)
	if !strings.Contains(withCtx, "Candidate context:\ncv facts") || !strings.Contains(withCtx, "- EN:") {
		t.Errorf("candidate context не должен затирать блок формата, got:\n%s", withCtx)
	}
}
