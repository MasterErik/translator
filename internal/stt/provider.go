// Package stt defines the speech-to-text provider interface.
// Implementations convert raw PCM audio into STTEvent transcriptions.
package stt

import (
	"context"

	"github.com/mastererik/translator/internal/common"
)

// STTProvider is the interface for speech-to-text engines.
// Implementations receive 16kHz mono PCM audio and emit STTEvent
// transcriptions for both interim and final results.
//
// Implementations:
//   - GladiaProvider: Gladia Live WebSocket API (Solaria-1 + Translation).
type STTProvider interface {
	// Start begins audio processing. It establishes any connections
	// (e.g. WebSocket) and starts background workers.
	Start(ctx context.Context) error

	// Stop gracefully shuts down the provider. It closes connections,
	// drains internal queues, and stops background goroutines.
	Stop() error

	// AudioStream returns a send-only channel for feeding 16kHz mono
	// PCM audio data to the STT engine. Callers should close this
	// channel when no more audio will be sent.
	AudioStream() chan<- []byte

	// TextStream returns a receive-only channel from which STTEvent
	// transcriptions can be read. The channel is closed when the
	// provider is stopped.
	TextStream() <-chan common.STTEvent
}
