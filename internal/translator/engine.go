package translator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

// TranslationResult holds the output of processing a final transcript
// through the translation engine. It includes the translated text,
// any generated answer hints (if the text was detected as a question),
// and a flag indicating whether the text was classified as a question.
//
// Answers are generated asynchronously — use GetAnswers() and
// SetAnswers() for safe concurrent access.
type TranslationResult struct {
	// Translation is the Russian translation of the original text.
	Translation string

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

// TranslationEngine orchestrates the translation and question-classification
// pipeline. It maintains a sliding window of recent translated utterances
// for context and delegates actual LLM calls to an LLMProvider.
//
// All methods are safe for concurrent use.
type TranslationEngine struct {
	llm       LLMProvider
	window    []string
	maxWindow int
	mu        sync.RWMutex
}

// NewEngine creates a new TranslationEngine with the given LLM provider
// and sliding window size. If maxWindow is 0 or negative, it defaults to 5.
func NewEngine(llm LLMProvider, maxWindow int) *TranslationEngine {
	if maxWindow <= 0 {
		maxWindow = 5
	}
	return &TranslationEngine{
		llm:       llm,
		window:    make([]string, 0, maxWindow),
		maxWindow: maxWindow,
	}
}

// ProcessFinalTranscriptStream обрабатывает финальную транскрипцию
// с потоковой передачей токенов перевода. Токены отправляются в канал
// по мере генерации — UI обновляется инкрементально без ожидания полного ответа.
//
// Канал закрывается после завершения перевода. После закрытия канала
// нужно проверить IsQuestion и при необходимости запустить генерацию подсказок.
func (e *TranslationEngine) ProcessFinalTranscriptStream(ctx context.Context, text string) (<-chan string, error) {
	// 1. Add to sliding window.
	e.addToWindow(text)

	// 2. Get window history for context.
	history := e.getWindowHistory()

	// 3. Check if streaming is supported.
	streamer, ok := e.llm.(StreamingTranslator)
	if !ok {
		// Fallback: синхронный перевод через канал из одного элемента.
		ch := make(chan string, 1)
		translation, err := e.llm.Translate(ctx, text, history)
		if err != nil {
			close(ch)
			return nil, fmt.Errorf("process final transcript stream: %w", err)
		}
		ch <- translation
		close(ch)
		return ch, nil
	}

	return streamer.TranslateStream(ctx, text, history)
}

// ProcessFinalTranscript processes a final (non-interim) transcript:
//  1. Adds the text to the sliding window history.
//  2. Calls the LLM provider to translate the text (EN→RU).
//  3. Classifies the text as a question or not.
//  4. If it is a question, launches answer generation in a goroutine.
//
// The returned TranslationResult always contains the translation and
// question classification. Answers may be empty if the text is not a
// question, or if answer generation is still in progress (the goroutine
// will update the result asynchronously via the answers channel, if needed).
func (e *TranslationEngine) ProcessFinalTranscript(ctx context.Context, text string) (*TranslationResult, error) {
	// 1. Add to sliding window.
	e.addToWindow(text)

	// 2. Get window history for context.
	history := e.getWindowHistory()

	// 3. Translate.
	translation, err := e.llm.Translate(ctx, text, history)
	if err != nil {
		slog.Error("перевод не удался", "text", text, "error", err)
		return nil, fmt.Errorf("process final transcript: %w", err)
	}

	// 4. Classify.
	isQuestion := IsQuestion(text)

	slog.Info("транскрипт обработан",
		"text", text,
		"translation", translation,
		"is_question", isQuestion,
		"window_size", len(history)+1,
	)

	result := &TranslationResult{
		Translation: translation,
		IsQuestion:  isQuestion,
	}

	// 5. Generate answers asynchronously if it's a question.
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

// addToWindow appends text to the sliding window, trimming the oldest
// entries if the window exceeds maxWindow.
func (e *TranslationEngine) addToWindow(text string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.window = append(e.window, text)
	if len(e.window) > e.maxWindow {
		e.window = e.window[len(e.window)-e.maxWindow:]
	}
}

// getWindowHistory returns a copy of the current sliding window contents,
// excluding the most recent entry (which is the text currently being
// translated).
func (e *TranslationEngine) getWindowHistory() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	// Return all but the last entry as history.
	if len(e.window) <= 1 {
		return nil
	}

	history := make([]string, len(e.window)-1)
	copy(history, e.window[:len(e.window)-1])
	return history
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
	return e.llm.GenerateAnswers(ctx, question, "")
}

// GenerateAnswersStream делегирует потоковую генерацию подсказок LLM-провайдеру.
// Токены доставляются по одному через возвращаемый канал.
// Канал закрывается по завершении генерации или при ошибке.
func (e *TranslationEngine) GenerateAnswersStream(ctx context.Context, question string) (<-chan string, error) {
	streamer, ok := e.llm.(StreamingTranslator)
	if !ok {
		// Fallback: синхронная генерация через канал из N элементов.
		answers, err := e.llm.GenerateAnswers(ctx, question, "")
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
	return streamer.GenerateAnswersStream(ctx, question, "")
}
