package candidatecontext

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mastererik/translator/internal/context/stackdet"
)

func TestManifestJSONRoundTrip(t *testing.T) {
	src := Manifest{
		Profile: "Erik Ivanov — Staff Software Engineer",
		GlobalAliases: map[string][]string{
			"kubernetes": {"k8s", "kube"},
			"postgresql": {"postgres", "pg"},
		},
		Sections: []Section{
			{ID: "experience", Title: "Опыт", Summary: "Опыт работы", Tags: []string{"java", "kafka"}},
		},
	}
	data, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	var got Manifest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if got.Profile != src.Profile {
		t.Errorf("Profile: got %q, want %q", got.Profile, src.Profile)
	}
	if len(got.GlobalAliases) != 2 || len(got.GlobalAliases["kubernetes"]) != 2 {
		t.Errorf("GlobalAliases round-trip failed: %v", got.GlobalAliases)
	}
	if len(got.Sections) != 1 || got.Sections[0].ID != "experience" {
		t.Errorf("Sections round-trip failed: %v", got.Sections)
	}
}

func TestIndexJSONRoundTrip(t *testing.T) {
	src := Index{
		Version:        IndexVersion,
		ManifestSHA256: "abc123",
		Files: []FileMeta{
			{Path: "sections/experience.md", Size: 1234, SHA256: "deadbeef"},
		},
		Facts: []Fact{
			{
				ID: "java-head-pricing", Section: "experience",
				File: "sections/experience.md", Start: 10, End: 20,
				Title: "Pricing Engine", Keywords: []string{"pricing", "kafka"},
				Aliases: []string{"price engine"}, ContentTokens: []string{"pricing", "engine"},
				Stack: []string{"java"}, Period: "2019-2022", Weight: "high",
			},
		},
	}
	data, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("marshal index: %v", err)
	}
	var got Index
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal index: %v", err)
	}
	if got.Version != src.Version || got.ManifestSHA256 != src.ManifestSHA256 {
		t.Errorf("index header round-trip failed: %+v", got)
	}
	if len(got.Files) != 1 || got.Files[0].Size != 1234 {
		t.Errorf("files round-trip failed: %v", got.Files)
	}
	if len(got.Facts) != 1 || got.Facts[0].Start != 10 || got.Facts[0].End != 20 {
		t.Errorf("facts round-trip failed: %v", got.Facts)
	}
	if !reflect.DeepEqual(got.Facts[0].Stack, []string{"java"}) ||
		got.Facts[0].Period != "2019-2022" || got.Facts[0].Weight != "high" {
		t.Errorf("stack/period/weight round-trip failed: %+v", got.Facts[0])
	}
}

// TestFactStackPeriodWeightOmitEmpty фиксирует формат индекса: у фактов без
// stack/period/weight поля не появляются в JSON (совместимость с прежним
// форматом на уровне отдельных фактов).
func TestFactStackPeriodWeightOmitEmpty(t *testing.T) {
	data, err := json.Marshal(Fact{ID: "f1", Section: "experience"})
	if err != nil {
		t.Fatalf("marshal fact: %v", err)
	}
	for _, key := range []string{"\"stack\"", "\"period\"", "\"weight\""} {
		if strings.Contains(string(data), key) {
			t.Errorf("пустое поле %s не должно попадать в JSON: %s", key, data)
		}
	}
}

