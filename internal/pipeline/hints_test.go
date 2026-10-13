package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mastererik/translator/internal/common"
)

// writeHintsDir создаёт валидную структуру каталога hints: manifest.json +
// sections/answers.md с двумя fact marker'ами, у которых уникальные keywords.
func writeHintsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sections"), 0o755); err != nil {
		t.Fatalf("mkdir sections: %v", err)
	}
	manifest := `{
  "profile": "Hints",
  "global_aliases": {},
  "sections": [{"id": "answers", "title": "Ответы"}]
}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest.json: %v", err)
	}
	section := `<!-- fact
{
  "id": "hint-weaknesses",
  "section": "answers",
  "title": "Weaknesses",
  "keywords": ["weakness", "weaknesses", "недостатки"]
}
-->
My main weakness is perfectionism: I prefer to verify assumptions early.
<!-- fact
{
  "id": "hint-relocation",
  "section": "answers",
  "title": "Relocation",
  "keywords": ["relocation", "переезд", "relocate"]
}
-->
I am open to relocation to any EU country.
`
	if err := os.WriteFile(filepath.Join(dir, "sections", "answers.md"), []byte(section), 0o644); err != nil {
		t.Fatalf("write answers.md: %v", err)
	}
	return dir
}

// writeBrokenHintsDir создаёт каталог hints без manifest.json (загрузка падает).
func writeBrokenHintsDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// TestResolveHintsDisabled — Dir="" → nil, nil.
func TestResolveHintsDisabled(t *testing.T) {
	fn, err := resolveHints(Config{}, noopLogf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fn != nil {
		t.Error("fn должен быть nil при пустом каталоге hints")
	}
}

// TestResolveHintsMatchAndMiss — валидный каталог: вопрос по keywords даёт
// непустой ответ с телом факта, посторонний вопрос — пустую строку.
func TestResolveHintsMatchAndMiss(t *testing.T) {
	tests := []struct {
		name        string
		question    string
		wantSubstr  string
		wantNonempt bool
	}{
		{
			name:        "match по keywords",
			question:    "What is your biggest weakness?",
			wantSubstr:  "perfectionism",
			wantNonempt: true,
		},
		{
			name:        "match по второму факту",
			question:    "Are you open to relocation?",
			wantSubstr:  "relocation to any EU country",
			wantNonempt: true,
		},
		{
			name:        "посторонний вопрос",
			question:    "quantum chromodynamics lattice",
			wantNonempt: false,
		},
		{
			name:        "пустой вопрос",
			question:    "",
			wantNonempt: false,
		},
	}

	dir := writeHintsDir(t)
	cfg := Config{Hints: common.HintsConfig{Dir: dir, MinScore: 0.0, TopK: 1}}

	fn, err := resolveHints(cfg, noopLogf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fn == nil {
		t.Fatal("fn не должен быть nil при валидном каталоге hints")
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := fn(tc.question)
			if tc.wantNonempt {
				if got == "" {
					t.Fatalf("fn(%q): ожидалась непустая подсказка", tc.question)
				}
				if !strings.Contains(got, tc.wantSubstr) {
					t.Errorf("fn(%q): got %q, want substring %q", tc.question, got, tc.wantSubstr)
				}
				return
			}
			if got != "" {
				t.Errorf("fn(%q): got %q, want empty", tc.question, got)
			}
		})
	}
}

// TestResolveHintsBrokenDir — битый каталог hints → nil, nil без ошибки.
func TestResolveHintsBrokenDir(t *testing.T) {
	dir := writeBrokenHintsDir(t)
	cfg := Config{Hints: common.HintsConfig{Dir: dir, MinScore: 0.0, TopK: 1}}

	fn, err := resolveHints(cfg, noopLogf)
	if err != nil {
		t.Fatalf("ожидалось отсутствие ошибки при битом каталоге hints, got %v", err)
	}
	if fn != nil {
		t.Error("fn должен быть nil при битом каталоге hints")
	}
}

// TestResolveHintsMissingDir — несуществующий каталог hints → nil, nil.
func TestResolveHintsMissingDir(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-hints")
	cfg := Config{Hints: common.HintsConfig{Dir: missing, MinScore: 0.0, TopK: 1}}

	fn, err := resolveHints(cfg, noopLogf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fn != nil {
		t.Error("fn должен быть nil для несуществующего каталога hints")
	}
}

// TestAppendHint — склейка базового ответа и подсказки.
func TestAppendHint(t *testing.T) {
	const header = "Suggested answer (use as is):"

	tests := []struct {
		name     string
		base     string
		hint     string
		want     string
		contains []string
	}{
		{
			name: "пустой hint возвращает base без изменений",
			base: "base answer",
			hint: "",
			want: "base answer",
		},
		{
			name: "пустой hint и пустой base",
			base: "",
			hint: "",
			want: "",
		},
		{
			name:     "непустой hint при пустом base начинается с заголовка",
			base:     "",
			hint:     "hint body",
			want:     header + "\nhint body",
			contains: []string{header, "hint body"},
		},
		{
			name:     "оба непустые содержат base и hint",
			base:     "base answer",
			hint:     "hint body",
			want:     "base answer\n\n" + header + "\nhint body",
			contains: []string{"base answer", header, "hint body"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := appendHint(tc.base, tc.hint)
			if got != tc.want {
				t.Errorf("appendHint(%q, %q): got %q, want %q", tc.base, tc.hint, got, tc.want)
			}
			for _, sub := range tc.contains {
				if !strings.Contains(got, sub) {
					t.Errorf("appendHint(%q, %q): got %q, want substring %q", tc.base, tc.hint, got, sub)
				}
			}
			if tc.hint == "" && got != tc.base {
				t.Errorf("пустой hint не должен менять base: got %q, want %q", got, tc.base)
			}
		})
	}
}
