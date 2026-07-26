package translator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

// TranslationResult holds the output of processing a question
// through the engine. It includes generated answer hints
// and whether the text was classified as a question.
//
// Answers are generated asynchronously — use GetAnswers() and
// SetAnswers() for safe concurrent access.
type TranslationResult struct {
	// Answers contains generated answer hints. Only populated when
	// IsQuestion is true. May be empty if answer generation is
	// still in progress (answers are generated asynchronously).
	answers   []string
	answersMu sync.Mutex

	// IsQuestion indicates whether the original text was classified
	// as a question.
	IsQuestion bool
}

// GetAnswers returns a copy of the answer hints. Safe for concurrent use.
func (r *TranslationResult) GetAnswers() []string {
	r.answersMu.Lock()
	defer r.answersMu.Unlock()
	out := make([]string, len(r.answers))
	copy(out, r.answers)
	return out
}

// SetAnswers replaces the answer hints. Safe for concurrent use.
func (r *TranslationResult) SetAnswers(a []string) {
	r.answersMu.Lock()
	defer r.answersMu.Unlock()
	r.answers = a
}

// questionWords is a set of English question-starting words and phrases
// used for heuristic question detection.
var questionWords = []string{
	"what", "how", "why", "when", "where", "who", "which",
	"can you", "could you", "would you", "will you", "do you",
	"have you", "did you", "are you", "is it", "is there",
	"explain", "describe", "tell me", "elaborate", "clarify",
	"share", "walk me", "talk about", "give me",
}

// TranslationEngine orchestrates question classification and answer
// generation. It delegates actual LLM calls to an LLMProvider.
//
// All methods are safe for concurrent use.
type TranslationEngine struct {
	llm       LLMProvider
	cvContext string
	mu        sync.RWMutex
}

// NewEngine creates a new TranslationEngine with the given LLM provider.
func NewEngine(llm LLMProvider) *TranslationEngine {
	return &TranslationEngine{llm: llm}
}

// SetCVContext задаёт контекст резюме для генерации подсказок.
func (e *TranslationEngine) SetCVContext(ctx string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cvContext = ctx
}

// ProcessQuestion processes a question text:
//  1. Classifies the text as a question or not.
//  2. If it is a question, launches answer generation in a goroutine.
//
// The returned TranslationResult contains the question classification.
// Answers may be empty if the text is not a question, or if answer
// generation is still in progress (the goroutine will update the result
// asynchronously).
func (e *TranslationEngine) ProcessQuestion(text string) (*TranslationResult, error) {
	// 1. Classify.
	isQuestion := IsQuestion(text)

	slog.Info("вопрос обработан",
		"text", text,
		"is_question", isQuestion,
	)

	result := &TranslationResult{
		IsQuestion: isQuestion,
	}

	// 2. Generate answers asynchronously if it's a question.
	if isQuestion {
		// Copy values for the goroutine to avoid data races.
		question := text
		go func() {
			slog.Info("генерация подсказок запущена", "question", question)

			// Use a background context so answer generation is not
			// tied to the request's lifecycle.
			bgCtx := context.Background()
			answers, genErr := e.llm.GenerateAnswers(bgCtx, question, "")
			if genErr != nil {
				slog.Error("генерация подсказок не удалась", "question", question, "error", genErr)
				return
			}
			slog.Info("подсказки сгенерированы", "question", question, "count", len(answers))
			result.SetAnswers(answers)
		}()
	}

	return result, nil
}

// IsQuestion performs a heuristic check to determine whether the given
// English text is a question. It checks for:
//   - A trailing question mark.
//   - Leading question words or phrases.
func IsQuestion(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}

	// Check for question mark.
	if strings.Contains(trimmed, "?") {
		return true
	}

	// Check for question words/phrases at the start.
	lower := strings.ToLower(trimmed)
	for _, qw := range questionWords {
		if strings.HasPrefix(lower, qw) {
			return true
		}
	}

	return false
}

// GenerateAnswers делегирует генерацию подсказок LLM-провайдеру.
func (e *TranslationEngine) GenerateAnswers(ctx context.Context, question string) ([]string, error) {
	e.mu.RLock()
	cvCtx := e.cvContext
	e.mu.RUnlock()
	return e.llm.GenerateAnswers(ctx, question, cvCtx)
}

// GenerateAnswersStream делегирует потоковую генерацию подсказок LLM-провайдеру.
// Токены доставляются по одному через возвращаемый канал.
// Канал закрывается по завершении генерации или при ошибке.
func (e *TranslationEngine) GenerateAnswersStream(ctx context.Context, question string) (<-chan string, error) {
	e.mu.RLock()
	cvCtx := e.cvContext
	e.mu.RUnlock()

	streamer, ok := e.llm.(StreamingAnswersProvider)
	if !ok {
		answers, err := e.llm.GenerateAnswers(ctx, question, cvCtx)
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
	return streamer.GenerateAnswersStream(ctx, question, cvCtx)
}
