//go:build integration

package translator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"
)

// =========================================================================
// LLM Benchmark Framework
// =========================================================================
//
// Общие функции для интеграционного тестирования и сравнения
// любых OpenAI-совместимых LLM-провайдеров.
//
// Используется:
//   - groq_integration_test.go  (Groq Cloud API)
//   - openai_integration_test.go (Z.AI GLM / OpenAI)
// =========================================================================

const defaultLatencyRuns = 5 // количество замеров для статистики

// ── Типы ──────────────────────────────────────────────────────────────────

// benchConfig описывает конфигурацию провайдера для бенчмарка.
type benchConfig struct {
	Name      string        // отображаемое имя (напр. "Groq", "LLM (GLM)")
	BaseURL   string
	APIKey    string
	Model     string
	Pause     time.Duration // пауза между запросами
}

// chatResponse — сырой ответ chat/completions.
type chatResponse struct {
	Content          string
	ReasoningContent string
}

// latencyStats — агрегированная статистика замеров.
type latencyStats struct {
	Min    time.Duration
	Max    time.Duration
	Mean   time.Duration
	Median time.Duration
}

// ── HTTP-запрос ───────────────────────────────────────────────────────────

// sendChatRequest отправляет запрос к chat/completions и возвращает
// полное тело ответа. Если disableThinking=true, добавляет поле thinking.
func sendChatRequest(t *testing.T, baseURL, apiKey, model, question string, disableThinking bool) string {
	t.Helper()

	body := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": SystemPromptAnswerGen},
			{"role": "user", "content": BuildAnswerPrompt(question)},
		},
		"temperature": 0.3,
	}
	if disableThinking {
		body["thinking"] = map[string]string{"type": "disabled"}
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	url := strings.TrimRight(baseURL, "/") + "/chat/completions"
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(jsonBody)))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("send request: %v", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("API error %d: %s", resp.StatusCode, string(respBytes))
	}

	return string(respBytes)
}

// ── Парсинг ───────────────────────────────────────────────────────────────

// parseChatResponse разбирает JSON ответа chat/completions.
func parseChatResponse(t *testing.T, rawJSON string) chatResponse {
	t.Helper()

	var result struct {
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(rawJSON), &result); err != nil {
		t.Fatalf("Failed to parse response: %v\nRaw: %s", err, rawJSON)
	}

	if len(result.Choices) == 0 {
		return chatResponse{}
	}

	return chatResponse{
		Content:          result.Choices[0].Message.Content,
		ReasoningContent: result.Choices[0].Message.ReasoningContent,
	}
}

// ── Бенчмарки ─────────────────────────────────────────────────────────────

// runLatencyBench выполняет N запросов и собирает замеры latency.
// Ошибки rate-limit логируются, но не прерывают тест.
func runLatencyBench(t *testing.T, cfg benchConfig, question string, runs int) []time.Duration {
	t.Helper()

	var latencies []time.Duration
	provider := NewChatProvider(cfg.BaseURL, cfg.APIKey, cfg.Model)
	errors := 0

	t.Log("")
	t.Logf("--- %s ---", cfg.Name)
	for i := 0; i < runs; i++ {
		start := time.Now()
		answers, err := provider.GenerateAnswers(context.Background(), question, "")
		elapsed := time.Since(start)

		if err != nil {
			errors++
			if isRateLimitError(err) {
				t.Logf("  Run %d: RATE-LIMITED (429) — skipping, %s", i+1, elapsed)
			} else {
				t.Logf("  Run %d: ERROR: %v (skipping)", i+1, err)
			}
		} else {
			latencies = append(latencies, elapsed)
			t.Logf("  Run %d: %v (answers: %d)", i+1, elapsed, len(answers))
		}

		if i < runs-1 {
			time.Sleep(cfg.Pause)
		}
	}

	if errors > 0 {
		t.Logf("  ⚠️  %d/%d runs failed for %s", errors, runs, cfg.Name)
	}

	return latencies
}

// benchCompare сравнивает latency двух провайдеров.
func benchCompare(t *testing.T, a, b benchConfig, question string, runs int) {
	t.Helper()

	t.Logf("============================================================")
	t.Logf("Latency Comparison: %s (%s) vs %s (%s)", a.Name, a.Model, b.Name, b.Model)
	t.Logf("Runs per provider: %d", runs)
	t.Logf("============================================================")

	aLat := runLatencyBench(t, a, question, runs)
	bLat := runLatencyBench(t, b, question, runs)
	aStats := computeLatencyStats(aLat)
	bStats := computeLatencyStats(bLat)

	printComparisonTable(t, a.Name, b.Name, len(aLat), len(bLat), aStats, bStats)

	if len(aLat) > 0 && len(bLat) > 0 {
		ratio := float64(aStats.Mean) / float64(bStats.Mean)
		t.Logf("%s / %s ratio: %.2fx", a.Name, b.Name, ratio)
		if ratio < 1.0 {
			t.Logf("✅ %s is %.1fx FASTER than %s", a.Name, 1.0/ratio, b.Name)
		} else {
			t.Logf("⚠️  %s is %.1fx SLOWER than %s", a.Name, ratio, b.Name)
		}
	}
}

