package candidatecontext

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testFact — минимальный JSON-маркер для BuildIndex.
type testFact struct {
	ID       string   `json:"id"`
	Section  string   `json:"section"`
	Title    string   `json:"title,omitempty"`
	Keywords []string `json:"keywords,omitempty"`
	Aliases  []string `json:"aliases,omitempty"`
}

func factJSON(f testFact) string {
	b, _ := json.Marshal(f) // struct literal — маршалинг не падает
	return "<!-- fact\n" + string(b) + "\n-->\n"
}

// newRetriever строит временное дерево (manifest.json + sections/*.md), строит
// index через BuildIndex и создаёт Retriever.
func newRetriever(t *testing.T, manifest string, files map[string]string, topK int, minScore float64) *Retriever {
	t.Helper()
	dir := buildTree(t, manifest, files)
	m := mustLoadManifest(t, dir)
	idx, err := BuildIndex(m, dir)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	r, err := NewRetriever(m, idx, dir, topK, minScore)
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}
	return r
}

func TestNewRetrieverErrors(t *testing.T) {
	if _, err := NewRetriever(nil, &Index{}, "", 0, 0); err == nil {
		t.Fatal("nil manifest: ожидалась ошибка")
	}
	if _, err := NewRetriever(&Manifest{}, nil, "", 0, 0); err == nil {
		t.Fatal("nil index: ожидалась ошибка")
	}
	bad := &Manifest{GlobalAliases: map[string][]string{"a": {""}}}
	if _, err := NewRetriever(bad, &Index{}, "", 0, 0); err == nil {
		t.Fatal("invalid alias: ожидалась ошибка")
	}
}

func TestRetrieveEmptyQuestion(t *testing.T) {
	r := newRetriever(t, manifestOneSection, map[string]string{
		"experience.md": factJSON(testFact{ID: "f1", Section: "experience"}) + "body\n",
	}, 0, 0)

	if got := r.Retrieve(""); got != nil {
		t.Fatalf("empty question: got %v, want nil", got)
	}
	if got := r.Retrieve("   \n\t  "); got != nil {
		t.Fatalf("whitespace question: got %v, want nil", got)
	}
	if got := r.Retrieve("the and of in a"); len(got) != 0 {
		t.Fatalf("stopword-only question: got %d results, want 0", len(got))
	}
}

func TestRetrieveExactTermMatch(t *testing.T) {
	r := newRetriever(t, manifestOneSection, map[string]string{
		"experience.md": factJSON(testFact{ID: "f1", Section: "experience", Keywords: []string{"pricing"}}) + "pricing engine body\n",
	}, 0, 0)

	got := r.Retrieve("pricing")
	if len(got) != 1 {
		t.Fatalf("got %d results, want 1", len(got))
	}
	if got[0].FactID != "f1" {
		t.Errorf("FactID: got %q, want f1", got[0].FactID)
	}
	if got[0].SectionID != "experience" {
		t.Errorf("SectionID: got %q, want experience", got[0].SectionID)
	}
	if got[0].Score <= 0 {
		t.Errorf("Score: got %v, want > 0", got[0].Score)
	}
	if got[0].Content != "pricing engine body" {
		t.Errorf("Content: got %q", got[0].Content)
	}
}

func TestRetrieveIDFDocumentFrequency(t *testing.T) {
	// "zebra" в 1 факте, "apple" в 2 фактах (N=3): rarer термин даёт больший IDF.
	files := map[string]string{
		"experience.md": factJSON(testFact{ID: "f1", Section: "experience", Keywords: []string{"zebra"}}) + "content one\n" +
			factJSON(testFact{ID: "f2", Section: "experience", Keywords: []string{"apple"}}) + "content two\n" +
			factJSON(testFact{ID: "f3", Section: "experience", Keywords: []string{"apple"}}) + "content three\n",
	}
	r := newRetriever(t, manifestOneSection, files, 0, 0)

	unique := r.Retrieve("zebra")
	shared := r.Retrieve("apple")
	if len(unique) != 1 || len(shared) != 2 {
		t.Fatalf("result counts: unique=%d, shared=%d", len(unique), len(shared))
	}
	if !(unique[0].Score > shared[0].Score) {
		t.Errorf("idf: rare-term score %v should exceed common-term score %v", unique[0].Score, shared[0].Score)
	}
}

