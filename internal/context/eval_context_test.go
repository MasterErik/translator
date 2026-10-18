package candidatecontext

// M1 плана: механика метрик по ИТОГОВОМУ контексту (EvaluateContextFn) на
// заглушке ContextFn с заданным ранжированием. Реальный прогон по каталогам
// делается командой test/context_eval (LeakRate требует живого индекса).

import (
	"math"
	"testing"
)

// stubContextFn возвращает заранее заданный ContextResult по вопросу; вопросы,
// которых нет в карте, дают пустой результат.
func stubContextFn(byQuestion map[string]ContextResult) ContextFn {
	return func(question string, _ SessionContext) ContextResult {
		return byQuestion[question]
	}
}

func near(got, want float64) bool { return math.Abs(got-want) < 1e-9 }

// TestEvaluateContextFnMechanics — все четыре метрики на одном наборе:
// промах в топ-1, утечка must_not, корректная пустая выдача, ложный показ
// подсказки и известный разрыв (known_gap вне gate).
func TestEvaluateContextFnMechanics(t *testing.T) {
	ds := &EvalDataset{Queries: []EvalQuery{
		{ID: "q1", Question: "miss в топ-1", RelevantFactIDs: []string{"r1"}, Tags: []string{"gate"}},
		{ID: "q2", Question: "утечка", RelevantFactIDs: []string{"r2"}, MustNotFactIDs: []string{"x1"}, Tags: []string{"gate"}},
		{ID: "q3", Question: "пустая выдача", Tags: []string{TagNoRelevant}},
		{ID: "q4", Question: "ложный hint", RelevantFactIDs: []string{"h1"}, Tags: []string{"gate"}},
		{ID: "q5", Question: "известный разрыв", RelevantFactIDs: []string{"r4"}, KnownGap: true},
	}}

	fn := stubContextFn(map[string]ContextResult{
		"miss в топ-1":     {FactIDs: []string{"nope", "r1"}},
		"утечка":           {FactIDs: []string{"x1", "other"}},
		"пустая выдача":    {},
		"ложный hint":      {FactIDs: []string{"other"}, HintIDs: []string{"notrelevant"}},
		"известный разрыв": {FactIDs: []string{"r4"}},
	})

	report := EvaluateContextFn(fn, ds)

	// Overall: 5 кейсов, из них 4 с непустым relevant (знаменатель Recall).
	o := report.Overall
	if o.Queries != 5 || o.ScoredQueries != 4 {
		t.Fatalf("Overall: got %d/%d кейсов, want 5/4", o.Queries, o.ScoredQueries)
	}
	if !near(o.RecallAt1, 0.25) { // r1 на ранге 2, r4 на ранге 1
		t.Errorf("Recall@1 = %v, want 0.25", o.RecallAt1)
	}
	if !near(o.RecallAt3, 0.5) {
		t.Errorf("Recall@3 = %v, want 0.5", o.RecallAt3)
	}
	if !near(o.RecallAt5, 0.5) {
		t.Errorf("Recall@5 = %v, want 0.5", o.RecallAt5)
	}
	if !near(o.MRR, 0.375) { // (1/2 + 1) / 4
		t.Errorf("MRR = %v, want 0.375", o.MRR)
	}
	if o.LeakedQueries != 1 || !near(o.LeakRate, 0.2) {
		t.Errorf("LeakRate = %v (%d кейсов), want 0.2 (1)", o.LeakRate, o.LeakedQueries)
	}
	if o.EmptyCases != 1 || o.EmptyShown != 1 || !near(o.EmptyRate, 1) {
		t.Errorf("EmptyRate = %v (%d/%d), want 1 (1/1)", o.EmptyRate, o.EmptyShown, o.EmptyCases)
	}
	if o.HintShows != 1 || o.FalseShows != 1 || !near(o.FalseShowRate, 1) {
		t.Errorf("FalseShowRate = %v (%d/%d), want 1 (1/1)", o.FalseShowRate, o.FalseShows, o.HintShows)
	}

	// Gate: known_gap исключён (4 кейса, 3 с непустым relevant).
	g := report.Gate
	if g.Queries != 4 || g.ScoredQueries != 3 {
		t.Fatalf("Gate: got %d/%d кейсов, want 4/3", g.Queries, g.ScoredQueries)
	}
	if !near(g.RecallAt1, 0) || !near(g.MRR, 0.5/3) {
		t.Errorf("Gate recall@1/MRR = %v/%v, want 0/0.1667", g.RecallAt1, g.MRR)
	}
	if !near(g.LeakRate, 0.25) {
		t.Errorf("Gate LeakRate = %v, want 0.25", g.LeakRate)
	}

	// Срезы: по тегам + отдельный known_gap.
	if got := report.SortedSliceKeys(); len(got) != 3 || got[0] != "gate" || got[1] != "known_gap" || got[2] != TagNoRelevant {
		t.Fatalf("SortedSliceKeys = %v, want [gate known_gap no_relevant]", got)
	}
	if kg := report.Slices["known_gap"]; kg.ScoredQueries != 1 || !near(kg.RecallAt1, 1) {
		t.Errorf("срез known_gap = %+v, want ScoredQueries=1 Recall@1=1", kg)
	}
	if gate := report.Slices["gate"]; gate.Queries != 3 || !near(gate.LeakRate, 1.0/3) {
		t.Errorf("срез gate = %+v, want Queries=3 LeakRate=0.333", gate)
	}
}