// benchAllModels тестирует список моделей одного провайдера.
func benchAllModels(t *testing.T, baseURL, apiKey, question string, models []string) {
	t.Helper()

	t.Logf("============================================================")
	t.Logf("All Models Comparison")
	t.Logf("Question: %q", question)
	t.Logf("============================================================")
	t.Logf("%-30s %12s %10s %s", "Model", "Latency", "Chars", "Preview")
	t.Logf("%-30s %12s %10s %s", "------------------------------", "------------", "----------", "-------")

	for _, model := range models {
		provider := NewChatProvider(baseURL, apiKey, model)

		start := time.Now()
		answers, err := provider.GenerateAnswers(context.Background(), question, "")
		elapsed := time.Since(start)

		if err != nil {
			t.Logf("%-30s %12s %10s ERROR: %v", model, "—", "—", err)
			continue
		}

		preview := "—"
		charCount := 0
		if len(answers) > 0 {
			preview = truncate(answers[0], 50)
			charCount = len(answers[0])
		}

		t.Logf("%-30s %12v %10d %q", model, elapsed, charCount, preview)
		time.Sleep(500 * time.Millisecond)
	}
	t.Log("============================================================")
}

// benchSingle тестирует базовую генерацию через один провайдер.
func benchSingle(t *testing.T, baseURL, apiKey, model, question string) {
	t.Helper()

	t.Logf("=== Provider Test ===")
	t.Logf("Model: %s", model)
	t.Logf("Endpoint: %s", baseURL)

	t.Run("raw_api", func(t *testing.T) {
		start := time.Now()
		rawJSON := sendChatRequest(t, baseURL, apiKey, model, question, false)
		elapsed := time.Since(start)

		parsed := parseChatResponse(t, rawJSON)
		t.Logf("Latency: %v", elapsed)
		t.Logf("Content length: %d chars", len(parsed.Content))
		t.Logf("Content preview: %q", truncate(parsed.Content, 200))

		if parsed.Content == "" {
			t.Fatal("Provider returned empty content")
		}
	})

	t.Run("parsed_answers", func(t *testing.T) {
		provider := NewChatProvider(baseURL, apiKey, model)

		start := time.Now()
		answers, err := provider.GenerateAnswers(context.Background(), question, "")
		elapsed := time.Since(start)

		if err != nil {
			t.Fatalf("GenerateAnswers error: %v", err)
		}

		t.Logf("Latency: %v", elapsed)
		t.Logf("Parsed answers: %d", len(answers))
		for i, a := range answers {
			t.Logf("  [%d] %q", i, a)
		}

		if len(answers) == 0 {
			t.Fatal("Provider returned empty answers after parsing")
		}
	})
}

// ── Статистика ────────────────────────────────────────────────────────────

// computeLatencyStats вычисляет статистику по срезу длительностей.
func computeLatencyStats(latencies []time.Duration) latencyStats {
	if len(latencies) == 0 {
		return latencyStats{}
	}

	sorted := make([]time.Duration, len(latencies))
	copy(sorted, latencies)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	var total time.Duration
	for _, l := range sorted {
		total += l
	}

	return latencyStats{
		Min:    sorted[0],
		Max:    sorted[len(sorted)-1],
		Mean:   total / time.Duration(len(sorted)),
		Median: sorted[len(sorted)/2],
	}
}

// printComparisonTable выводит таблицу сравнения двух провайдеров.
func printComparisonTable(t *testing.T, nameA, nameB string, countA, countB int, a, b latencyStats) {
	t.Helper()

	t.Log("")
	t.Log("============================================================")
	t.Log("                    LATENCY COMPARISON")
	t.Log("============================================================")
	t.Logf("%-20s %12s %12s", "Metric", nameA, nameB)
	t.Logf("%-20s %12s %12s", "--------------------", "------------", "------------")
	t.Logf("%-20s %12v %12v", "Count", countA, countB)
	t.Logf("%-20s %12v %12v", "Min", a.Min, b.Min)
	t.Logf("%-20s %12v %12v", "Max", a.Max, b.Max)
	t.Logf("%-20s %12v %12v", "Mean", a.Mean, b.Mean)
	t.Logf("%-20s %12v %12v", "Median", a.Median, b.Median)
	t.Log("============================================================")
}

// ── Утилиты ───────────────────────────────────────────────────────────────

// truncate обрезает строку до maxLen символов.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// dumpRawResponse печатает сырой JSON ответа (для диагностики).
func dumpRawResponse(label string, rawJSON string) {
	fmt.Printf("=== %s ===\n%s\n", label, rawJSON)
}
