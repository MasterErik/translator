// Интеграционный тест LLM-подсказок (Answer Generation) с реальным API.
// Перевод теперь выполняется Gladia Translation API, LLM — только для подсказок.
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
	cfg := common.LoadConfig()
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
	fmt.Printf("Model:        %s\n", cfg.OpenAIModel)
	fmt.Printf("Max Tokens:   %d\n", maxTokens)
	fmt.Printf("API Key:      %s...%s\n", apiKey[:8], apiKey[len(apiKey)-4:])
	fmt.Println()

	prov := translator.NewOpenAIProviderWithConfig(cfg.LLMBaseURL, apiKey, cfg.OpenAIModel)
	prov.SetMaxTokens(maxTokens)

	// ── 1. Answer Generation (batch) ──
	fmt.Println("═══ 1. GenerateAnswers ═══")
	ctx1, cancel1 := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel1()

	q := "What is the difference between a mutex and a channel in Go?"
	cv := "Senior Go developer, 5+ years, expert in concurrency patterns."
	fmt.Printf("Q: %s\n", q)

	start := time.Now()
	answers, err := prov.GenerateAnswers(ctx1, q, cv)
	elapsed := time.Since(start)
	if err != nil {
		fmt.Printf("FAIL (%s): %v\n", elapsed.Round(time.Millisecond), err)
	} else {
		for i, a := range answers {
			fmt.Printf("  %d. %s\n", i+1, a)
		}
		fmt.Printf("OK  (%s)\n", elapsed.Round(time.Millisecond))
	}

	// ── 2. Answer Generation (streaming) ──
	fmt.Println("\n═══ 2. GenerateAnswersStream ═══")
	ctx2, cancel2 := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel2()

	q2 := "Explain the CAP theorem in distributed systems."
	fmt.Printf("Q: %s\n", q2)

	tokenCh, err := prov.GenerateAnswersStream(ctx2, q2, cv)
	if err != nil {
		fmt.Printf("FAIL (create): %v\n", err)
	} else {
		fmt.Print("A: ")
		start := time.Now()
		var sb strings.Builder
		tokenCount := 0
		for token := range tokenCh {
			if strings.HasPrefix(token, "[ERROR:") {
				fmt.Print(token)
				break
			}
			sb.WriteString(token)
			fmt.Print(token)
			tokenCount++
		}
		elapsed := time.Since(start)
		if sb.Len() > 0 {
			fmt.Printf("\nOK  (%s, %d tokens)\n", elapsed.Round(time.Millisecond), tokenCount)
		} else {
			fmt.Printf("\nFAIL: no tokens\n")
		}
	}

	fmt.Println("\n═══ Done ═══")
}