func TestRetrieveNoTermFrequencyEffect(t *testing.T) {
	// f1: "widget" 1 раз, f2: "widget" 3 раза → одинаковый content-вклад.
	files := map[string]string{
		"experience.md": factJSON(testFact{ID: "f1", Section: "experience"}) + "widget\n" +
			factJSON(testFact{ID: "f2", Section: "experience"}) + "widget widget widget\n",
	}
	r := newRetriever(t, manifestOneSection, files, 0, 0)

	got := r.Retrieve("widget")
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2", len(got))
	}
	if got[0].Score != got[1].Score {
		t.Errorf("TF не должен влиять на score: %v vs %v", got[0].Score, got[1].Score)
	}
	if got[0].FactID != "f1" || got[1].FactID != "f2" {
		t.Errorf("tie order: got %q,%q want f1,f2", got[0].FactID, got[1].FactID)
	}
}

func TestRetrieveFieldWeights(t *testing.T) {
	// keyword(5) > title(4) > alias(3) > content(2) при одинаковом idf (df=4, N=4 → idf=1).
	files := map[string]string{
		"experience.md": factJSON(testFact{ID: "fkw", Section: "experience", Keywords: []string{"term"}}) + "kw body\n" +
			factJSON(testFact{ID: "ftitle", Section: "experience", Title: "term"}) + "title body\n" +
			factJSON(testFact{ID: "falias", Section: "experience", Aliases: []string{"term"}}) + "alias body\n" +
			factJSON(testFact{ID: "fcontent", Section: "experience"}) + "term content body\n",
	}
	r := newRetriever(t, manifestOneSection, files, 0, 0)

	got := r.Retrieve("term")
	want := []string{"fkw", "ftitle", "falias", "fcontent"}
	if len(got) != len(want) {
		t.Fatalf("got %d results, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].FactID != id {
			t.Errorf("order[%d]: got %q, want %q", i, got[i].FactID, id)
		}
	}
	for i := 1; i < len(got); i++ {
		if !(got[i-1].Score > got[i].Score) {
			t.Errorf("scores not strictly decreasing at %d: %v <= %v", i, got[i-1].Score, got[i].Score)
		}
	}
}

func TestRetrieveSectionBonus(t *testing.T) {
	manifest := `{
	  "profile": "Erik",
	  "global_aliases": {},
	  "sections": [
	    {"id": "tagged", "title": "Tagged", "tags": ["term"]},
	    {"id": "plain", "title": "Plain"}
	  ]
	}`
	files := map[string]string{
		"tagged.md": factJSON(testFact{ID: "f1", Section: "tagged", Keywords: []string{"term"}}) + "body one\n",
		"plain.md":  factJSON(testFact{ID: "f2", Section: "plain", Keywords: []string{"term"}}) + "body two\n",
	}
	r := newRetriever(t, manifest, files, 0, 0)

	got := r.Retrieve("term")
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2", len(got))
	}
	var f1, f2 float64
	for _, res := range got {
		switch res.FactID {
		case "f1":
			f1 = res.Score
		case "f2":
			f2 = res.Score
		}
	}
	if !(f1 > f2) {
		t.Errorf("section bonus: tagged fact %v should exceed plain %v", f1, f2)
	}
}

