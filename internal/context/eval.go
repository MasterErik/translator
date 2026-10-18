package candidatecontext

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// ContextFn — контракт сборки контекста: вопрос и состояние сессии на входе,
// готовый к отправке контекст на выходе. Именно такую функцию возвращает
// BuildContextFn; eval, probe и прод используют один и тот же вход (M0/M1).
type ContextFn func(question string, s SessionContext) ContextResult

// EvalQuery is a single evaluation case: a question, the session state it is
// asked in, and the ground-truth markup (relevant / must_not FactIDs).
//
// Поля legacy-набора (train.json/holdout.json, D13) — подмножество: у них нет
// id/category/lang/source/split, поэтому обязательность полей проверяет не
// загрузчик, а валидатор в test/context_eval (там же — сверка split с файлом).
type EvalQuery struct {
	ID                    string   `json:"id"`
	Question              string   `json:"question"`
	SessionTargetStack    []string `json:"session_target_stack"`
	RelevantFactIDs       []string `json:"relevant_fact_ids"`
	MustNotFactIDs        []string `json:"must_not_fact_ids"`
	ExpectEffectiveTarget []string `json:"expect_effective_target"`
	Category              string   `json:"category"`
	Tags                  []string `json:"tags"`
	Lang                  string   `json:"lang"`
	KnownGap              bool     `json:"known_gap"`
	Source                string   `json:"source"`
	Split                 string   `json:"split"`
	Notes                 string   `json:"notes"`
}

// EvalLogEntry is one journal record: either holdout consumption or a change
// made to the frozen legacy set.
type EvalLogEntry struct {
	Date     string `json:"date"`
	Decision string `json:"decision"`
}

// EvalDataset is a collection of evaluation queries plus dataset-level metadata
// and journals. Legacy (frozen) datasets carry only queries/frozen/legacy_log
// or queries/frozen/holdout_log; v2 datasets add dataset_version and notes.
type EvalDataset struct {
	DatasetVersion int            `json:"dataset_version"`
	Notes          string         `json:"notes"`
	Frozen         bool           `json:"frozen"`
	HoldoutLog     []EvalLogEntry `json:"holdout_log"`
	LegacyLog      []EvalLogEntry `json:"legacy_log"`
	Queries        []EvalQuery    `json:"queries"`
}

