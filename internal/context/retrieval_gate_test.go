package candidatecontext

import (
	"strings"
	"testing"
)

// Пороги M7a: content-only gate и relative threshold. Тесты весов/idf/порядка
// живут в retrieval_test.go и работают с выключенными порогами.

func TestContentGateRejectsBodyOnlyWeakMatch(t *testing.T) {
	// f1 совпал только телом и покрывает вопрос слабо (одно слово из двух),
	// f2 — тем же словом, но по keywords (осознанная разметка).
	files := map[string]string{
		"experience.md": factJSON(testFact{ID: "f1", Section: "experience"}) + "zebra only\n" +
			factJSON(testFact{ID: "f2", Section: "experience", Keywords: []string{"zebra"}}) + "nothing here\n",
	}
	r := newGatedRetriever(t, manifestOneSection, files, RetrieverOptions{ContentGateTau: 0.5})

	got := r.Retrieve("zebra quokka")
	ids := resultIDsOf(got)
	if len(ids) != 1 || ids[0] != "f2" {
		t.Fatalf("got %v, want [f2] (body-only кандидат отброшен гейтом)", ids)
	}

	// Отклонённый факт виден в explain с причиной.
	items := r.ExplainQuestion("zebra quokka", RetrieveOptions{})
	if len(items) != 2 {
		t.Fatalf("explain: got %d items, want 2", len(items))
	}
	byID := explainByID(items)
	if byID["f1"].Passed {
		t.Error("f1: explain должен отметить отклонение гейтом")
	}
	if !strings.Contains(byID["f1"].RejectedBy, "content_only") {
		t.Errorf("f1.RejectedBy = %q, want content_only…", byID["f1"].RejectedBy)
	}
	if byID["f1"].Breakdown.Gate == "" {
		t.Error("f1.Breakdown.Gate пуст — explain обязан показывать вердикт")
	}
	if !byID["f2"].Passed {
		t.Errorf("f2 отклонён (%s), хотя совпал по keywords", byID["f2"].RejectedBy)
	}
}

func TestContentGateKeepsStrongBodyMatch(t *testing.T) {
	// Кандидат без keywords, но покрывающий вопрос целиком, проходит.
	files := map[string]string{
		"experience.md": factJSON(testFact{ID: "f1", Section: "experience"}) + "kafka postgresql\n",
	}
	r := newGatedRetriever(t, manifestOneSection, files, RetrieverOptions{ContentGateTau: 0.9})

	got := r.Retrieve("kafka postgresql")
	if len(got) != 1 {
		t.Fatalf("got %d результатов, want 1 (ratio 1.0 ≥ tau)", len(got))
	}
	if got[0].Raw == 0 || got[0].Rank != got[0].Raw {
		t.Errorf("Raw=%v Rank=%v — без стек-множителя они совпадают", got[0].Raw, got[0].Rank)
	}
	if got[0].Breakdown != nil {
		t.Error("Breakdown заполнен без Explain — лишняя работа на прод-пути")
	}
}

func TestContentGateDisabledByNegativeTau(t *testing.T) {
	files := map[string]string{
		"experience.md": factJSON(testFact{ID: "f1", Section: "experience"}) + "zebra only\n",
	}
	r := newGatedRetriever(t, manifestOneSection, files, RetrieverOptions{ContentGateTau: -1})
	if got := r.Retrieve("zebra quokka"); len(got) != 1 {
		t.Fatalf("отрицательный tau должен выключать гейт, got %d", len(got))
	}
}

func TestRelativeThresholdDropsWeakCandidates(t *testing.T) {
	// fstrong: 3 совпадения по keywords; fweak: 1 совпадение по keywords.
	files := map[string]string{
		"experience.md": factJSON(testFact{ID: "fstrong", Section: "experience", Keywords: []string{"alpha", "beta", "gamma"}}) + "one\n" +
			factJSON(testFact{ID: "fweak", Section: "experience", Keywords: []string{"alpha"}}) + "two\n",
	}
	r := newGatedRetriever(t, manifestOneSection, files, RetrieverOptions{RelativeThreshold: 0.8})

	got := r.Retrieve("alpha beta gamma")
	if ids := resultIDsOf(got); len(ids) != 1 || ids[0] != "fstrong" {
		t.Fatalf("got %v, want [fstrong] (fweak ниже 0.8 × top1)", ids)
	}

	// Top-1 переживает порог по построению: его core — опорный.
	if got[0].Rank <= 0 {
		t.Errorf("rank top-1 = %v", got[0].Rank)
	}

	items := explainByID(r.ExplainQuestion("alpha beta gamma", RetrieveOptions{}))
	if items["fweak"].Passed {
		t.Error("fweak: explain должен показать отсечение относительным порогом")
	}
	if !strings.Contains(items["fweak"].RejectedBy, "relative") {
		t.Errorf("fweak.RejectedBy = %q, want relative…", items["fweak"].RejectedBy)
	}
}

