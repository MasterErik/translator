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

// StreamingTranslator extends LLMProvider with streaming translation.
// Tokens are delivered one-by-one through a channel, enabling
// incremental UI updates (typewriter effect) and reducing perceived latency.
//
// The channel is closed when translation is complete or on error.
// Implementations must ensure the channel is always closed.
type StreamingTranslator interface {
	LLMProvider

	// TranslateStream translates text with streaming output.
	// Returns a channel that receives translation tokens one at a time.
	// The last value on the channel is the complete translation,
	// followed by channel close.
	TranslateStream(ctx context.Context, text string, history []string) (<-chan string, error)

	// GenerateAnswersStream генерирует подсказки потоково.
	// Возвращает канал с токенами ответа. Канал закрывается
	// по завершении генерации или при ошибке.
	GenerateAnswersStream(ctx context.Context, question string, cvContext string) (<-chan string, error)
}