// TestEvaluateContextFnRankOrder — ранг считается по итоговой строке промпта:
// факты идут раньше подсказок, поэтому relevant в подсказке получает ранг
// len(FactIDs)+1 (подсказка уходит в самый низ контекста).
func TestEvaluateContextFnRankOrder(t *testing.T) {
	ds := &EvalDataset{Queries: []EvalQuery{
		{ID: "q1", Question: "hint", RelevantFactIDs: []string{"h1"}},
	}}
	fn := stubContextFn(map[string]ContextResult{
		"hint": {FactIDs: []string{"a", "b"}, HintIDs: []string{"h1"}},
	})

	report := EvaluateContextFn(fn, ds)
	o := report.Overall
	if !near(o.RecallAt1, 0) || !near(o.RecallAt3, 1) || !near(o.RecallAt5, 1) {
		t.Errorf("Recall@1/3/5 = %v/%v/%v, want 0/1/1", o.RecallAt1, o.RecallAt3, o.RecallAt5)
	}
	if !near(o.MRR, 1.0/3) {
		t.Errorf("MRR = %v, want 0.333 (relevant на 3-й позиции)", o.MRR)
	}
	if o.FalseShows != 0 || !near(o.FalseShowRate, 0) {
		t.Errorf("FalseShowRate = %v, want 0 (hint в relevant)", o.FalseShowRate)
	}
}

// TestEvaluateContextFnEmptyInputs — пустой вход не даёт NaN и не выглядит
// идеальным: все дроби нули, счётчики нули.
func TestEvaluateContextFnEmptyInputs(t *testing.T) {
	for _, tc := range []struct {
		name string
		fn   ContextFn
		ds   *EvalDataset
	}{
		{name: "nil fn", fn: nil, ds: &EvalDataset{Queries: []EvalQuery{{Question: "q"}}}},
		{name: "nil ds", fn: stubContextFn(nil), ds: nil},
		{name: "пустой набор", fn: stubContextFn(nil), ds: &EvalDataset{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := EvaluateContextFn(tc.fn, tc.ds)
			if report.Overall != (EvalMetrics{}) {
				t.Errorf("Overall = %+v, want zero", report.Overall)
			}
			if len(report.Slices) != 0 {
				t.Errorf("Slices = %v, want empty", report.Slices)
			}
		})
	}
}

// TestEvaluateContextFnSessionState — eval передаёт стек сессии из кейса, а не
// пустое состояние: метрики v2 обязаны учитывать таргет интервью.
func TestEvaluateContextFnSessionState(t *testing.T) {
	ds := &EvalDataset{Queries: []EvalQuery{
		{ID: "q1", Question: "stack", SessionTargetStack: []string{"java"}, RelevantFactIDs: []string{"r1"}},
	}}
	var got []string
	fn := func(_ string, s SessionContext) ContextResult {
		got = append([]string{}, s.TargetStack...)
		return ContextResult{FactIDs: []string{"r1"}}
	}

	EvaluateContextFn(fn, ds)
	if len(got) != 1 || got[0] != "java" {
		t.Errorf("TargetStack, переданный в ContextFn = %v, want [java]", got)
	}
}
