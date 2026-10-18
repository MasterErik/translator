// Command context_explain печатает explain-разложение по вопросам датасета:
// вклады полей, section/phrase бонусы, вердикты content-only гейта и relative
// threshold, стек-множитель. Инструмент калибровки M7: только диагностика,
// ранжирование совпадает с прод-путём (RetrieveWith/ExplainQuestion).
//
// Пример:
//
//	go run ./test/context_explain -dir candidate_context \
//	  -dataset internal/context/testdata/eval_v2_train.json -tag temporal
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	candidatecontext "github.com/mastererik/translator/internal/context"
)

func main() {
	dir := flag.String("dir", "./candidate_context", "path to candidate_context directory")
	dataset := flag.String("dataset", "", "path to eval dataset (JSON)")
	tag := flag.String("tag", "", "печатать только вопросы с этим тегом")
	id := flag.String("q", "", "печатать только вопрос с этим id")
	gateTau := flag.Float64("gate-tau", candidatecontext.DefaultContentGateTau, "content-only gate tau (отрицательное = выключить)")
	relThreshold := flag.Float64("rel-threshold", candidatecontext.DefaultRelativeThreshold, "relative threshold (отрицательное = выключить)")
	limit := flag.Int("limit", 6, "сколько кандидатов печатать на вопрос")
	rejectedOnly := flag.Bool("rejected-only", false, "печатать только отклонённых кандидатов")
	summary := flag.Bool("summary", false, "печатать сводку по распределению ratio/core вместо деталей")
	flag.Parse()

	if *dataset == "" {
		fmt.Fprintln(os.Stderr, "error: -dataset обязателен")
		os.Exit(2)
	}

	manifest, index, err := candidatecontext.LoadCandidateContext(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	retriever, err := candidatecontext.NewRetrieverWithOptions(manifest, index, *dir, candidatecontext.RetrieverOptions{
		TopK:              0,
		ContentGateTau:    *gateTau,
		RelativeThreshold: *relThreshold,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	ds, err := candidatecontext.LoadEvalDataset(*dataset)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	type ratioRow struct {
		ratio   float64
		core    float64
		passed  bool
		factID  string
		queryID string
	}
	var rows []ratioRow

	for _, q := range ds.Queries {
		if *tag != "" && !contains(q.Tags, *tag) {
			continue
		}
		if *id != "" && q.ID != *id {
			continue
		}
		opts := candidatecontext.RetrieveOptions{
			Session: candidatecontext.SessionContext{TargetStack: q.SessionTargetStack},
			Explain: true,
		}
		items := retriever.ExplainQuestion(q.Question, opts)
		sort.Slice(items, func(i, j int) bool { return items[i].Breakdown.Rank > items[j].Breakdown.Rank })

		fmt.Printf("\n=== %s [split=%s tags=%s] %q\n", q.ID, q.Split, strings.Join(q.Tags, ","), q.Question)
		if len(q.RelevantFactIDs) > 0 {
			fmt.Printf("    relevant:   %s\n", strings.Join(q.RelevantFactIDs, ", "))
		}
		if len(q.MustNotFactIDs) > 0 {
			fmt.Printf("    must_not:   %s\n", strings.Join(q.MustNotFactIDs, ", "))
		}
		if len(q.SessionTargetStack) > 0 {
			fmt.Printf("    target:     %s\n", strings.Join(q.SessionTargetStack, ", "))
		}

		shown := 0
		for _, it := range items {
			rows = append(rows, ratioRow{
				ratio: it.Breakdown.ContentRatio, core: it.Breakdown.Core,
				passed: it.Passed, factID: it.FactID, queryID: q.ID,
			})
			if *rejectedOnly && it.Passed {
				continue
			}
			if shown >= *limit {
				continue
			}
			shown++
			mark := " "
			if contains(q.RelevantFactIDs, it.FactID) {
				mark = "*"
			} else if contains(q.MustNotFactIDs, it.FactID) {
				mark = "!"
			}
			verdict := "pass"
			if !it.Passed {
				verdict = "REJ " + it.RejectedBy
			}
			b := it.Breakdown
			fmt.Printf("  %s %-24s rank=%6.3f raw=%6.3f core=%6.3f ratio=%.3f kw=%.2f ti=%.2f al=%.2f ct=%.2f sec=%.2f ph=%.2f | %s\n",
				mark, it.FactID, b.Rank, b.Raw, b.Core, b.ContentRatio,
				b.Keywords, b.Title, b.Aliases, b.Content, b.Section, b.Phrase, verdict)
		}
		if *summary {
			continue
		}
	}

	if *summary {
		sort.Slice(rows, func(i, j int) bool { return rows[i].ratio < rows[j].ratio })
		fmt.Printf("\nratio (matched_content_IDF / question_IDF), %d кандидатов:\n", len(rows))
		for i, r := range rows {
			if i >= 25 {
				break
			}
			fmt.Printf("  %.3f core=%6.3f passed=%v %s (%s)\n", r.ratio, r.core, r.passed, r.factID, r.queryID)
		}
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