func TestRetrieveSectionSummaryBonus(t *testing.T) {
	manifest := `{
	  "profile": "Erik",
	  "global_aliases": {},
	  "sections": [
	    {"id": "sum", "title": "Sum", "summary": "term here"},
	    {"id": "plain", "title": "Plain"}
	  ]
	}`
	files := map[string]string{
		"sum.md":   factJSON(testFact{ID: "f1", Section: "sum", Keywords: []string{"term"}}) + "body one\n",
		"plain.md": factJSON(testFact{ID: "f2", Section: "plain", Keywords: []string{"term"}}) + "body two\n",
	}
	r := newRetriever(t, manifest, files, 0, 0)

	got := r.Retrieve("term")
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2", len(got))
	}
	var f1, f2 float64
	for _, res := range got {
		switch res.FactID {
		case "f1":
			f1 = res.Score
		case "f2":
			f2 = res.Score
		}
	}
	if !(f1 > f2) {
		t.Errorf("section summary bonus: fact %v should exceed plain %v", f1, f2)
	}
}

func TestRetrievePhraseBonus(t *testing.T) {
	t.Run("title", func(t *testing.T) {
		files := map[string]string{
			"experience.md": factJSON(testFact{ID: "f1", Section: "experience", Title: "alpha beta"}) + "zzz\n" +
				factJSON(testFact{ID: "f2", Section: "experience", Title: "beta alpha"}) + "zzz\n",
		}
		r := newRetriever(t, manifestOneSection, files, 0, 0)
		got := r.Retrieve("alpha beta")
		if len(got) != 2 {
			t.Fatalf("got %d results, want 2", len(got))
		}
		if got[0].FactID != "f1" || got[1].FactID != "f2" {
			t.Fatalf("order: got %q,%q want f1,f2", got[0].FactID, got[1].FactID)
		}
		if diff := got[0].Score - got[1].Score; math.Abs(diff-3.0) > 1e-9 {
			t.Errorf("title phrase bonus: diff %v, want 3", diff)
		}
	})
	t.Run("content", func(t *testing.T) {
		files := map[string]string{
			"experience.md": factJSON(testFact{ID: "f1", Section: "experience"}) + "alpha beta\n" +
				factJSON(testFact{ID: "f2", Section: "experience"}) + "beta alpha\n",
		}
		r := newRetriever(t, manifestOneSection, files, 0, 0)
		got := r.Retrieve("alpha beta")
		if len(got) != 2 {
			t.Fatalf("got %d results, want 2", len(got))
		}
		if got[0].FactID != "f1" || got[1].FactID != "f2" {
			t.Fatalf("order: got %q,%q want f1,f2", got[0].FactID, got[1].FactID)
		}
		if diff := got[0].Score - got[1].Score; math.Abs(diff-1.0) > 1e-9 {
			t.Errorf("content phrase bonus: diff %v, want 1", diff)
		}
	})
}

func TestRetrieveZeroScoreRejected(t *testing.T) {
	r := newRetriever(t, manifestOneSection, map[string]string{
		"experience.md": factJSON(testFact{ID: "f1", Section: "experience", Keywords: []string{"foo"}}) + "body\n",
	}, 0, 0)

	if got := r.Retrieve("unrelated terms here"); len(got) != 0 {
		t.Fatalf("no term overlap: got %d results, want 0", len(got))
	}
}

func TestRetrieveSectionMetadataDoesNotCreateMatch(t *testing.T) {
	// Термин запроса встречается ТОЛЬКО в section metadata (tags + summary),
	// но ни в одном fact-level поле → факт не должен попадать в выдачу.
	manifest := `{
	  "profile": "Erik",
	  "global_aliases": {},
	  "sections": [
	    {"id": "tagged", "title": "Tagged", "tags": ["zebra"], "summary": "zebra here"}
	  ]
	}`
	files := map[string]string{
		"tagged.md": factJSON(testFact{ID: "f1", Section: "tagged"}) + "unrelated body\n",
	}
	r := newRetriever(t, manifest, files, 0, 0)

	if got := r.Retrieve("zebra"); len(got) != 0 {
		t.Fatalf("section-only metadata match: got %d results, want 0", len(got))
	}
}