func TestManifestTechFamiliesAndMatrixRoundTrip(t *testing.T) {
	src := Manifest{
		Profile: "p",
		TechFamilies: []stackdet.Family{
			{Name: "golang", Display: "Go", Members: []string{"golang", "gopher"}, Ambiguous: []string{"go"}},
		},
		TechnicalMatrix: "stack | period\ngolang | 2021-2024",
	}
	data, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if !strings.Contains(string(data), "\"tech_families\"") || !strings.Contains(string(data), "\"technical_matrix\"") {
		t.Fatalf("новые поля не сериализуются: %s", data)
	}
	var got Manifest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if len(got.TechFamilies) != 1 {
		t.Fatalf("TechFamilies: got %+v", got.TechFamilies)
	}
	fam := got.TechFamilies[0]
	if fam.Name != "golang" || fam.Display != "Go" ||
		!reflect.DeepEqual(fam.Members, []string{"golang", "gopher"}) ||
		!reflect.DeepEqual(fam.Ambiguous, []string{"go"}) {
		t.Errorf("Family round-trip failed: %+v", fam)
	}
	if got.TechnicalMatrix != src.TechnicalMatrix {
		t.Errorf("TechnicalMatrix: got %q", got.TechnicalMatrix)
	}
}

func TestFactOffsetsAreInt64(t *testing.T) {
	// Start/End объявлены как int64 — проверяем, что большие значения не обрезаются.
	f := Fact{Start: 1<<40 + 5, End: 1<<40 + 100}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal fact: %v", err)
	}
	var got Fact
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal fact: %v", err)
	}
	if got.Start != f.Start || got.End != f.End {
		t.Errorf("int64 offsets corrupted: got start=%d end=%d, want start=%d end=%d",
			got.Start, got.End, f.Start, f.End)
	}
}

func TestCandidateContextRender(t *testing.T) {
	cc := CandidateContext{
		Profile: "Erik Ivanov",
		Facts: []RetrievalResult{
			{FactID: "a", SectionID: "exp", Score: 3.0, Content: "Pricing Engine with Kafka."},
			{FactID: "b", SectionID: "exp", Score: 2.0, Content: "PostgreSQL sharding."},
			{FactID: "empty", SectionID: "exp", Score: 1.0, Content: ""},
		},
	}
	out := cc.Render()
	if !strings.Contains(out, "Erik Ivanov") {
		t.Errorf("Render: profile missing from %q", out)
	}
	if !strings.Contains(out, "Pricing Engine with Kafka.") {
		t.Errorf("Render: fact a missing from %q", out)
	}
	if !strings.Contains(out, "PostgreSQL sharding.") {
		t.Errorf("Render: fact b missing from %q", out)
	}
	// Факт с пустым Content не должен порождать пустых блоков.
	if strings.Contains(out, "\n\n\n") {
		t.Errorf("Render: пустой факт породил лишние переводы строк: %q", out)
	}
}

func TestCandidateContextRenderEmpty(t *testing.T) {
	if got := (CandidateContext{}).Render(); got != "" {
		t.Errorf("Render empty: got %q, want empty", got)
	}
}

// TestCandidateContextRenderWithMatrix фиксирует M4: матрица рендерится
// отдельным блоком между профилем и фактами и не теряется.
func TestCandidateContextRenderWithMatrix(t *testing.T) {
	cc := CandidateContext{
		Profile: "Erik Ivanov",
		Matrix:  "stack | period\ngolang | 2021-2024",
		Facts:   []RetrievalResult{{FactID: "a", Content: "Kafka pricing."}},
	}
	out := cc.Render()
	profileIdx := strings.Index(out, "Candidate profile:")
	matrixIdx := strings.Index(out, "Technical matrix:")
	factIdx := strings.Index(out, "Kafka pricing.")
	if profileIdx < 0 || matrixIdx < 0 || factIdx < 0 {
		t.Fatalf("Render: блоки потеряны: %q", out)
	}
	if !(profileIdx < matrixIdx && matrixIdx < factIdx) {
		t.Errorf("Render: порядок блоков профиль→матрица→факты нарушен: %q", out)
	}
}

// TestCandidateContextRenderMatrixOnly проверяет, что матрица рендерится и без
// профиля (нет ведущих переводов строк).
func TestCandidateContextRenderMatrixOnly(t *testing.T) {
	out := (CandidateContext{Matrix: "golang | 2021"}).Render()
	if !strings.HasPrefix(out, "Technical matrix:") {
		t.Errorf("Render: got %q", out)
	}
}
