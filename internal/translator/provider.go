// Package translator defines the LLM provider interface used by the
// translation engine to produce translations and generate interview answers.
package translator

import "context"

// LLMProvider is the interface for language model backends.
// It provides translation and answer generation capabilities
// used by the translation engine.
//
// Implementations:
//   - OpenAIProvider (current): GPT-4o-mini via go-openai.
//   - Future: local models (Ollama, llama.cpp).
type LLMProvider interface {
	// Translate translates the given text to the target language,
	// using the provided conversation history for context.
	// history contains previously translated utterances.
	Translate(ctx context.Context, text string, history []string) (string, error)

	// GenerateAnswers generates candidate answers to a detected question,
	// optionally incorporating CV/resume context for personalization.
	// cvContext may be empty.
	GenerateAnswers(ctx context.Context, question string, cvContext string) ([]string, error)
}