func TestRetrieveFactLevelBeatsSectionOnlyMatch(t *testing.T) {
	// fA совпадает только с section tags; fB — реальный fact-level match
	// (keywords). В выдачу должен попасть только fB.
	manifest := `{
	  "profile": "Erik",
	  "global_aliases": {},
	  "sections": [
	    {"id": "tagged", "title": "Tagged", "tags": ["widget"]},
	    {"id": "plain", "title": "Plain"}
	  ]
	}`
	files := map[string]string{
		"tagged.md": factJSON(testFact{ID: "fA", Section: "tagged"}) + "generic body\n",
		"plain.md":  factJSON(testFact{ID: "fB", Section: "plain", Keywords: []string{"widget"}}) + "widget body\n",
	}
	r := newRetriever(t, manifest, files, 0, 0)

	got := r.Retrieve("widget")
	if len(got) != 1 {
		t.Fatalf("got %d results, want 1 (only fact-level match)", len(got))
	}
	if got[0].FactID != "fB" {
		t.Errorf("FactID: got %q, want fB", got[0].FactID)
	}
}

func TestRetrievePhraseBonusFieldScope(t *testing.T) {
	// Phrase bonus начисляется только за биграммы в title/content, но не в
	// keywords/aliases. Проверяем разницу (равенство/неравенство score), а не
	// точные числа.
	t.Run("keywords_only_no_bonus", func(t *testing.T) {
		// Биграмма "alpha beta" подряд в keywords (f1) не должна давать bonus
		// относительно переставленной пары (f2) — score равны.
		files := map[string]string{
			"experience.md": factJSON(testFact{ID: "f1", Section: "experience", Keywords: []string{"alpha beta"}}) + "zzz\n" +
				factJSON(testFact{ID: "f2", Section: "experience", Keywords: []string{"beta alpha"}}) + "zzz\n",
		}
		r := newRetriever(t, manifestOneSection, files, 0, 0)
		got := r.Retrieve("alpha beta")
		if len(got) != 2 {
			t.Fatalf("got %d results, want 2", len(got))
		}
		if got[0].Score != got[1].Score {
			t.Errorf("phrase в keywords не должен давать bonus: %v vs %v", got[0].Score, got[1].Score)
		}
	})
	t.Run("aliases_only_no_bonus", func(t *testing.T) {
		files := map[string]string{
			"experience.md": factJSON(testFact{ID: "f1", Section: "experience", Aliases: []string{"alpha beta"}}) + "zzz\n" +
				factJSON(testFact{ID: "f2", Section: "experience", Aliases: []string{"beta alpha"}}) + "zzz\n",
		}
		r := newRetriever(t, manifestOneSection, files, 0, 0)
		got := r.Retrieve("alpha beta")
		if len(got) != 2 {
			t.Fatalf("got %d results, want 2", len(got))
		}
		if got[0].Score != got[1].Score {
			t.Errorf("phrase в aliases не должен давать bonus: %v vs %v", got[0].Score, got[1].Score)
		}
	})
	t.Run("cap", func(t *testing.T) {
		// f1: 2 title-биграммы (raw bonus = 6); f2: 3 title-биграммы (raw = 9).
		// С cap оба ограничиваются до 6 → score равны. Без cap f2 был бы выше.
		files := map[string]string{
			"experience.md": factJSON(testFact{ID: "f1", Section: "experience", Title: "x y"}) + "zzz\n" +
				factJSON(testFact{ID: "f2", Section: "experience", Title: "x y x y"}) + "zzz\n",
		}
		r := newRetriever(t, manifestOneSection, files, 0, 0)
		got := r.Retrieve("x y x y")
		if len(got) != 2 {
			t.Fatalf("got %d results, want 2", len(got))
		}
		if got[0].Score != got[1].Score {
			t.Errorf("cap должен выравнивать bonus сверх 6: %v vs %v", got[0].Score, got[1].Score)
		}
	})
	t.Run("lexical_without_phrase", func(t *testing.T) {
		// Термины совпадают только в keywords (без биграммы в title/content) —
		// факт всё равно возвращается обычным lexical match'ем.
		files := map[string]string{
			"experience.md": factJSON(testFact{ID: "f1", Section: "experience", Keywords: []string{"alpha", "beta"}}) + "zzz\n",
		}
		r := newRetriever(t, manifestOneSection, files, 0, 0)
		got := r.Retrieve("alpha beta")
		if len(got) != 1 || got[0].FactID != "f1" {
			t.Fatalf("lexical match без phrase: got %v, want [f1]", got)
		}
	})
}

