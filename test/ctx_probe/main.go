// Command ctx_probe — диагностический пробник единой сборки контекста
// (candidatecontext.BuildContextFn). Использует тот же вход, что и pipeline,
// eval и interview_sim (M0: «eval = прод»): печатает по каждому вопросу
// итоговые факты (FactIDs), подсказки (HintIDs), флаги HintsDisabled/Degraded
// и превью итоговой строки, уходящей в prompt.
//
// Дополнительно под флагом -scores показывает сырой ранжированный список
// Retriever.Retrieve — это ranking ДО бюджета, модификаторов и отсечения по
// токенам. Итоговый (объяснимый) ranking появится в M7 через Explain.
//
// Usage:
//
//	go run ./test/ctx_probe --dir candidate_context --hints-dir hints \
//	    --q "Describe your experience with PHP|What is your experience with Apache Kafka?"
//
// Legacy-режим (D14): если задан только -legacy-file (без -dir) — пробник
// читает файл базы CV целиком и печатает то, что уходит в prompt.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	candidatecontext "github.com/mastererik/translator/internal/context"
)

// previewLimit — длина однострочного превью Text в символах.
const previewLimit = 600

func main() {
	dir := flag.String("dir", "candidate_context", "path to candidate_context directory (empty → legacy mode via -legacy-file)")
	hintsDir := flag.String("hints-dir", "hints", "path to hints directory (empty — hints disabled)")
	questions := flag.String("q", "", "questions separated by |")
	top := flag.Int("top", 5, "candidate context top-K")
	minScore := flag.Float64("min-score", 0, "candidate context min score")
	maxTokens := flag.Int("max-tokens", 2000, "candidate facts token budget")
	maxProfileTokens := flag.Int("max-profile-tokens", 150, "candidate profile token budget")
	hintsTop := flag.Int("hints-top", 1, "hints top-K")
	hintsMinScore := flag.Float64("hints-min-score", 0, "hints min score")
	legacyFile := flag.String("legacy-file", "", "legacy CV file (used when -dir is empty, or as fallback)")
	allowLegacy := flag.Bool("allow-legacy-fallback", false, "fall back to -legacy-file if the -dir context fails to load")
	onLoadError := flag.String("on-load-error", candidatecontext.OnLoadErrorFail, "load error mode: fail | degraded")
	scores := flag.Bool("scores", false, "also print raw Retriever ranking (before budget/modifiers)")
	flag.Parse()

	// Legacy-режим: -dir не задан вовсе, источник — файл базы CV.
	legacyMode := *dir == ""
	if legacyMode && *legacyFile == "" {
		fmt.Fprintln(os.Stderr, "error: задайте -dir (fact-level) или -legacy-file (legacy-режим)")
		os.Exit(1)
	}

	cfg := candidatecontext.ContextFnConfig{
		Dir:                 *dir,
		TopK:                *top,
		MinScore:            *minScore,
		MaxTokens:           *maxTokens,
		MaxProfileTokens:    *maxProfileTokens,
		LegacyFile:          *legacyFile,
		AllowLegacyFallback: *allowLegacy,
		HintsDir:            *hintsDir,
		HintsTopK:           *hintsTop,
		HintsMinScore:       *hintsMinScore,
		OnLoadError:         *onLoadError,
		Logf: func(msg string) {
			fmt.Fprintf(os.Stderr, "%s\n", msg)
		},
	}
	contextFn, err := candidatecontext.BuildContextFn(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Сырой ranking — только под -scores и только в fact-level режиме
	// (в legacy-режиме retrieval по вопросу не выполняется).
	var retriever *candidatecontext.Retriever
	if *scores && !legacyMode {
		manifest, index, lErr := candidatecontext.LoadCandidateContext(*dir)
		if lErr != nil {
			fmt.Fprintf(os.Stderr, "error: scores: %v\n", lErr)
			os.Exit(1)
		}
		retriever, err = candidatecontext.NewRetriever(manifest, index, *dir, *top, *minScore)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: scores: %v\n", err)
			os.Exit(1)
		}
	}

	mode := fmt.Sprintf("fact-level dir=%q", *dir)
	if legacyMode {
		mode = fmt.Sprintf("legacy file=%q", *legacyFile)
	}
	fmt.Printf("ctx_probe: mode=%s hints_dir=%q top_k=%d max_tokens=%d max_profile_tokens=%d hints_top_k=%d\n",
		mode, *hintsDir, *top, *maxTokens, *maxProfileTokens, *hintsTop)
	if retriever != nil {
		fmt.Println("  (-scores: сырой ranking ДО бюджета/модификаторов/отсечения по токенам)")
	}

	for _, q := range strings.Split(*questions, "|") {
		q = strings.TrimSpace(q)
		if q == "" {
			continue
		}

		res := contextFn(q, candidatecontext.SessionContext{})

		fmt.Printf("\n=== %q ===\n", q)
		fmt.Printf("  FactIDs:       %s\n", idsString(res.FactIDs))
		fmt.Printf("  HintIDs:       %s\n", idsString(res.HintIDs))
		fmt.Printf("  HintsDisabled: %v\n", res.HintsDisabled)
		fmt.Printf("  Degraded:      %v\n", res.Degraded)
		fmt.Printf("  Text (%d симв.):\n", len(res.Text))
		preview := oneLine(res.Text)
		if len(preview) > previewLimit {
			preview = preview[:previewLimit] + "..."
		}
		if preview == "" {
			preview = "(пусто)"
		}
		fmt.Printf("    %s\n", preview)

		if retriever != nil {
			fmt.Println("  --- raw ranking (до бюджета) ---")
			results := retriever.Retrieve(q)
			if len(results) == 0 {
				fmt.Println("    (пусто)")
			}
			for i, r := range results {
				content := oneLine(r.Content)
				if len(content) > 90 {
					content = content[:90] + "..."
				}
				fmt.Printf("    %d. %-38s s=%6.2f  [%s]  %s\n", i+1, r.FactID, r.Score, r.SectionID, content)
			}
		}
	}
}

// idsString форматирует ID списком; пустой список — "(none)".
func idsString(ids []string) string {
	if len(ids) == 0 {
		return "(none)"
	}
	return strings.Join(ids, ", ")
}

// oneLine схлопывает переводы строк и серии пробелов в один пробел.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
