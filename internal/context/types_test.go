package candidatecontext

import (
	"encoding/json"
	"strings"
	"testing"
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
