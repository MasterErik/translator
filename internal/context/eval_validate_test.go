package candidatecontext

// M1 плана: валидатор разметки. Проверки — про данные, а не про ранжирование:
// падать должен любой набор, по которому нельзя честно посчитать метрики.

import (
	"path/filepath"
	"strings"
	"testing"
)

// validV2Query — кейс, проходящий все проверки v2; тесты портят в нём ровно
// одно поле, чтобы падение было однозначно приписано этому полю.
func validV2Query() EvalQuery {
	return EvalQuery{
		ID:                    "q001",
		Question:              "How do you guarantee data consistency across services?",
		RelevantFactIDs:       []string{"skills-streaming"},
		MustNotFactIDs:        []string{"exp-software-engineer-web"},
		Category:              "negative",
		Tags:                  []string{"stack-penalty"},
		Lang:                  "en",
		Source:                "synthetic:M1-валидатор",
		Split:                 "train",
		SessionTargetStack:    []string{"java"},
		ExpectEffectiveTarget: []string{"java"},
	}
}

// validateV2 прогоняет один кейс через валидатор v2-режима.
func validateV2(q EvalQuery) []error {
	ds := &EvalDataset{DatasetVersion: 2, Queries: []EvalQuery{q}}
	return ValidateEvalDataset(ds, ValidateOptions{
		Split:        "train",
		KnownFactIDs: map[string]bool{"skills-streaming": true, "exp-software-engineer-web": true, "alarm-qa-01": true},
		RequireV2:    true,
	})
}

// TestValidateEvalDatasetV2Valid — корректный v2-кейс проходит.
func TestValidateEvalDatasetV2Valid(t *testing.T) {
	if errs := validateV2(validV2Query()); len(errs) != 0 {
		t.Fatalf("ожидался валидный кейс, получены ошибки: %v", errs)
	}
}

// TestValidateEvalDatasetV2Problems — каждое нарушение ловится отдельной
// проверкой (включая те, что JSON Schema выразить не может).
func TestValidateEvalDatasetV2Problems(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*EvalQuery)
		wantSubstr string
	}{
		{name: "пустой id", mutate: func(q *EvalQuery) { q.ID = "" }, wantSubstr: "обязательное поле id пусто"},
		{name: "пустой question", mutate: func(q *EvalQuery) { q.Question = "  " }, wantSubstr: "пустой question"},
		{name: "неизвестный FactID", mutate: func(q *EvalQuery) { q.RelevantFactIDs = []string{"nope"} }, wantSubstr: "отсутствует в индексах"},
		{name: "ID и в relevant, и в must_not", mutate: func(q *EvalQuery) { q.MustNotFactIDs = []string{"skills-streaming"} }, wantSubstr: "одновременно в relevant и must_not"},
		{name: "bad lang", mutate: func(q *EvalQuery) { q.Lang = "de" }, wantSubstr: "недопустимый lang"},
		{name: "bad category", mutate: func(q *EvalQuery) { q.Category = "other" }, wantSubstr: "недопустимая category"},
		{name: "bad source", mutate: func(q *EvalQuery) { q.Source = "from memory" }, wantSubstr: "не соответствует схеме"},
		{name: "bad split", mutate: func(q *EvalQuery) { q.Split = "test" }, wantSubstr: "недопустимый split"},
		{name: "split файла", mutate: func(q *EvalQuery) { q.Split = "holdout" }, wantSubstr: "не совпадает с назначением файла"},
		{name: "negative без must_not", mutate: func(q *EvalQuery) { q.MustNotFactIDs = nil }, wantSubstr: "требует непустой must_not"},
		{name: "пустой relevant без no_relevant", mutate: func(q *EvalQuery) { q.RelevantFactIDs = nil }, wantSubstr: "требует тег no_relevant"},
		{name: "no_relevant с разметкой", mutate: func(q *EvalQuery) { q.Tags = []string{TagNoRelevant} }, wantSubstr: "требует пустой relevant"},
		{name: "expect_effective_target в верхнем регистре", mutate: func(q *EvalQuery) { q.ExpectEffectiveTarget = []string{"Java"} }, wantSubstr: "должен быть в нижнем регистре"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := validV2Query()
			tc.mutate(&q)
			errs := validateV2(q)
			if len(errs) == 0 {
				t.Fatalf("ожидалась ошибка, набор принят как валидный")
			}
			var found bool
			for _, err := range errs {
				if strings.Contains(err.Error(), tc.wantSubstr) {
					found = true
				}
			}
			if !found {
				t.Errorf("ожидалась ошибка со словами %q, получено: %v", tc.wantSubstr, errs)
			}
		})
	}
}