func TestRetrieveMinScore(t *testing.T) {
	files := map[string]string{
		"experience.md": factJSON(testFact{ID: "fkw", Section: "experience", Keywords: []string{"term"}}) + "kw body\n" +
			factJSON(testFact{ID: "fcontent", Section: "experience"}) + "term content\n",
	}
	// df(term)=2, N=2 → idf=1 → scores: fkw=5, fcontent=2.

	r := newRetriever(t, manifestOneSection, files, 0, 3.0)
	got := r.Retrieve("term")
	if len(got) != 1 || got[0].FactID != "fkw" {
		t.Fatalf("minScore=3: got %v, want only fkw", got)
	}

	r0 := newRetriever(t, manifestOneSection, files, 0, 0)
	if got := r0.Retrieve("term"); len(got) != 2 {
		t.Fatalf("minScore=0: got %d, want 2", len(got))
	}

	rneg := newRetriever(t, manifestOneSection, files, 0, -1.0)
	if got := rneg.Retrieve("term"); len(got) != 2 {
		t.Fatalf("minScore<0 (as 0): got %d, want 2", len(got))
	}
}

func TestRetrieveTopK(t *testing.T) {
	files := map[string]string{
		"experience.md": factJSON(testFact{ID: "fkw", Section: "experience", Keywords: []string{"term"}}) + "kw body\n" +
			factJSON(testFact{ID: "ftitle", Section: "experience", Title: "term"}) + "title body\n" +
			factJSON(testFact{ID: "fcontent", Section: "experience"}) + "term content\n",
	}
	// df(term)=3, N=3 → idf=1 → scores: 5, 4, 2.

	r := newRetriever(t, manifestOneSection, files, 2, 0)
	got := r.Retrieve("term")
	if len(got) != 2 {
		t.Fatalf("topK=2: got %d, want 2", len(got))
	}
	if got[0].FactID != "fkw" || got[1].FactID != "ftitle" {
		t.Errorf("topK order: got %q,%q want fkw,ftitle", got[0].FactID, got[1].FactID)
	}

	r0 := newRetriever(t, manifestOneSection, files, 0, 0)
	if got := r0.Retrieve("term"); len(got) != 3 {
		t.Fatalf("topK=0 (unlimited): got %d, want 3", len(got))
	}
	rneg := newRetriever(t, manifestOneSection, files, -1, 0)
	if got := rneg.Retrieve("term"); len(got) != 3 {
		t.Fatalf("topK<0 (unlimited): got %d, want 3", len(got))
	}
}

func TestRetrieveTieBreakDeterministic(t *testing.T) {
	files := map[string]string{
		"experience.md": factJSON(testFact{ID: "zebra", Section: "experience"}) + "widget\n" +
			factJSON(testFact{ID: "apple", Section: "experience"}) + "widget\n",
	}
	r := newRetriever(t, manifestOneSection, files, 0, 0)

	got := r.Retrieve("widget")
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2", len(got))
	}
	if got[0].FactID != "apple" || got[1].FactID != "zebra" {
		t.Errorf("tie order: got %q,%q want apple,zebra", got[0].FactID, got[1].FactID)
	}
	if got[0].Score != got[1].Score {
		t.Errorf("expected equal scores, got %v vs %v", got[0].Score, got[1].Score)
	}
}

func TestRetrievePointReading(t *testing.T) {
	marker := factJSON(testFact{ID: "f1", Section: "experience", Keywords: []string{"term"}})
	body := "\n  hello   world  \n\n"
	files := map[string]string{
		"experience.md": "header ignored\n" + marker + body,
	}
	r := newRetriever(t, manifestOneSection, files, 0, 0)

	got := r.Retrieve("term")
	if len(got) != 1 {
		t.Fatalf("got %d results, want 1", len(got))
	}
	if got[0].Content != "hello   world" {
		t.Errorf("content: got %q, want %q", got[0].Content, "hello   world")
	}
	if strings.Contains(got[0].Content, "<!--") {
		t.Errorf("content contains marker: %q", got[0].Content)
	}
}

