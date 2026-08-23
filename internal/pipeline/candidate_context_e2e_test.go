package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	candidatecontext "github.com/mastererik/translator/internal/context"
)

// writeE2ECandidateContextDir создаёт структуру candidate_context с профилем и
// ДВУМЯ фактами в одной секции: relevant (pricing engine / kafka) и irrelevant
// (хобби). Перед первым маркером идёт строка-«служебная база», которая не
// должна утекать в prompt-ready вывод.
func writeE2ECandidateContextDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sections"), 0o755); err != nil {
		t.Fatalf("mkdir sections: %v", err)
	}

	manifest := `{
  "profile": "Erik Ivanov — senior Go engineer, 8 years",
  "global_aliases": {},
  "sections": [{"id": "experience", "title": "Опыт", "summary": "Профессиональный опыт", "tags": ["go", "backend"]}]
}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest.json: %v", err)
	}

	// Промпт-готовый контент факта — ТОЛЬКО тело после маркера. Строка до
	// первого маркера — «служебная база», которая не должна попасть в вывод.
	section := `PRIVATE DATABASE — DO NOT LEAK
<!-- fact
{
  "id": "pricing-engine",
  "section": "experience",
  "title": "Pricing Engine",
  "keywords": ["pricing", "kafka"]
}
-->
Built a real-time pricing engine in Go using Kafka for event streaming.

<!-- fact
{
  "id": "hobby-photography",
  "section": "experience",
  "title": "Photography Hobby",
  "keywords": ["photography", "hiking"]
}
-->
In my free time I enjoy hiking and landscape photography.
`
	if err := os.WriteFile(filepath.Join(dir, "sections", "experience.md"), []byte(section), 0o644); err != nil {
		t.Fatalf("write experience.md: %v", err)
	}
	return dir
}

// TestCandidateContextE2E — сквозной путь fact-level retrieval:
// LoadCandidateContext → NewRetriever → NewBudgeter → Budget → Render.
// Проверяет, что в prompt-ready строку попадают только профиль и relevant факт,
// а marker-синтаксис, retrieval-интерналы и посторонний текст не утекают.
func TestCandidateContextE2E(t *testing.T) {
	dir := writeE2ECandidateContextDir(t)

	const (
		topK             = 5
		minScore         = 0.0
		maxTokens        = 2000
		maxProfileTokens = 150
	)

	manifest, index, err := candidatecontext.LoadCandidateContext(dir)
	if err != nil {
		t.Fatalf("LoadCandidateContext: %v", err)
	}

	retriever, err := candidatecontext.NewRetriever(manifest, index, dir, topK, minScore)
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}

	budgeter := candidatecontext.NewBudgeter(maxTokens, maxProfileTokens)
	rendered := budgeter.Budget(manifest.Profile, retriever.Retrieve("pricing engine kafka")).Render()

	// Профиль должен присутствовать.
	if !strings.Contains(rendered, "Erik Ivanov") {
		t.Errorf("rendered должен содержать профиль, got:\n%s", rendered)
	}

	// Relevant факт должен присутствовать.
	if !strings.Contains(rendered, "pricing engine") {
		t.Errorf("rendered должен содержать relevant факт, got:\n%s", rendered)
	}

	// Irrelevant факт не должен попасть в вывод.
	if strings.Contains(rendered, "photography") || strings.Contains(rendered, "hiking") {
		t.Errorf("rendered не должен содержать irrelevant факт, got:\n%s", rendered)
	}

	// Marker-синтаксис не должен утекать.
	if strings.Contains(rendered, "<!--") {
		t.Errorf("rendered не должен содержать marker-синтаксис «<!--», got:\n%s", rendered)
	}

	// Retrieval/index/manifest internals не должны утекать.
	for _, internal := range []string{
		"manifest_sha256",
		"content_tokens",
		`"score"`,
		`"fact_id"`,
		`"section_id"`,
		`"keywords"`,
	} {
		if strings.Contains(rendered, internal) {
			t.Errorf("rendered не должен содержать internal %q, got:\n%s", internal, rendered)
		}
	}

	// Посторонний текст вне фактов (raw source database) не должен утекать.
	if strings.Contains(rendered, "PRIVATE DATABASE") {
		t.Errorf("rendered не должен содержать служебную строку raw source, got:\n%s", rendered)
	}
}
