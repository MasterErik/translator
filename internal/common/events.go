package common

import "time"

// Event — строковые константы для поля STTEvent.Event.
const (
	// EventUpdate — промежуточный (interim) результат распознавания.
	EventUpdate = "Update"

	// EventEndOfTurn — финальный результат распознавания (конец фразы).
	EventEndOfTurn = "EndOfTurn"

	// EventEagerEndOfTurn — досрочный финальный результат (EagerEndOfTurn).
	EventEagerEndOfTurn = "EagerEndOfTurn"

	// EventError — событие с ошибкой.
	EventError = "Error"
)

// STTEvent represents a speech-to-text transcription result.
// It is emitted by an STTProvider for both interim (partial) and final
// transcriptions from either the speaker loopback channel or the microphone.
type STTEvent struct {
	// Text is the transcribed text content.
	Text string

	// Event specifies the type of transcription event.
	// Possible values: "Update" (interim), "EndOfTurn" (final),
	// "EagerEndOfTurn" (early final), "Error".
	// Use constants EventUpdate, EventEndOfTurn, EventEagerEndOfTurn, EventError.
	Event string

	// ChannelID identifies the audio source: "speaker" for loopback
	// capture or "mic" for the local microphone.
	ChannelID string

	// Timestamp records when the transcription was received.
	Timestamp time.Time

	// Error holds any error that occurred during transcription.
	// When non-nil, Text may be empty and Event should be EventError.
	Error error
}
