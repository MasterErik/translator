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
	"explain", "describe", "tell me", "tell us", "elaborate", "clarify",
	"share", "share with us", "walk me", "walk us", "walk through",
	"talk about", "talk us through", "let's talk about", "give me",
	"introduce yourself", "introduce",
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
// Before those checks, negative guards (F12.1) reject known false positives —
// exclamatory and rhetorical openings that STT transcribes on par with
// questions ("What a mess…", "Why don't we move on"). Guards run BEFORE the
// question-mark check: an STT-produced "?" after an exclamation ("What a
// mess?") still is not an interview question.
//
// The leading-word check requires a word boundary after the matched phrase
// (end of string, space, apostrophe, or comma) so that "however", "whatever",
// "whoever" and "whatsapp" are not misclassified as questions.
func IsQuestion(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}

	lower := strings.ToLower(trimmed)

	// Negative guards (F12.1): exclamatory / rhetorical openings.
	if isNonQuestionOpening(lower) {
		return false
	}

	// Check for question mark.
	if strings.Contains(trimmed, "?") {
		return true
	}

	// Strip a leading polite "please" (optionally followed by a comma) so that
	// "Please tell us…", "Please, describe…" are detected as requests for an
	// answer. Only strips when "please" is followed by a word boundary.
	lower = stripLeadingPlease(lower)
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

// isNonQuestionOpening reports whether the lowercased text starts with a
// pattern that looks like a question-word opening but is an exclamation or a
// rhetorical suggestion, not a request for an answer (F12.1). Calibrated
// against real STT false positives; add new patterns with a sessLog example
// in the comment.
func isNonQuestionOpening(lower string) bool {
	// "What a mess…", "What an achievement…", "What the heck…" — exclamatory
	// openings. Interview questions never start with "what a/an/the".
	for _, excl := range []string{"what a ", "what an ", "what the "} {
		if strings.HasPrefix(lower, excl) {
			return true
		}
	}
	// "Why don't we move on / try / start…" — a suggestion to act, not a
	// question. "Why don't you…" stays a question (real interview probe).
	// Both apostrophe and apostrophe-less STT variants are covered.
	for _, sugg := range []string{"why don't we ", "why dont we "} {
		if strings.HasPrefix(lower, sugg) {
			return true
		}
	}
	return false
}

// stripLeadingPlease removes a leading polite "please" (optionally followed by
// a comma and/or whitespace) so that "Please tell us…" and "Please, describe…"
// are classified as requests for an answer. It only strips when "please" ends
// at a word boundary, so "pleasing" and "please" alone stay untouched.
func stripLeadingPlease(s string) string {
	const please = "please"
	if !strings.HasPrefix(s, please) {
		return s
	}
	rest := s[len(please):]
	if rest == "" || (rest[0] != ' ' && rest[0] != '	' && rest[0] != ',') {
		return s
	}
	return strings.TrimLeft(rest, " 	,")
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
