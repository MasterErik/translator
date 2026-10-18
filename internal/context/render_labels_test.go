package candidatecontext

// Snapshot-тесты M9: метки фактов «[Stack · period · weight]», пометка
// OUT OF TARGET STACK, строка пустой выдачи и разрешение Family.Display при
// материализации RetrievalResult.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mastererik/translator/internal/context/stackdet"
)

// m9Families — минимальная таблица семейств для проверки Display.
func m9Families() []stackdet.Family {
	return []stackdet.Family{
		{Name: "java", Display: "Java", Members: []string{"java", "spring"}, Ambiguous: []string{"spring"}},
		{Name: "golang", Display: "Go", Members: []string{"golang"}},
		{Name: "php", Display: "PHP", Members: []string{"php", "laravel"}},
		// Семья без display — метка падает на каноническое имя.
		{Name: "rust", Members: []string{"rust"}},
	}
}

// TestFormatPeriod фиксирует M9.1: дефис периода печатается как en dash.
func TestFormatPeriod(t *testing.T) {
	tests := []struct{ in, want string }{
		{"2018-2024", "2018\u20132024"},
		{"2021-present", "2021\u2013present"},
		{"2018", "2018"},
		{"", ""},
		{"  2019-2020  ", "2019\u20132020"},
		// Нераспознанный формат не переписывается.
		{"since 2018", "since 2018"},
	}
	for _, tc := range tests {
		if got := FormatPeriod(tc.in); got != tc.want {
			t.Errorf("FormatPeriod(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestRetrievalResultLabel проверяет сборку метки: пустые поля опускаются,
// StackDisplay приоритетнее Stack.
func TestRetrievalResultLabel(t *testing.T) {
	tests := []struct {
		name string
		res  RetrievalResult
		want string
	}{
		{
			name: "полная метка",
			res:  RetrievalResult{StackDisplay: []string{"Java"}, Period: "2018-2024", Weight: "primary"},
			want: "[Java · 2018\u20132024 · primary]",
		},
		{
			name: "fallback на каноническое имя",
			res:  RetrievalResult{Stack: []string{"golang"}, Weight: "secondary"},
			want: "[golang · secondary]",
		},
		{
			name: "несколько стеков",
			res:  RetrievalResult{StackDisplay: []string{"Java", "Go"}, Period: "2021-present"},
			want: "[Java, Go · 2021\u2013present]",
		},
		{
			name: "только период",
			res:  RetrievalResult{Period: "2020"},
			want: "[2020]",
		},
		{
			name: "пустые поля — пустая метка",
			res:  RetrievalResult{},
			want: "",
		},
		{
			name: "пустые элементы стека опускаются",
			res:  RetrievalResult{Stack: []string{"", "  "}, Weight: "background"},
			want: "[background]",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.res.Label(); got != tc.want {
				t.Errorf("Label() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRenderLabelsSnapshot — побайтный snapshot вывода с метками, out-of-target
// пометкой и порядком блоков профиль → матрица → факты.
func TestRenderLabelsSnapshot(t *testing.T) {
	cc := CandidateContext{
		Profile: "Erik Ivanov",
		Matrix:  "stack | period\nJava | 2018-2024\nGo | 2021-present",
		Facts: []RetrievalResult{
			{
				FactID:       "java-alarm",
				Content:      "Designed an alarm platform on Java 21.",
				StackDisplay: []string{"Java"},
				Period:       "2018-2024",
				Weight:       "primary",
			},
			{
				FactID:       "php-legacy",
				Content:      "Maintained a PHP/Laravel billing service.",
				StackDisplay: []string{"PHP"},
				Period:       "2016-2018",
				Weight:       "secondary",
				OutOfTarget:  true,
			},
			{
				FactID:  "no-label",
				Content: "Only body, no stack metadata.",
			},
		},
	}
	got := cc.Render()
	want := "Candidate profile:\n" +
		"Erik Ivanov\n" +
		"\n" +
		"Technical matrix:\n" +
		"stack | period\n" +
		"Java | 2018-2024\n" +
		"Go | 2021-present\n" +
		"\n" +
		"[Java · 2018\u20132024 · primary]\n" +
		"Designed an alarm platform on Java 21.\n" +
		"\n" +
		"[PHP · 2016\u20132018 · secondary]\n" +
		OutOfTargetNotice + "\n" +
		"Maintained a PHP/Laravel billing service.\n" +
		"\n" +
		"Only body, no stack metadata."
	if got != want {
		t.Errorf("Render mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestRenderOutOfTargetOnly — пометка печатается и без остальных полей метки,
// и всегда перед телом факта.
func TestRenderOutOfTargetOnly(t *testing.T) {
	cc := CandidateContext{Facts: []RetrievalResult{
		{FactID: "x", Content: "PHP billing work.", OutOfTarget: true},
	}}
	got := cc.Render()
	want := OutOfTargetNotice + "\nPHP billing work."
	if got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
}

// TestRenderOutOfTargetText — текст пометки по M9.4 (снапшот константы).
func TestRenderOutOfTargetText(t *testing.T) {
	want := "OUT OF TARGET STACK — use only if the question is directly about it"
	if OutOfTargetNotice != want {
		t.Errorf("OutOfTargetNotice = %q, want %q", OutOfTargetNotice, want)
	}
	wantEmpty := "No relevant facts found — rely on the candidate profile and Technical Matrix; do not invent projects."
	if EmptyRetrievalNotice != wantEmpty {
		t.Errorf("EmptyRetrievalNotice = %q, want %q", EmptyRetrievalNotice, wantEmpty)
	}
}

// TestRenderEmptyWithProfileSnapshot — пустая выдача после профиля отделяется
// пустой строкой и не даёт хвостовых пробелов.
func TestRenderEmptyWithProfileSnapshot(t *testing.T) {
	cc := CandidateContext{Profile: "Erik Ivanov"}
	want := "Candidate profile:\nErik Ivanov\n\n" + EmptyRetrievalNotice
	if got := cc.Render(); got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
}

// TestStackDisplayResolution — Family.Display разрешается при материализации:
// java → Java, golang → Go, неизвестная семья → каноническое имя.
func TestStackDisplayResolution(t *testing.T) {
	r := &Retriever{families: m9Families()}
	tests := []struct {
		name  string
		stack []string
		want  []string
	}{
		{"java", []string{"java"}, []string{"Java"}},
		{"golang", []string{"golang"}, []string{"Go"}},
		{"несколько", []string{"java", "php"}, []string{"Java", "PHP"}},
		{"неизвестная семья", []string{"cobol"}, []string{"cobol"}},
		{"семья без display", []string{"rust"}, []string{"rust"}},
		{"пустой стек", nil, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := r.stackDisplay(tc.stack)
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("stackDisplay(%v) = %v, want %v", tc.stack, got, tc.want)
			}
		})
	}
}

// writeLabeledCCDir создаёт каталог candidate context с размеченным фактом
// (stack/period/weight) — как его читает LoadCandidateContext.
func writeLabeledCCDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "manifest.json"), `{
  "profile": "Erik Ivanov",
  "global_aliases": {},
  "sections": [{"id": "experience", "title": "Опыт"}],
  "tech_families": [
    {"name": "java", "display": "Java", "members": ["java", "spring"], "ambiguous": ["spring"]},
    {"name": "golang", "display": "Go", "members": ["golang"]}
  ]
}`)
	writeFile(t, filepath.Join(dir, "sections", "experience.md"), `<!-- fact
{
  "id": "java-alarm",
  "section": "experience",
  "title": "Java alarm platform",
  "keywords": ["java", "alarm"],
  "stack": ["java"],
  "period": "2018-2024",
  "weight": "primary"
}
-->
Designed an alarm processing platform on Java 21.
`)
	return dir
}

// TestRetrieverResultCarriesLabels — result() переносит Stack/Period/Weight из
// Fact и заполняет StackDisplay по manifest.TechFamilies.
func TestRetrieverResultCarriesLabels(t *testing.T) {
	dir := writeLabeledCCDir(t)
	manifest, index, err := LoadCandidateContext(dir)
	if err != nil {
		t.Fatalf("LoadCandidateContext: %v", err)
	}
	r, err := NewRetrieverWithOptions(manifest, index, dir, RetrieverOptions{
		TopK:              1,
		ContentGateTau:    -1,
		RelativeThreshold: -1,
	})
	if err != nil {
		t.Fatalf("NewRetrieverWithOptions: %v", err)
	}
	res := r.Retrieve("java alarm platform")
	if len(res) != 1 {
		t.Fatalf("ожидался 1 факт, got %v", res)
	}
	got := res[0]
	if len(got.Stack) != 1 || got.Stack[0] != "java" {
		t.Errorf("Stack: got %v, want [java]", got.Stack)
	}
	if len(got.StackDisplay) != 1 || got.StackDisplay[0] != "Java" {
		t.Errorf("StackDisplay: got %v, want [Java]", got.StackDisplay)
	}
	if got.Period != "2018-2024" {
		t.Errorf("Period: got %q, want 2018-2024", got.Period)
	}
	if got.Weight != "primary" {
		t.Errorf("Weight: got %q, want primary", got.Weight)
	}
	if want := "[Java · 2018\u20132024 · primary]"; got.Label() != want {
		t.Errorf("Label() = %q, want %q", got.Label(), want)
	}
}

// TestBuildContextFnRendersLabel — сквозной путь retrieval → budget → render:
// метка факта попадает в итоговый текст контекста.
func TestBuildContextFnRendersLabel(t *testing.T) {
	dir := writeLabeledCCDir(t)
	cfg := ccConfig(dir)
	cfg.MaxMatrixTokens = 50
	fn, err := BuildContextFn(cfg)
	if err != nil {
		t.Fatalf("BuildContextFn: %v", err)
	}
	out := fn("java alarm platform", SessionContext{}).Text
	want := "[Java · 2018\u20132024 · primary]\nDesigned an alarm processing platform on Java 21."
	if !strings.Contains(out, want) {
		t.Errorf("контекст без метки факта: %q", out)
	}
}

// writeOutOfTargetCCDir — каталог с Java-фактом и PHP-фактом, чтобы в java-
// сессии PHP-факт оказался чужим стеком.
func writeOutOfTargetCCDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "manifest.json"), `{
  "profile": "Erik Ivanov",
  "global_aliases": {},
  "sections": [{"id": "experience", "title": "Опыт"}],
  "tech_families": [
    {"name": "java", "display": "Java", "members": ["java", "spring"], "ambiguous": ["spring"]},
    {"name": "php", "display": "PHP", "members": ["php", "laravel"]}
  ]
}`)
	writeFile(t, filepath.Join(dir, "sections", "experience.md"), `<!-- fact
{
  "id": "java-billing",
  "section": "experience",
  "title": "Billing platform",
  "keywords": ["billing"],
  "stack": ["java"],
  "period": "2018-2024",
  "weight": "primary"
}
-->
Built the billing platform on Java and Spring Boot.
`+"\n"+`<!-- fact
{
  "id": "php-billing",
  "section": "experience",
  "title": "Billing service",
  "keywords": ["billing", "service"],
  "stack": ["php"],
  "period": "2016-2018",
  "weight": "secondary"
}
-->
Maintained the billing service in PHP and Laravel.
`)
	return dir
}

// TestBuildContextFnOutOfTargetNotice — M9.4: в java-сессии PHP-факт, прошедший
// в выдачу, помечен OUT OF TARGET STACK; свой факт — без пометки.
func TestBuildContextFnOutOfTargetNotice(t *testing.T) {
	dir := writeOutOfTargetCCDir(t)
	cfg := ccConfig(dir)
	cfg.ContentGateTau = -1
	cfg.RelativeThreshold = -1
	cfg.TopK = 5
	fn, err := BuildContextFn(cfg)
	if err != nil {
		t.Fatalf("BuildContextFn: %v", err)
	}
	out := fn("billing service", SessionContext{TargetStack: []string{"java"}}).Text
	if !strings.Contains(out, OutOfTargetNotice+"\nMaintained the billing service in PHP and Laravel.") {
		t.Errorf("PHP-факт без пометки out-of-target: %q", out)
	}
	if strings.Contains(out, OutOfTargetNotice+"\nBuilt the billing platform on Java") {
		t.Errorf("свой факт не должен быть помечен out-of-target: %q", out)
	}
}

// TestBuildContextFnEmptyRetrievalNotice — M9.3 сквозь пайплайн: вопрос без
// совпадений даёт явную строку пустой выдачи.
func TestBuildContextFnEmptyRetrievalNotice(t *testing.T) {
	dir := writeLabeledCCDir(t)
	fn, err := BuildContextFn(ccConfig(dir))
	if err != nil {
		t.Fatalf("BuildContextFn: %v", err)
	}
	out := fn("quantum chromodynamics lattice", SessionContext{}).Text
	if !strings.Contains(out, EmptyRetrievalNotice) {
		t.Errorf("нет строки пустой выдачи: %q", out)
	}
	if !strings.HasSuffix(out, EmptyRetrievalNotice) {
		t.Errorf("строка пустой выдачи должна завершать контекст: %q", out)
	}
}

// TestRenderLabelsFileSnapshot — файловый snapshot меток: закрепляет точный
// текст блоков в testdata, чтобы регресс формата не прошёл незамеченным.
func TestRenderLabelsFileSnapshot(t *testing.T) {
	dir := writeOutOfTargetCCDir(t)
	cfg := ccConfig(dir)
	cfg.ContentGateTau = -1
	cfg.RelativeThreshold = -1
	cfg.TopK = 5
	fn, err := BuildContextFn(cfg)
	if err != nil {
		t.Fatalf("BuildContextFn: %v", err)
	}
	got := fn("billing service", SessionContext{TargetStack: []string{"java"}}).Text

	path := filepath.Join("testdata", "render_labels_snapshot.txt")
	if os.Getenv("RENDER_LABELS_UPDATE") == "1" {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("запись snapshot: %v", err)
		}
		t.Logf("snapshot перезаписан: %s", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("чтение snapshot %s (пересоздать: RENDER_LABELS_UPDATE=1 go test ./internal/context -run TestRenderLabelsFileSnapshot): %v", path, err)
	}
	if got != string(want) {
		fmt.Printf("--- got ---\n%s\n--- want ---\n%s\n", got, want)
		t.Fatalf("snapshot расходится: %s", path)
	}
}
