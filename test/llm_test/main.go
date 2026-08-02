// Интеграционный тест LLM-подсказок (Answer Generation) с реальным API.
// Проверяет что GenerateAnswers возвращает ровно 1 подсказку с русским переводом.
// Usage: go run ./test/llm_test
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mastererik/translator/internal/common"
	"github.com/mastererik/translator/internal/translator"
)

func main() {
	cfg, err := common.LoadConfigFromYAML("config.yaml")
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "ERROR: config.yaml not found:", err)
		os.Exit(1)
	}
	apiKey := cfg.LLMAPIKey
	if apiKey == "" {
		_, _ = fmt.Fprintln(os.Stderr, "ERROR: LLM_API_KEY or OPENAI_API_KEY not set")
		os.Exit(1)
	}

	maxTokens := cfg.MaxTokens
	if maxTokens == 0 {
		maxTokens = 256
	}

	fmt.Printf("LLM Base URL: %s\n", cfg.LLMBaseURL)
	fmt.Printf("Model:        %s\n", cfg.LLMModel)
	fmt.Printf("Max Tokens:   %d\n", maxTokens)
	fmt.Printf("API Key:      %s...%s\n", apiKey[:8], apiKey[len(apiKey)-4:])
	fmt.Println()

	prov := translator.NewChatProvider(cfg.LLMBaseURL, apiKey, cfg.LLMModel)
	prov.SetMaxTokens(maxTokens)

	cv := "Senior Go developer, 5+ years, expert in concurrency patterns."

	questions := []string{
		"What is the difference between a mutex and a channel in Go?",
		"Explain the CAP theorem in distributed systems.",
	}

	passed := 0
	failed := 0

	for i, q := range questions {
		fmt.Printf("─── Test %d ───\n", i+1)
		fmt.Printf("Q: %s\n", q)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

		start := time.Now()
		answers, err := prov.GenerateAnswers(ctx, q, cv)
		elapsed := time.Since(start)
		cancel()

		if err != nil {
			fmt.Printf("FAIL (%s): API error: %v\n\n", elapsed.Round(time.Millisecond), err)
			failed++
			continue
		}

		// Validation 1: exactly 1 answer
		if len(answers) != 1 {
			fmt.Printf("FAIL (%s): expected 1 answer, got %d\n", elapsed.Round(time.Millisecond), len(answers))
			for j, a := range answers {
				fmt.Printf("  [%d] %s\n", j+1, a)
			}
			fmt.Println()
			failed++
			continue
		}

		answer := answers[0]

		// Validation 2: must contain "| RU:"
		if !strings.Contains(answer, "| RU:") {
			fmt.Printf("FAIL (%s): answer missing '| RU:' separator\n", elapsed.Round(time.Millisecond))
			fmt.Printf("  Got: %s\n", answer)
			fmt.Println()
			failed++
			continue
		}

		fmt.Printf("  %s\n", answer)
		fmt.Printf("PASS (%s) — 1 answer with RU: translation\n\n", elapsed.Round(time.Millisecond))
		passed++
	}

	fmt.Println("═══════════════════════════════")
	fmt.Printf("Results: %d passed, %d failed, %d total\n", passed, failed, len(questions))

	if failed > 0 {
		os.Exit(1)
	}
}
