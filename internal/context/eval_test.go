package candidatecontext

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newSyntheticRetriever builds a Retriever over a hand-constructed Index with 5
// facts, backed by a real temporary source file so that Retrieve can point-read
// fact bodies. Each fact carries one unique keyword ("alpha".."epsilon"), which
// gives full control over retrieval order: a question listing several keywords
// returns the matching facts in FactID ascending order (equal scores).
func newSyntheticRetriever(t *testing.T, topK int, minScore float64) *Retriever {
	t.Helper()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sections"), 0o755); err != nil {
		t.Fatalf("mkdir sections: %v", err)
	}

	// Write one section file with five fact bodies, tracking byte offsets so the
	// synthetic index can point-read each body.
	bodies := []string{"content one", "content two", "content three", "content four", "content five"}
	var sb strings.Builder
	starts := make([]int64, len(bodies))
	ends := make([]int64, len(bodies))
	for i, b := range bodies {
		starts[i] = int64(sb.Len())
		sb.WriteString(b)
		sb.WriteString("\n")
		ends[i] = int64(sb.Len())
	}
	if err := os.WriteFile(filepath.Join(dir, "sections", "s.md"), []byte(sb.String()), 0o644); err != nil {
		t.Fatalf("write section file: %v", err)
	}

	keywords := []string{"alpha", "beta", "gamma", "delta", "epsilon"}
	facts := make([]Fact, len(keywords))
	for i, kw := range keywords {
		facts[i] = Fact{
			ID:            fmt.Sprintf("f%d", i+1),
			Section:       "s",
			File:          "sections/s.md",
			Start:         starts[i],
			End:           ends[i],
			Keywords:      []string{kw},
			ContentTokens: []string{"filler"}, // neutral: never matches query terms
		}
	}

	manifest := &Manifest{
		GlobalAliases: map[string][]string{},
		Sections:      []Section{{ID: "s"}},
	}
	index := &Index{Version: IndexVersion, Facts: facts}

	r, err := NewRetriever(manifest, index, dir, topK, minScore)
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}
	return r
}

func almostEqual(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s: got %.6f, want %.6f", name, got, want)
	}
}

func TestEvalEvaluateKnownScenario(t *testing.T) {
	r := newSyntheticRetriever(t, 5, 0)

	ds := &EvalDataset{Queries: []EvalQuery{
		{Question: "alpha", RelevantFactIDs: []string{"f1"}},                  // hit at rank 1
		{Question: "alpha beta", RelevantFactIDs: []string{"f2"}},             // hit at rank 2
		{Question: "alpha beta gamma", RelevantFactIDs: []string{"f2", "f3"}}, // first hit at rank 2
		{Question: "alpha", RelevantFactIDs: []string{"missing"}},             // no hit
	}}

	m := Evaluate(r, ds)

	// Recall@1: only query 1 hits top-1 → 1/4.
	almostEqual(t, "Recall@1", m.RecallAt1, 0.25)
	// Recall@3: queries 1,2,3 hit within top-3 → 3/4.
	almostEqual(t, "Recall@3", m.RecallAt3, 0.75)
	// Recall@5: queries 1,2,3 hit within top-5 → 3/4.
	almostEqual(t, "Recall@5", m.RecallAt5, 0.75)
	// MRR: (1 + 1/2 + 1/2 + 0) / 4 = 0.5.
	almostEqual(t, "MRR", m.MRR, 0.5)
}

