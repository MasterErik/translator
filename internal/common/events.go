package common

import "time"

// STTEvent represents a speech-to-text transcription result.
// It is emitted by an STTProvider for both interim (partial) and final
// transcriptions from either the speaker loopback channel or the microphone.
type STTEvent struct {
	// Text is the transcribed text content.
	Text string

	// IsFinal indicates whether this is a final (complete) utterance
	// or an interim (partial) result that may change.
	IsFinal bool

	// ChannelID identifies the audio source: "speaker" for loopback
	// capture or "mic" for the local microphone.
	ChannelID string

	// Timestamp records when the transcription was received.
	Timestamp time.Time

	// Error holds any error that occurred during transcription.
	// When non-nil, Text may be empty and IsFinal should be true.
	Error error
}

// UIEventType represents the kind of UI event being sent to the overlay.
type UIEventType string

const (
	// UIEventTranslation is a translated text to display as subtitles.
	UIEventTranslation UIEventType = "Translation"

	// UIEventAnswer is a single generated answer to a detected question.
	UIEventAnswer UIEventType = "Answer"

	// UIEventAnswerCandidates is a set of candidate answers to a detected question.
	UIEventAnswerCandidates UIEventType = "AnswerCandidates"
)

// UIEvent represents a message sent from the translation engine to the
// GioUI overlay. It carries translated text, generated answers, or
// answer candidates depending on its Type.
type UIEvent struct {
	// Type classifies the event (Translation, Answer, or AnswerCandidates).
	Type UIEventType

	// Text is the translated text (for Translation events) or the
	// original question text (for Answer/AnswerCandidates events).
	Text string

	// Answers contains the list of generated answer candidates.
	// Only populated for AnswerCandidates events.
	Answers []string

	// Timestamp records when the event was created.
	Timestamp time.Time
}