func TestRelativeThresholdIgnoresSectionBonus(t *testing.T) {
	// fstrong — 2 совпадения по keywords в секции без тегов; fweak — 1 в секции,
	// теги и summary которой совпали с вопросом (section-бонус). Контракт M7a:
	// опора порога — core (без section-бонуса), поэтому бонус не вытягивает
	// слабый факт через порог, хотя и делает его видимым.
	manifest := `{
	  "profile": "Erik",
	  "global_aliases": {},
	  "sections": [
	    {"id": "tagged", "title": "Tagged", "tags": ["alpha", "beta"], "summary": "alpha beta"},
	    {"id": "plain", "title": "Plain"}
	  ]
	}`
	files := map[string]string{
		"tagged.md": factJSON(testFact{ID: "fweak", Section: "tagged", Keywords: []string{"alpha"}}) + "one\n",
		"plain.md":  factJSON(testFact{ID: "fstrong", Section: "plain", Keywords: []string{"alpha", "beta"}}) + "two\n",
	}
	r := newGatedRetriever(t, manifest, files, RetrieverOptions{RelativeThreshold: 0.6})

	items := explainByID(r.ExplainQuestion("alpha beta", RetrieveOptions{}))
	strong, weak := items["fstrong"].Breakdown, items["fweak"].Breakdown
	if weak.Core >= strong.Core {
		t.Fatalf("фикстура неверна: weak.Core=%.3f должен быть меньше strong.Core=%.3f", weak.Core, strong.Core)
	}
	if weak.Section <= 0 {
		t.Fatalf("фикстура неверна: section-бонус не применён (%.3f)", weak.Section)
	}
	if items["fweak"].Passed {
		t.Errorf("fweak прошёл порог: core=%.3f, порог %.3f (rank с бонусом %.3f)",
			weak.Core, strong.Core*0.6, weak.Rank)
	}
	if !items["fstrong"].Passed {
		t.Errorf("fstrong отклонён: %s", items["fstrong"].RejectedBy)
	}
	// Порог считается от core: 0.6 × strong.Core > weak.Core.
	if weak.Core >= strong.Core*0.6 {
		t.Errorf("weak.Core=%.3f не ниже порога %.3f", weak.Core, strong.Core*0.6)
	}
}

func TestRelativeThresholdDisabledByNegativeTau(t *testing.T) {
	files := map[string]string{
		"experience.md": factJSON(testFact{ID: "fstrong", Section: "experience", Keywords: []string{"alpha", "beta"}}) + "one\n" +
			factJSON(testFact{ID: "fweak", Section: "experience", Keywords: []string{"alpha"}}) + "two\n",
	}
	r := newGatedRetriever(t, manifestOneSection, files, RetrieverOptions{RelativeThreshold: -1})
	if got := r.Retrieve("alpha beta"); len(got) != 2 {
		t.Fatalf("отрицательный порог должен выключать фильтр, got %d", len(got))
	}
}

func TestRetrieveEmptyResultIsNotError(t *testing.T) {
	// Вопрос без совпадений — штатная пустая выдача (не ошибка и не паника).
	files := map[string]string{
		"experience.md": factJSON(testFact{ID: "f1", Section: "experience", Keywords: []string{"alpha"}}) + "body\n",
	}
	r := newGatedRetriever(t, manifestOneSection, files, RetrieverOptions{})

	if got := r.Retrieve("совершенно посторонний вопрос quokka"); len(got) != 0 {
		t.Fatalf("got %d результатов, want 0", len(got))
	}
	// Пустой вопрос → nil, без паники.
	if got := r.Retrieve("   "); got != nil {
		t.Fatalf("пустой вопрос: got %v, want nil", got)
	}
	if items := r.ExplainQuestion("   ", RetrieveOptions{}); items != nil {
		t.Fatalf("explain пустого вопроса: got %v, want nil", items)
	}
}

func TestExplainBreakdownComponentsSumToRanks(t *testing.T) {
	files := map[string]string{
		"experience.md": factJSON(testFact{ID: "f1", Section: "experience", Keywords: []string{"alpha"}, Aliases: []string{"beta"}, Title: "gamma delta"}) + "epsilon zeta\n",
	}
	r := newGatedRetriever(t, manifestOneSection, files, RetrieverOptions{ContentGateTau: -1, RelativeThreshold: -1})

	items := r.ExplainQuestion("alpha beta gamma delta epsilon zeta", RetrieveOptions{})
	if len(items) != 1 {
		t.Fatalf("explain: got %d items, want 1", len(items))
	}
	b := items[0].Breakdown
	if got, want := b.FieldScore, b.Keywords+b.Title+b.Aliases+b.Content; !nearlyEqual(got, want) {
		t.Errorf("FieldScore=%v, want сумма вкладов %v", got, want)
	}
	if got, want := b.Raw, b.FieldScore+b.Section+b.Phrase; !nearlyEqual(got, want) {
		t.Errorf("Raw=%v, want %v", got, want)
	}
	if got, want := b.Core, b.FieldScore+b.Phrase; !nearlyEqual(got, want) {
		t.Errorf("Core=%v, want %v (mult=1 в M7a)", got, want)
	}
	if got, want := b.Rank, b.Core+b.Section; !nearlyEqual(got, want) {
		t.Errorf("Rank=%v, want %v", got, want)
	}
	if b.Mult != 1 {
		t.Errorf("Mult=%v, want 1 без стек-множителей", b.Mult)
	}
}

// explainByID индексирует explain-элементы по FactID.
func explainByID(items []ExplainItem) map[string]ExplainItem {
	out := make(map[string]ExplainItem, len(items))
	for _, it := range items {
		out[it.FactID] = it
	}
	return out
}

// resultIDsOf возвращает FactID результатов в порядке выдачи.
func resultIDsOf(results []RetrievalResult) []string {
	ids := make([]string, 0, len(results))
	for _, r := range results {
		ids = append(ids, r.FactID)
	}
	return ids
}

// nearlyEqual сравнивает float64 с точностью, достаточной для сумм idf.
func nearlyEqual(a, b float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < 1e-9
}
