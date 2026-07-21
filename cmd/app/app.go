// Package main wires all Translator modules together with graceful shutdown.
//
// This file contains the shared types, overlay stub, and pipeline helper
// functions used by both the real main() entry point (main.go, requires cgo)
// and the test suite (main_test.go, always compiles).
package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/mastererik/translator/internal/logger"
	"github.com/mastererik/translator/internal/stt"
	"github.com/mastererik/translator/internal/translator"
)

// --------------------------------------------------------------------------
// capturer interface — decouples the pipeline from concrete capture types
// --------------------------------------------------------------------------

// capturer abstracts dual-channel audio capture so that the pipeline
// helper functions work with both capture.Capture (real, requires cgo)
// and capture.StubCapture (test stub, no cgo required).
type capturer interface {
	Start(ctx context.Context) (<-chan []byte, <-chan []byte, error)
}

// --------------------------------------------------------------------------
// Overlay stub (replaces internal/ui until GioUI dependency is added)
// --------------------------------------------------------------------------

// OverlayConfig holds configuration for the overlay window.
type OverlayConfig struct {
	Title        string
	Width        int
	Height       int
	FontSize     int
	TopZoneRatio float64
}

// MsgType classifies overlay messages.
type MsgType string

const (
	MsgTranslation      MsgType = "Translation"
	MsgAnswerCandidates MsgType = "AnswerCandidates"
)

// OverlayMsg is a single message displayed in the overlay.
type OverlayMsg struct {
	Type      MsgType
	Text      string
	Answers   []string
	Timestamp time.Time
}

// Overlay is a stub overlay that collects messages in memory for testing.
// In production, internal/ui.Overlay renders a two-zone GioUI window.
type Overlay struct {
	mu       sync.Mutex
	messages []OverlayMsg
	shutdown chan struct{}
}

// NewOverlay creates a new stub overlay with sensible defaults.
func NewOverlay(cfg OverlayConfig) *Overlay {
	if cfg.FontSize <= 0 {
		cfg.FontSize = 18
	}
	if cfg.TopZoneRatio <= 0 || cfg.TopZoneRatio > 1 {
		cfg.TopZoneRatio = 0.6
	}
	if cfg.Title == "" {
		cfg.Title = "Translator Overlay"
	}
	return &Overlay{
		messages: make([]OverlayMsg, 0),
		shutdown: make(chan struct{}),
	}
}

// AddMessage appends a message to the overlay buffer. Safe for concurrent use.
func (o *Overlay) AddMessage(msg OverlayMsg) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.messages = append(o.messages, msg)
}

// GetMessages returns a copy of all buffered messages. Safe for concurrent use.
func (o *Overlay) GetMessages() []OverlayMsg {
	o.mu.Lock()
	defer o.mu.Unlock()
	result := make([]OverlayMsg, len(o.messages))
	copy(result, o.messages)
	return result
}

// Run blocks until ctx is cancelled, then signals shutdown. In the real
// overlay this runs a GioUI event loop; here it's a no-op waiter.
func (o *Overlay) Run(ctx context.Context) error {
	defer close(o.shutdown)
	<-ctx.Done()
	return ctx.Err()
}

// WaitShutdown blocks until the overlay has finished shutting down.
func (o *Overlay) WaitShutdown() {
	<-o.shutdown
}

// --------------------------------------------------------------------------
// Pipeline helper functions
// --------------------------------------------------------------------------

// runCapture starts dual-channel audio capture and routes PCM data to
// the STT provider's audio stream and the session logger's audio dump.
// It returns when ctx is cancelled; capture channels are drained by the
// capture implementation.
func runCapture(ctx context.Context, cap capturer, sttProv stt.STTProvider, sessLog logger.SessionLogger) {
	loopbackCh, micCh, err := cap.Start(ctx)
	if err != nil {
		slog.Error("failed to start audio capture", "error", err)
		return
	}

	audioStream := sttProv.AudioStream()

	// Route loopback (speaker) audio.
	go routeAudioChannel(ctx, loopbackCh, audioStream, "speaker", sessLog)

	// Route microphone audio.
	go routeAudioChannel(ctx, micCh, audioStream, "mic", sessLog)

	// Wait for context cancellation; capture will clean up via its own
	// context-aware shutdown goroutines.
	<-ctx.Done()
	slog.Info("capture pipeline stopped")
}

