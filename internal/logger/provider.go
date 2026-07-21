// Package logger defines the session logging interface.
// Implementations persist transcribed text and raw audio chunks
// for post-session review and debugging.
package logger

import "github.com/mastererik/translator/internal/common"

// SessionLogger is the interface for session recording and persistence.
// It logs STT events as structured records and saves raw PCM audio
// chunks for each audio channel.
type SessionLogger interface {
	// LogText records an STT transcription event to the session log.
	// Events from different channels ("speaker", "mic") are all written
	// to the same session file, typically in JSON Lines format.
	LogText(event common.STTEvent) error

	// SaveAudioChunk appends a raw PCM audio chunk to the file for the
	// given channelID ("speaker" or "mic"). Chunks are written
	// sequentially to preserve timing.
	SaveAudioChunk(channelID string, pcm []byte) error

	// Close flushes all buffered data to disk and closes open file handles.
	// Must be called during graceful shutdown to avoid data loss.
	Close() error
}