func TestRetrieveReadFailuresSkipped(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sections"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sections", "real.md"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := &Manifest{GlobalAliases: map[string][]string{}, Sections: []Section{{ID: "s"}}}
	idx := &Index{
		Version: IndexVersion,
		Facts: []Fact{
			{ID: "f1", Section: "s", File: "sections/missing.md", Start: 0, End: 5, Keywords: []string{"term"}},
			{ID: "f2", Section: "s", File: "sections/real.md", Start: 10, End: 5, Keywords: []string{"term"}},
		},
	}
	r, err := NewRetriever(m, idx, dir, 0, 0)
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}
	if got := r.Retrieve("term"); len(got) != 0 {
		t.Fatalf("unreadable facts must be skipped, got %d results", len(got))
	}
}

// --- unit tests for scoring helpers ---

func TestIDFValue(t *testing.T) {
	dfMap := map[string]int{"common": 2, "rare": 1}
	n := 3

	rare := idfValue(dfMap, n, "rare")
	common := idfValue(dfMap, n, "common")
	absent := idfValue(dfMap, n, "absent")

	if !(rare > common) {
		t.Errorf("rare (%v) should exceed common (%v)", rare, common)
	}
	if !(absent > rare) {
		t.Errorf("absent (df=0) (%v) should exceed rare (%v)", absent, rare)
	}
	if math.Abs(rare-(math.Log(4.0/2.0)+1)) > 1e-9 {
		t.Errorf("rare idf: got %v, want %v", rare, math.Log(4.0/2.0)+1)
	}
	if math.Abs(absent-(math.Log(4.0/1.0)+1)) > 1e-9 {
		t.Errorf("absent idf: got %v, want %v", absent, math.Log(4.0/1.0)+1)
	}
}

func TestSumIDFDedup(t *testing.T) {
	qSet := toSet([]string{"a", "a", "b"})
	field := toSet([]string{"a", "a", "a"})
	dfMap := map[string]int{"a": 1, "b": 2}
	n := 2

	got := sumIDF(qSet, field, dfMap, n)
	want := idfValue(dfMap, n, "a")
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("sumIDF dedup: got %v, want %v", got, want)
	}
}

func TestHasBigramAndBigramHits(t *testing.T) {
	tokens := []string{"x", "y", "z", "x", "y"}
	if !hasBigram(tokens, "x", "y") {
		t.Error("expected contiguous bigram x y to be found")
	}
	if hasBigram(tokens, "x", "z") {
		t.Error("non-contiguous pair x z must not match")
	}
	if hasBigram(tokens, "y", "x") {
		t.Error("reversed pair y x must not match")
	}
	// bigrams of question "x y x y": x y (i0), y x (i1), x y (i2).
	if hits := bigramHits(tokens, []string{"x", "y", "x", "y"}); hits != 2 {
		t.Errorf("bigramHits: got %d, want 2", hits)
	}
	if hits := bigramHits(tokens, []string{"x"}); hits != 0 {
		t.Errorf("bigramHits single token: got %d, want 0", hits)
	}
}

func TestPhraseBonusValue(t *testing.T) {
	title := []string{"x", "y", "z"}
	content := []string{"x", "y"}
	q := []string{"x", "y", "z"}

	// title: 2 bigram hits (x y, y z) → 6; content: 1 (x y) → +1 → 7 → cap 6.
	if got := phraseBonusValue(title, content, q); got != phraseBonusCap {
		t.Errorf("phrase bonus: got %v, want cap %v", got, phraseBonusCap)
	}
	if got := phraseBonusValue(title, content, []string{"x"}); got != 0 {
		t.Errorf("single-token question: got %v, want 0", got)
	}
	if got := phraseBonusValue(title, content, nil); got != 0 {
		t.Errorf("empty question: got %v, want 0", got)
	}
}