// routeAudioChannel reads PCM chunks from src and forwards them to dst.
// It also saves each chunk to the session logger with the given channel ID.
// The goroutine exits when src is closed or ctx is cancelled.
func routeAudioChannel(ctx context.Context, src <-chan []byte, dst chan<- []byte, channelID string, sessLog logger.SessionLogger) {
	for {
		select {
		case <-ctx.Done():
			return
		case chunk, ok := <-src:
			if !ok {
				return
			}
			// Forward to STT provider (non-blocking; drop under backpressure).
			select {
			case dst <- chunk:
			case <-ctx.Done():
				return
			default:
				// Audio stream buffer full; drop frame to avoid blocking.
			}

			// Save to session log (best-effort, non-blocking).
			if err := sessLog.SaveAudioChunk(channelID, chunk); err != nil {
				slog.Warn("failed to save audio chunk", "channel", channelID, "error", err)
			}
		}
	}
}

// runSTT reads STTEvent transcriptions from the provider and routes them:
//   - Interim events are sent to the overlay for low-latency preview.
//   - Final events are processed through the translation engine, logged,
//     and sent to the overlay as translation + answer candidates.
func runSTT(ctx context.Context, sttProv stt.STTProvider, engine *translator.TranslationEngine, overlay *Overlay, sessLog logger.SessionLogger) {
	textStream := sttProv.TextStream()

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-textStream:
			if !ok {
				return
			}

			// Handle error-bearing events.
			if event.Error != nil {
				slog.Warn("stt event error", "error", event.Error)
				continue
			}

			// Log all text events.
			if err := sessLog.LogText(event); err != nil {
				slog.Warn("failed to log text event", "error", err)
			}

			if !event.IsFinal {
				// Interim: show low-latency preview in overlay.
				overlay.AddMessage(OverlayMsg{
					Type:      MsgTranslation,
					Text:      event.Text,
					Timestamp: event.Timestamp,
				})
				continue
			}

			// Final: translate and classify.
			result, err := engine.ProcessFinalTranscript(ctx, event.Text)
			if err != nil {
				slog.Error("translation failed", "error", err, "text", event.Text)
				continue
			}

			// Send translation to overlay.
			overlay.AddMessage(OverlayMsg{
				Type:      MsgTranslation,
				Text:      result.Translation,
				Timestamp: event.Timestamp,
			})

			// If the utterance was classified as a question, launch a
			// goroutine to wait for async answer generation to complete
			// and then send the candidates to the overlay.
			// TODO: Replace polling with a channel-based approach when
			// the engine is updated to send answers via a channel.
			if result.IsQuestion {
				resultCopy := result
				eventCopy := event
				go func() {
					// Poll for async answer generation to complete.
					// The engine spawns a goroutine in ProcessFinalTranscript
					// that writes answers to result.Answers after the call returns.
					for i := 0; i < 10; i++ {
						if len(resultCopy.Answers) > 0 {
							overlay.AddMessage(OverlayMsg{
								Type:      MsgAnswerCandidates,
								Text:      eventCopy.Text,
								Answers:   resultCopy.Answers,
								Timestamp: eventCopy.Timestamp,
							})
							return
						}
						select {
						case <-ctx.Done():
							return
						case <-time.After(100 * time.Millisecond):
						}
					}
					slog.Warn("answer generation timed out", "question", eventCopy.Text)
				}()
			}
		}
	}
}

// runUI starts the overlay event loop and blocks until ctx is cancelled
// or the overlay window is destroyed.
func runUI(ctx context.Context, overlay *Overlay) {
	slog.Info("ui overlay started")
	if err := overlay.Run(ctx); err != nil {
		slog.Info("ui overlay stopped", "reason", err)
	}
}
