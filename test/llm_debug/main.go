// Debug: проверяем raw ответ от GLM-4.7-Flash
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/mastererik/translator/internal/common"
	"github.com/mastererik/translator/internal/translator"
)

func main() {
	cfg, err := common.LoadConfigFromYAML("config.yaml")
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR: config.yaml not found:", err)
		os.Exit(1)
	}
	apiKey := cfg.LLMAPIKey
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "ERROR: no API key")
		os.Exit(1)
	}

	prov := translator.NewChatProvider(cfg.LLMBaseURL, apiKey, cfg.LLMModel)
	prov.SetMaxTokens(cfg.MaxTokens)

	cv := "Senior Go developer, 5+ years, expert in concurrency patterns."
	q := "What is the difference between a mutex and a channel in Go?"

	fmt.Printf("Model: %s\nBase URL: %s\nMaxTokens: %d\n\n", cfg.LLMModel, cfg.LLMBaseURL, cfg.MaxTokens)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	answers, err := prov.GenerateAnswers(ctx, translator.AnswerRequest{Question: q, CandidateContext: cv})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Got %d answers:\n", len(answers))
	for i, a := range answers {
		fmt.Printf("  [%d] %q\n", i+1, a)
	}
}
