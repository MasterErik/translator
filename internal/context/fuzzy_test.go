package candidatecontext

import (
	"strings"
	"testing"
)

// ── F13: ASR-fuzzy коррекция запроса (Damerau-Levenshtein ≤ 1) ──────────────

// TestWithinOneEdit — таблица расстояния ≤ 1 (все 4 типа правок + отказы).
func TestWithinOneEdit(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		// Равенство.
		{"", "", true},
		{"kubernetes", "kubernetes", true},
		// Замена.
		{"kubernates", "kubernetes", true},
		{"питон", "пйтон", true},
		// Вставка / удаление.
		{"kubernets", "kubernetes", true}, // вставка "e"
		{"docer", "docker", true},
		{"docker", "docer", true},
		{"postgresq", "postgresql", true},
		{"", "a", true},
		{"a", "", true},
		// Транспозиция соседних.
		{"redis", "rdeis", true},
		// Две правки — отказ.
		{"rdiis", "redis", false},
		{"пайтон", "питон", false},
		{"cooberneties", "kubernetes", false},
		{"docker", "redis", false},
		{"redis", "dockers", false},
	}

	for _, tt := range tests {
		if got := withinOneEdit(tt.a, tt.b); got != tt.want {
			t.Errorf("withinOneEdit(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

// TestFuzzyCorrector — правила коррекции: длина, «в словаре», однозначный
// кандидат, неоднозначность, отсутствие кандидата, детерминизм.
func TestFuzzyCorrector(t *testing.T) {
	vocab := map[string]struct{}{
		"kubernetes": {}, "docker": {}, "redis": {}, "postgresql": {},
		"mongo": {}, "mongi": {}, // пара кандидатов в радиусе 1 от "monga"
	}
	c := newFuzzyCorrector(vocab)

	tests := []struct {
		token string
		want  string
	}{
		// Однозначный кандидат в радиусе 1 — коррекция.
		{"kubernets", "kubernetes"},
		{"kubernates", "kubernetes"},
		{"docke", "docker"},
		{"postgresq", "postgresql"},
		// Уже в словаре — как есть.
		{"kubernetes", "kubernetes"},
		{"docker", "docker"},
		// Два кандидата в радиусе 1 («mongo», «mongi») — не рискуем.
		{"monga", "monga"},
		// Нет кандидата — как есть.
		{"cooberneties", "cooberneties"},
		{"zookeeper", "zookeeper"},
		// Короткие токены (< 5) не трогаются.
		{"redi", "redi"},
		{"gо", "gо"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := c.correctToken(tt.token); got != tt.want {
			t.Errorf("correctToken(%q) = %q, want %q", tt.token, got, tt.want)
		}
	}

	// Correct сохраняет длину и позиции.
	got := c.Correct([]string{"what", "is", "your", "kubernets", "experience"})
	want := []string{"what", "is", "your", "kubernetes", "experience"}
	if len(got) != len(want) {
		t.Fatalf("Correct: len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Correct[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// nil-корректор — no-op.
	var nilc *fuzzyCorrector
	in := []string{"kubernets"}
	if got := nilc.Correct(in); len(got) != 1 || got[0] != "kubernets" {
		t.Errorf("nil Correct = %v, want без изменений", got)
	}

	// Детерминизм: повторный вызов даёт тот же результат.
	a := c.correctToken("kubernets")
	b := c.correctToken("kubernets")
	if a != b {
		t.Errorf("недетерминизм: %q vs %q", a, b)
	}
}

// TestNewFuzzyCorrectorEmpty — пустой словарь даёт nil-корректор.
func TestNewFuzzyCorrectorEmpty(t *testing.T) {
	if got := newFuzzyCorrector(nil); got != nil {
		t.Errorf("newFuzzyCorrector(nil) = %+v, want nil", got)
	}
	if got := newFuzzyCorrector(map[string]struct{}{}); got != nil {
		t.Errorf("newFuzzyCorrector(пустой) = %+v, want nil", got)
	}
}

// TestRetrieverFuzzyCorrectionIntegration — вопрос со сбойным символом ASR
// находит факт с Kubernetes только при включённой коррекции; с
// DisableFuzzyCorrection — не находит.
func TestRetrieverFuzzyCorrectionIntegration(t *testing.T) {
	manifest := `{
  "profile": "Erik",
  "global_aliases": {},
  "sections": [{"id": "s1", "title": "Infrastructure"}]
}`
	marker := "<!-- fact\n{\n  \"id\": \"f1\",\n  \"section\": \"s1\"\n}\n-->"
	body := marker + `
I have run production Kubernetes clusters for three years, managing
deployments and horizontal scaling.
`
	dir := buildTree(t, manifest, map[string]string{"s1.md": body})
	manifestObj := mustLoadManifest(t, dir)
	index, err := BuildIndex(manifestObj, dir)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}

	// Вопрос с одиночной ASR-ошибкой в техническом термине: единственное
	// совпадение с фактом — через коррекцию «kubernets» → «kubernetes».
	typoQuery := "Tell me about your Kubernets?"

	withCorrection, err := NewRetrieverWithOptions(manifestObj, index, dir, RetrieverOptions{TopK: 3})
	if err != nil {
		t.Fatalf("NewRetrieverWithOptions: %v", err)
	}
	got := withCorrection.Retrieve(typoQuery)
	if len(got) == 0 || got[0].FactID != "f1" {
		t.Fatalf("с коррекцией ожидается факт f1, получено %+v", got)
	}

	without, err := NewRetrieverWithOptions(manifestObj, index, dir, RetrieverOptions{TopK: 3, DisableFuzzyCorrection: true})
	if err != nil {
		t.Fatalf("NewRetrieverWithOptions(disabled): %v", err)
	}
	if got := without.Retrieve(typoQuery); len(got) != 0 {
		t.Errorf("без коррекции ожидается пустая выдача, получено %+v", got)
	}

	// Чистый запрос не меняется от коррекции (не ломает существующий recall).
	clean := withCorrection.Retrieve("Tell me about your Kubernetes?")
	if len(clean) == 0 || clean[0].FactID != "f1" {
		t.Errorf("чистый запрос теряется при коррекции: %+v", clean)
	}

	// explain: корректированный токен виден в диагностировании.
	items := withCorrection.ExplainQuestion(typoQuery, RetrieveOptions{})
	if len(items) == 0 || !strings.Contains(strings.Join([]string{items[0].FactID}, ","), "f1") {
		t.Errorf("ExplainQuestion не видит факт при коррекции: %+v", items)
	}
}
