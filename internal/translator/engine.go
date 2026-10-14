package translator

import (
	"context"
	"fmt"
	"strings"
)

// questionWords is a set of English question-starting words and phrases
// used for heuristic question detection.
var questionWords = []string{
	"what", "how", "why", "when", "where", "who", "which",
	"can you", "could you", "would you", "will you", "do you",
	"have you", "did you", "are you", "is it", "is there",
	"explain", "describe", "tell me", "elaborate", "clarify",
	"share", "walk me", "talk about", "let's talk about", "give me",
}

// TranslationEngine orchestrates question classification and answer
// generation. It delegates actual LLM calls to an LLMProvider.
//
// All methods are safe for concurrent use.
type TranslationEngine struct {
	llm LLMProvider
}

// NewEngine creates a new TranslationEngine with the given LLM provider.
func NewEngine(llm LLMProvider) *TranslationEngine {
	return &TranslationEngine{llm: llm}
}

// IsQuestion performs a heuristic check to determine whether the given
// English text is a question. It checks for:
//   - A trailing question mark.
//   - Leading question words or phrases, terminated by a word boundary.
//
// The leading-word check requires a word boundary after the matched phrase
// (end of string, space, apostrophe, or comma) so that "however", "whatever",
// "whoever" and "whatsapp" are not misclassified as questions.
func IsQuestion(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}

	// Check for question mark.
	if strings.Contains(trimmed, "?") {
		return true
	}

	// Check for question words/phrases at the start, followed by a word
	// boundary so that longer words sharing the prefix are not matched.
	lower := strings.ToLower(trimmed)
	for _, qw := range questionWords {
		if !strings.HasPrefix(lower, qw) {
			continue
		}
		rest := lower[len(qw):]
		if rest == "" || rest[0] == ' ' || rest[0] == '\'' || rest[0] == ',' {
			return true
		}
	}

	return false
}

// GenerateAnswers делегирует генерацию подсказок LLM-провайдеру.
func (e *TranslationEngine) GenerateAnswers(ctx context.Context, req AnswerRequest) ([]string, error) {
	return e.llm.GenerateAnswers(ctx, req)
}

// GenerateAnswersStream делегирует потоковую генерацию подсказок LLM-провайдеру.
// Токены доставляются по одному через возвращаемый канал.
// Канал закрывается по завершении генерации или при ошибке.
func (e *TranslationEngine) GenerateAnswersStream(ctx context.Context, req AnswerRequest) (<-chan string, error) {
	streamer, ok := e.llm.(StreamingAnswersProvider)
	if !ok {
		answers, err := e.llm.GenerateAnswers(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("generate answers stream: %w", err)
		}
		ch := make(chan string, len(answers))
		for _, a := range answers {
			ch <- a
		}
		close(ch)
		return ch, nil
	}
	return streamer.GenerateAnswersStream(ctx, req)
}