func TestEvalEvaluateSingleQueryPrecise(t *testing.T) {
	r := newSyntheticRetriever(t, 5, 0)

	// Full hit at rank 1.
	m := Evaluate(r, &EvalDataset{Queries: []EvalQuery{
		{Question: "epsilon", RelevantFactIDs: []string{"f5"}},
	}})
	almostEqual(t, "Recall@1 (rank1)", m.RecallAt1, 1.0)
	almostEqual(t, "Recall@3 (rank1)", m.RecallAt3, 1.0)
	almostEqual(t, "Recall@5 (rank1)", m.RecallAt5, 1.0)
	almostEqual(t, "MRR (rank1)", m.MRR, 1.0)

	// Relevant fact at rank 2 (first result is an irrelevant one).
	m = Evaluate(r, &EvalDataset{Queries: []EvalQuery{
		{Question: "alpha beta", RelevantFactIDs: []string{"f2"}},
	}})
	almostEqual(t, "Recall@1 (rank2)", m.RecallAt1, 0.0)
	almostEqual(t, "Recall@3 (rank2)", m.RecallAt3, 1.0)
	almostEqual(t, "Recall@5 (rank2)", m.RecallAt5, 1.0)
	almostEqual(t, "MRR (rank2)", m.MRR, 0.5)

	// Complete miss.
	m = Evaluate(r, &EvalDataset{Queries: []EvalQuery{
		{Question: "alpha", RelevantFactIDs: []string{"nope"}},
	}})
	almostEqual(t, "Recall@1 (miss)", m.RecallAt1, 0.0)
	almostEqual(t, "Recall@3 (miss)", m.RecallAt3, 0.0)
	almostEqual(t, "Recall@5 (miss)", m.RecallAt5, 0.0)
	almostEqual(t, "MRR (miss)", m.MRR, 0.0)
}

func TestEvalEvaluateEmptyDataset(t *testing.T) {
	r := newSyntheticRetriever(t, 5, 0)

	m := Evaluate(r, &EvalDataset{})
	almostEqual(t, "Recall@1", m.RecallAt1, 0.0)
	almostEqual(t, "Recall@3", m.RecallAt3, 0.0)
	almostEqual(t, "Recall@5", m.RecallAt5, 0.0)
	almostEqual(t, "MRR", m.MRR, 0.0)
}

func TestEvalEvaluateNilInputs(t *testing.T) {
	r := newSyntheticRetriever(t, 5, 0)

	m := Evaluate(nil, &EvalDataset{Queries: []EvalQuery{{Question: "alpha"}}})
	almostEqual(t, "nil retriever MRR", m.MRR, 0.0)

	m = Evaluate(r, nil)
	almostEqual(t, "nil dataset MRR", m.MRR, 0.0)
}

func TestEvalLoadDatasetValid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dataset.json")
	content := `{"queries":[
		{"question":"alpha","relevant_fact_ids":["f1"]},
		{"question":"alpha beta","relevant_fact_ids":["f2","f3"]}
	]}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write dataset: %v", err)
	}

	ds, err := LoadEvalDataset(path)
	if err != nil {
		t.Fatalf("LoadEvalDataset: %v", err)
	}
	if len(ds.Queries) != 2 {
		t.Fatalf("queries: got %d, want 2", len(ds.Queries))
	}
	if ds.Queries[0].Question != "alpha" {
		t.Errorf("question[0]: got %q, want %q", ds.Queries[0].Question, "alpha")
	}
	if len(ds.Queries[0].RelevantFactIDs) != 1 || ds.Queries[0].RelevantFactIDs[0] != "f1" {
		t.Errorf("relevant[0]: got %v, want [f1]", ds.Queries[0].RelevantFactIDs)
	}
	if len(ds.Queries[1].RelevantFactIDs) != 2 {
		t.Errorf("relevant[1]: got %v, want 2 ids", ds.Queries[1].RelevantFactIDs)
	}
}

func TestEvalLoadDatasetInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dataset.json")
	if err := os.WriteFile(path, []byte(`{not json`), 0o644); err != nil {
		t.Fatalf("write dataset: %v", err)
	}
	if _, err := LoadEvalDataset(path); err == nil {
		t.Fatal("LoadEvalDataset: expected error for invalid JSON")
	}
}

func TestEvalLoadDatasetMissing(t *testing.T) {
	if _, err := LoadEvalDataset(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("LoadEvalDataset: expected error for missing file")
	}
}
