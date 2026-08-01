//go:build integration

package translator

import (
	"os"
	"testing"
)

// =========================================================================
// Groq Cloud API Integration Tests
// =========================================================================
//
// Groq Cloud предлагает постоянный бесплатный тир (Developer Plan) с доступом к:
//   - llama-3.1-8b-instant     (560 tps, default)
//   - llama-3.3-70b-versatile  (280 tps)
//   - openai/gpt-oss-20b       (1000 tps)
//   - openai/gpt-oss-120b      (500 tps)
//
// API endpoint: https://api.groq.com/openai/v1/chat/completions
// OpenAI-совместимый формат запросов.
//
// Использует основные переменные LLM (Groq — основной провайдер):
//   LLM_API_KEY  — API ключ из https://console.groq.com/keys
//   LLM_MODEL — модель (по умолчанию llama-3.1-8b-instant)
// =========================================================================

const (
	groqBaseURL      = "https://api.groq.com/openai/v1"
	defaultGroqModel = "llama-3.1-8b-instant"
)

// TestIntegration_GroqGenerateAnswers тестирует базовую генерацию ответов
// через Groq Cloud API с замером времени.
func TestIntegration_GroqGenerateAnswers(t *testing.T) {
	apiKey := os.Getenv("LLM_API_KEY")
	model := os.Getenv("LLM_MODEL")

	if apiKey == "" {
		t.Skip("LLM_API_KEY not set, skipping Groq integration test")
	}
	if model == "" {
		model = defaultGroqModel
	}

	benchSingle(t, groqBaseURL, apiKey, model, "What is Redis?")
}

// TestIntegration_GroqAllModels тестирует все доступные production-модели Groq
// и выводит сравнительную таблицу.
func TestIntegration_GroqAllModels(t *testing.T) {
	apiKey := os.Getenv("LLM_API_KEY")
	if apiKey == "" {
		t.Skip("LLM_API_KEY not set, skipping all-models test")
	}

	models := []string{
		"llama-3.1-8b-instant",
		"llama-3.3-70b-versatile",
		"openai/gpt-oss-20b",
		"openai/gpt-oss-120b",
	}

	benchAllModels(t, groqBaseURL, apiKey, "What is Redis?", models)
}
