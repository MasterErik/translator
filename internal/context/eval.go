package candidatecontext

import (
	"encoding/json"
	"fmt"
	"os"
)

// EvalQuery is a single evaluation case: a question plus the set of FactIDs
// that are considered relevant (ground truth) for that question.
type EvalQuery struct {
	Question        string   `json:"question"`
	RelevantFactIDs []string `json:"relevant_fact_ids"`
}

// EvalDataset is a collection of evaluation queries for fact-level retrieval.
type EvalDataset struct {
	Queries []EvalQuery `json:"queries"`
}

// LoadEvalDataset reads and parses a JSON evaluation dataset from path.
func LoadEvalDataset(path string) (*EvalDataset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("candidatecontext: read eval dataset %s: %w", path, err)
	}
	var ds EvalDataset
	if err := json.Unmarshal(data, &ds); err != nil {
		return nil, fmt.Errorf("candidatecontext: parse eval dataset %s: %w", path, err)
	}
	return &ds, nil
}

// EvalMetrics aggregates retrieval metrics over a dataset. Every value is a
// fraction in the range [0, 1].
type EvalMetrics struct {
	RecallAt1 float64
	RecallAt3 float64
	RecallAt5 float64
	MRR       float64
}

// Evaluate runs every query of ds through r.Retrieve and computes Recall@k and
// MRR. Recall@k is the fraction of queries for which at least one relevant
// FactID appears among the first k results. MRR is the mean over queries of
// 1/rank of the first relevant result (rank is 1-based; 0 if no relevant result
// is returned). An empty dataset (or a nil retriever/dataset) yields all-zero
// metrics.
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
		RecallAt1: recall1 / n,
		RecallAt3: recall3 / n,
		RecallAt5: recall5 / n,
		MRR:       mrr / n,
	}
}
