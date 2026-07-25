// Интеграционный тест LLM-перевода с реальным API.
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
		fmt.Fprintln(os.Stderr, "ERROR: LLM_API_KEY or OPENAI_API_KEY not set")
		os.Exit(1)
	}

	maxTokens := cfg.MaxTokens
	if maxTokens == 0 {
		maxTokens = 256 // default for translation
	}

	fmt.Printf("LLM Base URL: %s\n", cfg.LLMBaseURL)
	fmt.Printf("Model:        %s\n", cfg.OpenAIModel)
	fmt.Printf("Max Tokens:   %d\n", maxTokens)
	fmt.Printf("API Key:      %s...%s\n", apiKey[:8], apiKey[len(apiKey)-4:])
	fmt.Println()

	prov := translator.NewOpenAIProviderWithConfig(cfg.LLMBaseURL, apiKey, cfg.OpenAIModel)
	prov.SetMaxTokens(maxTokens)

	// ── 1. Translate ──
	fmt.Println("═══ 1. Translate ═══")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	phrases := []string{
		"Hello, could you explain what a deadlock is and how to avoid it in Go?",
		"We use Redis for caching and PostgreSQL for persistence.",
	}
	for i, p := range phrases {
		start := time.Now()
		tr, err := prov.Translate(ctx, p, nil)
		elapsed := time.Since(start)
		fmt.Printf("[%d] EN: %s\n", i+1, p)
		if err != nil {
			fmt.Printf("    FAIL (%s): %v\n", elapsed.Round(time.Millisecond), err)
		} else {
			fmt.Printf("    RU: %s  (%s)\n", tr, elapsed.Round(time.Millisecond))
		}
	}

	// ── 2. Streaming Translate ──
	fmt.Println("\n═══ 2. Streaming Translate ═══")
	ctx2, cancel2 := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel2()

	streamPhrase := "Can you describe the event-driven architecture pattern?"
	fmt.Printf("EN: %s\n", streamPhrase)

	tokenCh, err := prov.TranslateStream(ctx2, streamPhrase, nil)
	if err != nil {
		fmt.Printf("FAIL (create): %v\n", err)
	} else {
		fmt.Print("RU: ")
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
			fmt.Printf("  (%s, %d tokens)\n", elapsed.Round(time.Millisecond), tokenCount)
		} else {
			fmt.Printf("  FAIL: no tokens\n")
		}
	}

	// ── 3. Translate with History ──
	fmt.Println("\n═══ 3. Translate with History ═══")
	ctx3, cancel3 := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel3()

	history := []string{
		"Мы используем микросервисную архитектуру.",
		"Основной язык разработки — Go.",
	}
	p3 := "We also use Redis for caching and PostgreSQL for persistence."
	fmt.Printf("History: %v\n", history)
	fmt.Printf("EN: %s\n", p3)

	start := time.Now()
	tr3, err := prov.Translate(ctx3, p3, history)
	elapsed := time.Since(start)
	if err != nil {
		fmt.Printf("FAIL (%s): %v\n", elapsed.Round(time.Millisecond), err)
	} else {
		fmt.Printf("RU: %s  (%s)\n", tr3, elapsed.Round(time.Millisecond))
	}

	// ── 4. Answer Generation ──
	fmt.Println("\n═══ 4. Answer Generation ═══")
	ctx4, cancel4 := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel4()

	q := "What is the difference between a mutex and a channel in Go?"
	cv := "Senior Go developer, 5+ years, expert in concurrency patterns."
	fmt.Printf("Q: %s\n", q)

	start = time.Now()
	answers, err := prov.GenerateAnswers(ctx4, q, cv)
	elapsed = time.Since(start)
	if err != nil {
		fmt.Printf("FAIL (%s): %v\n", elapsed.Round(time.Millisecond), err)
	} else {
		for i, a := range answers {
			fmt.Printf("  %d. %s\n", i+1, a)
		}
		fmt.Printf("OK  (%s)\n", elapsed.Round(time.Millisecond))
	}

	fmt.Println("\n═══ Done ═══")
}
