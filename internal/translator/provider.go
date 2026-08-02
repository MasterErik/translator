// Package translator defines the LLM provider interface used by the
// engine to generate interview answers.
package translator

import "context"

// LLMProvider is the interface for language model backends.
// It provides answer generation capabilities used by the engine.
//
// Implementations:
//   - ChatProvider (current): OpenAI-compatible API via go-openai.
//   - Future: local models (Ollama, llama.cpp).
type LLMProvider interface {
	// GenerateAnswers generates candidate answers to a detected question,
	// optionally incorporating CV/resume context for personalization.
	// cvContext may be empty.
	GenerateAnswers(ctx context.Context, question string, cvContext string) ([]string, error)
}

// StreamingAnswersProvider extends LLMProvider with streaming answer generation.
// Tokens are delivered one-by-one through a channel, enabling
// incremental UI updates (typewriter effect) and reducing perceived latency.
//
// The channel is closed when generation is complete or on error.
// Implementations must ensure the channel is always closed.
type StreamingAnswersProvider interface {
	LLMProvider

	// GenerateAnswersStream генерирует подсказки потоково.
	// Возвращает канал с токенами ответа. Канал закрывается
	// по завершении генерации или при ошибке.
	GenerateAnswersStream(ctx context.Context, question string, cvContext string) (<-chan string, error)
}
