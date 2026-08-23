package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mastererik/translator/internal/common"
)

// noopLogf — логгер-заглушка для resolveCandidateContext в тестах.
func noopLogf(string) {}

// writeCandidateContextDir создаёт валидную структуру candidate_context:
// manifest.json + sections/experience.md с fact marker'ом.
func writeCandidateContextDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sections"), 0o755); err != nil {
		t.Fatalf("mkdir sections: %v", err)
	}
	manifest := `{
  "profile": "Erik Ivanov",
  "global_aliases": {},
  "sections": [{"id": "experience", "title": "Опыт"}]
}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest.json: %v", err)
	}
	section := `<!-- fact
{
  "id": "pricing-engine",
  "section": "experience",
  "title": "Pricing Engine",
  "keywords": ["pricing", "kafka"]
}
-->
Built a real-time pricing engine in Go.
`
	if err := os.WriteFile(filepath.Join(dir, "sections", "experience.md"), []byte(section), 0o644); err != nil {
		t.Fatalf("write experience.md: %v", err)
	}
	return dir
}

// writeInvalidCandidateContextDir создаёт структуру candidate_context, которая
// падает на BuildIndex: manifest ссылается на секцию без source-файла.
func writeInvalidCandidateContextDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	manifest := `{"profile": "Erik", "sections": [{"id": "experience"}]}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest.json: %v", err)
	}
	return dir
}

// writeLegacyFile создаёт временный legacy candidate context файл с содержимым.
func writeLegacyFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cv.md")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write legacy file: %v", err)
	}
	return p
}

// TestResolveCandidateContextLegacy — legacy путь: Dir="", File задан.
func TestResolveCandidateContextLegacy(t *testing.T) {
	content := "# Candidate CV\nsome facts\n"
	file := writeLegacyFile(t, content)

	legacy, fn, err := resolveCandidateContext(Config{CandidateContextFile: file}, noopLogf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if legacy != content {
		t.Errorf("legacy context: got %q, want %q", legacy, content)
	}
	if fn != nil {
		t.Error("fn должен быть nil в legacy-режиме")
	}
}

// TestResolveCandidateContextNoSource — Dir="" и File="" → оба пустые.
func TestResolveCandidateContextNoSource(t *testing.T) {
	legacy, fn, err := resolveCandidateContext(Config{}, noopLogf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if legacy != "" {
		t.Errorf("legacy: got %q, want empty", legacy)
	}
	if fn != nil {
		t.Error("fn должен быть nil без источника")
	}
}

// TestResolveCandidateContextFactLevel — новый путь: Dir задан с валидной структурой.
func TestResolveCandidateContextFactLevel(t *testing.T) {
	dir := writeCandidateContextDir(t)
	cfg := Config{
		CandidateContext: common.CandidateContextConfig{
			Dir:              dir,
			MaxTokens:        2000,
			MaxProfileTokens: 150,
			MinScore:         0.0,
			TopK:             5,
		},
	}

	legacy, fn, err := resolveCandidateContext(cfg, noopLogf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if legacy != "" {
		t.Errorf("legacy должен быть пустым в fact-level режиме, got %q", legacy)
	}
	if fn == nil {
		t.Fatal("fn не должен быть nil в fact-level режиме")
	}

	got := fn("pricing engine kafka")
	if !strings.Contains(got, "Erik Ivanov") {
		t.Errorf("rendered context должен содержать профиль, got %q", got)
	}
	if !strings.Contains(got, "pricing engine") {
		t.Errorf("rendered context должен содержать факт, got %q", got)
	}
}

// TestResolveCandidateContextDirPriorityOverLegacyFile — при заданных и Dir, и
// legacy файле факт-режим (Dir) имеет приоритет: legacy == "" и fn != nil.
func TestResolveCandidateContextDirPriorityOverLegacyFile(t *testing.T) {
	dir := writeCandidateContextDir(t)
	file := writeLegacyFile(t, "legacy candidate context")

	cfg := Config{
		CandidateContextFile: file,
		CandidateContext: common.CandidateContextConfig{
			Dir:              dir,
			MaxTokens:        2000,
			MaxProfileTokens: 150,
			MinScore:         0.0,
			TopK:             5,
		},
	}

	legacy, fn, err := resolveCandidateContext(cfg, noopLogf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if legacy != "" {
		t.Errorf("legacy должен быть пустым при приоритете Dir, got %q", legacy)
	}
	if fn == nil {
		t.Fatal("fn не должен быть nil при приоритете Dir")
	}

	got := fn("pricing engine kafka")
	if !strings.Contains(got, "Erik Ivanov") {
		t.Errorf("rendered context должен содержать профиль, got %q", got)
	}
}

// TestResolveCandidateContextRebuildFailureWithFallback — невалидная структура
// Dir + заданный legacy файл → читается legacy файл (fn=nil, err=nil).
func TestResolveCandidateContextRebuildFailureWithFallback(t *testing.T) {
	badDir := writeInvalidCandidateContextDir(t)
	content := "legacy candidate context"
	file := writeLegacyFile(t, content)

	cfg := Config{
		CandidateContextFile: file,
		CandidateContext:     common.CandidateContextConfig{Dir: badDir},
	}

	legacy, fn, err := resolveCandidateContext(cfg, noopLogf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if legacy != content {
		t.Errorf("legacy fallback: got %q, want %q", legacy, content)
	}
	if fn != nil {
		t.Error("fn должен быть nil при fallback на legacy файл")
	}
}

// TestResolveCandidateContextRebuildFailureNoFallback — невалидная структура Dir
// без legacy файла → фатальная ошибка.
func TestResolveCandidateContextRebuildFailureNoFallback(t *testing.T) {
	badDir := writeInvalidCandidateContextDir(t)
	cfg := Config{
		CandidateContext: common.CandidateContextConfig{Dir: badDir},
	}

	_, fn, err := resolveCandidateContext(cfg, noopLogf)
	if err == nil {
		t.Fatal("ожидалась ошибка для невалидной структуры без legacy файла")
	}
	if fn != nil {
		t.Error("fn должен быть nil при фатальной ошибке")
	}
}

// TestResolveCandidateContextLegacyReadFailure — legacy файл не читается →
// пустой legacyContext без ошибки (только warn).
func TestResolveCandidateContextLegacyReadFailure(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.md")
	legacy, fn, err := resolveCandidateContext(Config{CandidateContextFile: missing}, noopLogf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if legacy != "" {
		t.Errorf("legacy: got %q, want empty", legacy)
	}
	if fn != nil {
		t.Error("fn должен быть nil при ошибке чтения legacy файла")
	}
}
