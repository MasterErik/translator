// Command context_index пересобирает fact-level retrieval index для candidate
// context: читает manifest.json, строит index и атомарно сохраняет его в
// index.json. Полезен для ручной (пере)сборки после правок sections/*.md.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mastererik/translator/internal/context"
)

func main() {
	dir := flag.String("dir", "./candidate_context", "path to candidate_context directory (manifest.json + sections/)")
	flag.Parse()

	manifestPath := filepath.Join(*dir, "manifest.json")
	manifest, err := candidatecontext.LoadManifest(manifestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	index, err := candidatecontext.BuildIndex(manifest, *dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	indexPath := filepath.Join(*dir, "index.json")
	if err := candidatecontext.SaveIndex(index, indexPath); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("facts: %d\nfiles: %d\nindex: %s\n", len(index.Facts), len(index.Files), indexPath)
}
