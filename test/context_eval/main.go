// Command context_eval evaluates fact-level retrieval quality for candidate
// context against one or two JSON evaluation datasets (train and/or holdout).
// It prints a table of Recall@1/3/5 and MRR to stdout.
package main

import (
	"flag"
	"fmt"
	"os"

	candidatecontext "github.com/mastererik/translator/internal/context"
)

func main() {
	dir := flag.String("dir", "./candidate_context", "path to candidate_context directory (manifest.json + sections/)")
	train := flag.String("train", "", "path to train evaluation dataset (JSON)")
	holdout := flag.String("holdout", "", "path to holdout evaluation dataset (JSON)")
	flag.Parse()

	manifest, index, err := candidatecontext.LoadCandidateContext(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	retriever, err := candidatecontext.NewRetriever(manifest, index, *dir, 5, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	datasets := []struct {
		name string
		path string
	}{
		{"train", *train},
		{"holdout", *holdout},
	}

	fmt.Printf("%-10s %8s %9s %9s %9s %9s\n", "dataset", "queries", "Recall@1", "Recall@3", "Recall@5", "MRR")
	for _, ds := range datasets {
		if ds.path == "" {
			continue
		}
		d, err := candidatecontext.LoadEvalDataset(ds.path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		m := candidatecontext.Evaluate(retriever, d)
		fmt.Printf("%-10s %8d %9.3f %9.3f %9.3f %9.3f\n", ds.name, len(d.Queries), m.RecallAt1, m.RecallAt3, m.RecallAt5, m.MRR)
	}
}
