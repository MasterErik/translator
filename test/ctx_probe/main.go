// Command ctx_probe — диагностический пробник retrieval: печатает топ-K
// фактов со scores для заданных вопросов. Используется для ручной
// диагностики утечек (explain-режим до его реализации в Task 2b).
//
// Usage: go run ./test/ctx_probe --dir candidate_context --q "question" [--top 5]
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	candidatecontext "github.com/mastererik/translator/internal/context"
)

func main() {
	dir := flag.String("dir", "./candidate_context", "path to candidate_context directory")
	questions := flag.String("q", "", "questions separated by |")
	top := flag.Int("top", 5, "top-K")
	flag.Parse()

	manifest, index, err := candidatecontext.LoadCandidateContext(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	retriever, err := candidatecontext.NewRetriever(manifest, index, *dir, *top, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	for _, q := range strings.Split(*questions, "|") {
		q = strings.TrimSpace(q)
		if q == "" {
			continue
		}
		fmt.Printf("\n=== %q ===\n", q)
		for i, r := range retriever.Retrieve(q) {
			content := strings.Join(strings.Fields(r.Content), " ")
			if len(content) > 90 {
				content = content[:90] + "..."
			}
			fmt.Printf("  %d. %-38s s=%6.2f  [%s]  %s\n", i+1, r.FactID, r.Score, r.SectionID, content)
		}
		if len(retriever.Retrieve(q)) == 0 {
			fmt.Println("  (пусто)")
		}
	}
}