// TestValidateEvalDatasetDuplicateIDs — дубли id делают разметку неоднозначной.
func TestValidateEvalDatasetDuplicateIDs(t *testing.T) {
	q := validV2Query()
	q2 := q
	q2.Question = "другой вопрос"
	ds := &EvalDataset{Queries: []EvalQuery{q, q2}}

	errs := ValidateEvalDataset(ds, ValidateOptions{
		Split:        "train",
		KnownFactIDs: map[string]bool{"skills-streaming": true, "exp-software-engineer-web": true},
		RequireV2:    true,
	})
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "дублирующийся id") {
		t.Fatalf("ожидалась ошибка про дубль id, получено: %v", errs)
	}
}

// TestValidateEvalDatasetLegacy — legacy (frozen) набор проверяется без v2-полей:
// нет id/lang/source/split — это не ошибка, но битая разметка всё равно падает.
func TestValidateEvalDatasetLegacy(t *testing.T) {
	ds := &EvalDataset{
		Frozen: true,
		Queries: []EvalQuery{
			{Question: "What is your experience with Apache Kafka?", RelevantFactIDs: []string{"skills-streaming"}},
		},
	}
	errs := ValidateEvalDataset(ds, ValidateOptions{
		KnownFactIDs: map[string]bool{"skills-streaming": true},
	})
	if len(errs) != 0 {
		t.Fatalf("legacy-кейс должен проходить: %v", errs)
	}

	ds.Queries[0].RelevantFactIDs = []string{"skills-streaming", "missing-fact"}
	errs = ValidateEvalDataset(ds, ValidateOptions{
		KnownFactIDs: map[string]bool{"skills-streaming": true},
	})
	if len(errs) == 0 {
		t.Fatal("битый FactID в legacy-наборе должен ловиться")
	}
}

// TestValidateEvalDatasetEmpty — пустой набор невалиден (нулевая знаменатель).
func TestValidateEvalDatasetEmpty(t *testing.T) {
	if errs := ValidateEvalDataset(&EvalDataset{}, ValidateOptions{}); len(errs) == 0 {
		t.Fatal("пустой набор должен падать")
	}
	if errs := ValidateEvalDataset(nil, ValidateOptions{}); len(errs) == 0 {
		t.Fatal("nil-набор должен падать")
	}
}

// TestMergeFactIDSets — коллизия ID между candidate_context и hints: разметка
// становится неоднозначной, о ней нужно сообщить.
func TestMergeFactIDSets(t *testing.T) {
	cc := map[string]bool{"a": true, "shared": true}
	hints := map[string]bool{"b": true, "shared": true}

	merged, dupes := MergeFactIDSets(cc, hints)
	if len(merged) != 3 || !merged["a"] || !merged["b"] || !merged["shared"] {
		t.Errorf("merged = %v, want {a,b,shared}", merged)
	}
	if len(dupes) != 1 || dupes[0] != "shared" {
		t.Errorf("dupes = %v, want [shared]", dupes)
	}

	// Тот же ID внутри одного каталога — не коллизия между каталогами
	// (уникальность внутри каталога обеспечивает builder).
	merged, dupes = MergeFactIDSets(map[string]bool{"a": true}, map[string]bool{"b": true})
	if len(dupes) != 0 || len(merged) != 2 {
		t.Errorf("merged/dupes = %v/%v, want 2 элемента без коллизий", merged, dupes)
	}
}

// TestLoadFactIDs — ID берутся из индекса каталога (фикстура golden-теста).
func TestLoadFactIDs(t *testing.T) {
	dir := copyFixture(t, filepath.Join("testdata", "ccfn", "cc"))
	ids, err := LoadFactIDs(dir)
	if err != nil {
		t.Fatalf("LoadFactIDs: %v", err)
	}
	for _, id := range []string{"cc-pricing", "cc-php-legacy", "cc-java-platform", "cc-skills-kafka", "cc-skills-db"} {
		if !ids[id] {
			t.Errorf("в индексе нет %q (получено %v)", id, ids)
		}
	}

	if _, err := LoadFactIDs(t.TempDir()); err == nil {
		t.Error("отсутствие manifest.json должно быть ошибкой")
	}
}