// LoadEvalDataset reads and parses a JSON evaluation dataset from path.
// Unknown fields are rejected (json.Decoder.DisallowUnknownFields): опечатка в
// имени поля разметки должна падать, а не молча превращаться в пустой must_not
// и нулевой LeakRate.
func LoadEvalDataset(path string) (*EvalDataset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("candidatecontext: read eval dataset %s: %w", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var ds EvalDataset
	if err := dec.Decode(&ds); err != nil {
		return nil, fmt.Errorf("candidatecontext: parse eval dataset %s: %w", path, err)
	}
	return &ds, nil
}

// EvalMetrics aggregates retrieval metrics over a set of queries. Fractions are
// in [0, 1]; counts are raw case numbers so that an empty denominator is
// distinguishable from a perfect score.
type EvalMetrics struct {
	Queries       int // всего кейсов
	ScoredQueries int // кейсов с непустым relevant (знаменатель Recall/MRR)
	RecallAt1     float64
	RecallAt3     float64
	RecallAt5     float64
	MRR           float64

	LeakedQueries int     // кейсов, где must_not попал в итоговую выдачу
	LeakRate      float64 // LeakedQueries / Queries

	EmptyCases int     // кейсов с пустым relevant (ожидается пустая выдача)
	EmptyShown int     // из них: выдача действительно пуста
	EmptyRate  float64 // EmptyShown / EmptyCases

	HintShows     int     // кейсов, где показан готовый ответ (hints)
	FalseShows    int     // из них: ни один показанный hint не входит в relevant
	FalseShowRate float64 // FalseShows / HintShows
}

// EvalReport is the result of a full dataset run through ContextFn.
type EvalReport struct {
	Overall EvalMetrics            // все кейсы
	Gate    EvalMetrics            // без known_gap: по нему принимается regression gate
	Slices  map[string]EvalMetrics // по тегам + срез "known_gap"
}

// SortedSliceKeys returns slice names in a stable order (for reports).
func (r EvalReport) SortedSliceKeys() []string {
	keys := make([]string, 0, len(r.Slices))
	for k := range r.Slices {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Evaluate runs every query of ds through r.Retrieve and computes Recall@k and
// MRR. Recall@k is the fraction of queries for which at least one relevant
// FactID appears among the first k results. MRR is the mean over queries of
// 1/rank of the first relevant result (rank is 1-based; 0 if no relevant result
// is returned). An empty dataset (or a nil retriever/dataset) yields all-zero
// metrics.
//
// Evaluate считается по сырому ranking'у retriever'а и сохранён для механики;
// метрики по итоговому контексту (LeakRate/EmptyRate/FalseShowRate) считает
// EvaluateContextFn.
func Evaluate(r *Retriever, ds *EvalDataset) EvalMetrics {
	if r == nil || ds == nil || len(ds.Queries) == 0 {
		return EvalMetrics{}
	}

	n := float64(len(ds.Queries))
	var recall1, recall3, recall5, mrr float64
	for _, q := range ds.Queries {
		relevant := toSet(q.RelevantFactIDs)
		results := r.Retrieve(q.Question)

		// firstRank is the 1-based position of the first relevant result;
		// 0 means no relevant result was returned.
		firstRank := 0
		for i, res := range results {
			if _, ok := relevant[res.FactID]; ok {
				firstRank = i + 1
				break
			}
		}
		if firstRank == 0 {
			continue
		}

		// Recall@k holds iff the first relevant result falls within top-k.
		if firstRank <= 1 {
			recall1++
		}
		if firstRank <= 3 {
			recall3++
		}
		if firstRank <= 5 {
			recall5++
		}
		mrr += 1.0 / float64(firstRank)
	}

	return EvalMetrics{
		Queries:       len(ds.Queries),
		ScoredQueries: len(ds.Queries),
		RecallAt1:     recall1 / n,
		RecallAt3:     recall3 / n,
		RecallAt5:     recall5 / n,
		MRR:           mrr / n,
	}
}

// EvaluateContextFn runs every query of ds through fn and computes metrics over
// the FINAL context that would go to the model:
//
//   - Recall@k / MRR — по фактам, показанным в контексте (FactIDs, затем
//     HintIDs: подсказка уходит в конец промпта, поэтому её ранг хуже);
//   - LeakRate — доля кейсов, где must_not попал в FactIDs ∪ HintIDs;
//   - EmptyRate — доля корректных пустых выдач на кейсах с пустым relevant;
//   - FalseShowRate — доля показанных подсказок, не входящих в relevant.
//
// Кейсы с пустым relevant не участвуют в Recall/MRR (иначе корректная пустая
// выдача штрафовала бы recall). Кейсы с known_gap не участвуют в Gate, но
// считаются в Overall и в отдельном срезе.
func EvaluateContextFn(fn ContextFn, ds *EvalDataset) EvalReport {
	report := EvalReport{Slices: map[string]EvalMetrics{}}
	if fn == nil || ds == nil || len(ds.Queries) == 0 {
		return report
	}

	overall := &evalAccumulator{}
	gate := &evalAccumulator{}
	bySlice := map[string]*evalAccumulator{}

	for _, q := range ds.Queries {
		res := fn(q.Question, SessionContext{TargetStack: q.SessionTargetStack})
		ranked := make([]string, 0, len(res.FactIDs)+len(res.HintIDs))
		ranked = append(ranked, res.FactIDs...)
		ranked = append(ranked, res.HintIDs...)

		overall.add(q, ranked, res.HintIDs)
		if !q.KnownGap {
			gate.add(q, ranked, res.HintIDs)
		}

		keys := append([]string{}, q.Tags...)
		if q.KnownGap {
			keys = append(keys, "known_gap")
		}
		for _, key := range keys {
			acc, ok := bySlice[key]
			if !ok {
				acc = &evalAccumulator{}
				bySlice[key] = acc
			}
			acc.add(q, ranked, res.HintIDs)
		}
	}

	report.Overall = overall.metrics()
	report.Gate = gate.metrics()
	for key, acc := range bySlice {
		report.Slices[key] = acc.metrics()
	}
	return report
}

// evalAccumulator is a running sum of per-case outcomes.
type evalAccumulator struct {
	queries    int
	scored     int
	recall1    int
	recall3    int
	recall5    int
	mrr        float64
	leaked     int
	emptyCases int
	emptyShown int
	hintShows  int
	falseShows int
}

// add records one case: ranked is the FINAL показанная выдача (факты, затем
// подсказки), hintIDs — только показанные подсказки.
func (a *evalAccumulator) add(q EvalQuery, ranked, hintIDs []string) {
	a.queries++

	relevant := toSet(q.RelevantFactIDs)
	if len(relevant) > 0 {
		a.scored++
		firstRank := 0
		for i, id := range ranked {
			if _, ok := relevant[id]; ok {
				firstRank = i + 1
				break
			}
		}
		if firstRank > 0 {
			if firstRank <= 1 {
				a.recall1++
			}
			if firstRank <= 3 {
				a.recall3++
			}
			if firstRank <= 5 {
				a.recall5++
			}
			a.mrr += 1.0 / float64(firstRank)
		}
	} else {
		a.emptyCases++
		if len(ranked) == 0 {
			a.emptyShown++
		}
	}

	mustNot := toSet(q.MustNotFactIDs)
	for _, id := range ranked {
		if _, ok := mustNot[id]; ok {
			a.leaked++
			break
		}
	}

	if len(hintIDs) > 0 {
		a.hintShows++
		falseShow := true
		for _, id := range hintIDs {
			if _, ok := relevant[id]; ok {
				falseShow = false
				break
			}
		}
		if falseShow {
			a.falseShows++
		}
	}
}

// metrics converts the running sums into fractions. Zero denominator gives a
// zero fraction (а не NaN): пустой набор не должен выглядеть идеальным.
func (a *evalAccumulator) metrics() EvalMetrics {
	m := EvalMetrics{
		Queries:       a.queries,
		ScoredQueries: a.scored,
		LeakedQueries: a.leaked,
		EmptyCases:    a.emptyCases,
		EmptyShown:    a.emptyShown,
		HintShows:     a.hintShows,
		FalseShows:    a.falseShows,
	}
	if a.scored > 0 {
		n := float64(a.scored)
		m.RecallAt1 = float64(a.recall1) / n
		m.RecallAt3 = float64(a.recall3) / n
		m.RecallAt5 = float64(a.recall5) / n
		m.MRR = a.mrr / n
	}
	if a.queries > 0 {
		m.LeakRate = float64(a.leaked) / float64(a.queries)
	}
	if a.emptyCases > 0 {
		m.EmptyRate = float64(a.emptyShown) / float64(a.emptyCases)
	}
	if a.hintShows > 0 {
		m.FalseShowRate = float64(a.falseShows) / float64(a.hintShows)
	}
	return m
}
