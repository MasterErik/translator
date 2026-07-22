package stt

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/mastererik/translator/internal/common"
)

// maxReconnectAttempts is the maximum number of reconnection attempts before giving up.
const maxReconnectAttempts = 10

// initialBackoff is the starting backoff duration for exponential backoff.
const initialBackoff = 200 * time.Millisecond

// maxBackoff caps the maximum backoff duration.
const maxBackoff = 30 * time.Second

// writeTimeout is the deadline for writing a message to the WebSocket.
const writeTimeout = 5 * time.Second

// DeepgramProvider implements STTProvider using the Deepgram real-time WebSocket API.
// It opens a WebSocket connection, streams raw 16kHz mono PCM audio, and emits
// STTEvent transcription results on a channel.
type DeepgramProvider struct {
	apiKey  string
	model   string
	wsConn  *websocket.Conn
	audioCh chan []byte
	textCh  chan common.STTEvent
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
}

// NewDeepgramProvider creates a new DeepgramProvider with the given API key and model.
// The model parameter selects the Deepgram model (e.g., "flux-general-en").
// Note: /v2/listen (Flux) only supports flux-general-en for streaming.
// Start must be called to begin the transcription session.
func NewDeepgramProvider(apiKey, model string) *DeepgramProvider {
	return &DeepgramProvider{
		apiKey:  apiKey,
		model:   model,
		audioCh: make(chan []byte, 64),
		textCh:  make(chan common.STTEvent, 32),
	}
}

// Start opens the WebSocket connection to Deepgram and launches background
// goroutines for reading audio input and writing transcription output.
func (d *DeepgramProvider) Start(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.cancel != nil {
		return fmt.Errorf("deepgram: already started")
	}

	d.ctx, d.cancel = context.WithCancel(ctx)

	// Connect with exponential backoff.
	conn, err := d.connect()
	if err != nil {
		d.cancel = nil
		d.ctx = nil
		return fmt.Errorf("deepgram: %w", err)
	}
	d.wsConn = conn

	go d.writePump()
	go d.readPump()

	return nil
}

// AudioStream returns the send-only channel for writing raw 16kHz mono PCM data.
func (d *DeepgramProvider) AudioStream() chan<- []byte {
	return d.audioCh
}

// TextStream returns the receive-only channel for reading STTEvent transcription results.
func (d *DeepgramProvider) TextStream() <-chan common.STTEvent {
	return d.textCh
}

// Stop cancels context (goroutines exit), then closes the WebSocket.
// Subsequent calls are no-ops. Safe for concurrent use.
func (d *DeepgramProvider) Stop() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.cancel == nil {
		return nil
	}

	// Cancel context first — writePump/readPump will close wsConn in their defers.
	d.cancel()

	if d.wsConn != nil {
		_ = d.wsConn.Close()
		d.wsConn = nil
	}

	return nil
}

// connect establishes a WebSocket connection to the Deepgram API with the configured
// model and parameters. It uses the API key in the Authorization header.
func (d *DeepgramProvider) connect() (*websocket.Conn, error) {
	url := "wss://api.deepgram.com/v2/listen?model=flux-general-en&encoding=linear16&sample_rate=16000&eot_threshold=0.8"

	header := http.Header{}
	header.Set("Authorization", "Token "+d.apiKey)

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, _, err := dialer.Dial(url, header)
	if err != nil {
		return nil, fmt.Errorf("websocket dial: %w", err)
	}

	// Flux v2 does not support KeepAlive (that's a v1/Nova protocol feature).
	// Sending a KeepAlive to /v2/listen causes the server to close the connection
	// with code 1006. Just return the established connection as-is.

	return conn, nil
}

// reconnectWithBackoff attempts to reconnect to Deepgram with exponential backoff.
// It returns the new connection or an error after maxReconnectAttempts failures.
func (d *DeepgramProvider) reconnectWithBackoff() (*websocket.Conn, error) {
	backoff := initialBackoff
	for attempt := 0; attempt < maxReconnectAttempts; attempt++ {
		slog.Info("deepgram: reconnecting",
			"attempt", attempt+1,
			"backoff", backoff,
		)

		conn, err := d.connect()
		if err == nil {
			slog.Info("deepgram: reconnected successfully", "attempt", attempt+1)
			return conn, nil
		}

		slog.Warn("deepgram: reconnect failed",
			"attempt", attempt+1,
			"error", err,
			"next_backoff", minDuration(backoff*2, maxBackoff),
		)

		select {
		case <-d.ctx.Done():
			return nil, d.ctx.Err()
		case <-time.After(backoff):
		}

		backoff = minDuration(backoff*2, maxBackoff)
	}

	return nil, fmt.Errorf("deepgram: max reconnect attempts (%d) reached", maxReconnectAttempts)
}

// writePump reads audio chunks from audioCh and writes them to the WebSocket connection.
// It handles reconnection on write failures using exponential backoff.
func (d *DeepgramProvider) writePump() {
	defer func() {
		d.mu.Lock()
		if d.wsConn != nil {
			d.wsConn.Close()
			d.wsConn = nil
		}
		d.mu.Unlock()
	}()

	for {
		select {
		case <-d.ctx.Done():
			return
		case chunk, ok := <-d.audioCh:
			if !ok {
				return
			}

			d.mu.Lock()
			conn := d.wsConn
			d.mu.Unlock()

			if conn == nil {
				// Try to reconnect.
				d.mu.Lock()
				if d.wsConn == nil {
					d.mu.Unlock()
					newConn, err := d.reconnectWithBackoff()
					if err != nil {
						slog.Error("deepgram: write pump reconnect failed", "error", err)
						d.emitError(fmt.Errorf("reconnect failed: %w", err))
						return
					}
					d.mu.Lock()
					d.wsConn = newConn
					conn = newConn
				} else {
					conn = d.wsConn
				}
				d.mu.Unlock()
			}

			conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := conn.WriteMessage(websocket.BinaryMessage, chunk); err != nil {
				slog.Warn("deepgram: write error, will retry", "error", err)
				// Close the broken connection.
				d.mu.Lock()
				if d.wsConn == conn {
					conn.Close()
					d.wsConn = nil
				}
				d.mu.Unlock()
			}
		}
	}
}

// readPump reads WebSocket JSON messages from Deepgram and parses transcription results.
// It emits interim (IsFinal=false) and final (IsFinal=true) STTEvent values on textCh.
func (d *DeepgramProvider) readPump() {
	defer func() {
		d.mu.Lock()
		if d.wsConn != nil {
			d.wsConn.Close()
			d.wsConn = nil
		}
		d.mu.Unlock()
	}()

	for {
		select {
		case <-d.ctx.Done():
			return
		default:
		}

		d.mu.Lock()
		conn := d.wsConn
		d.mu.Unlock()

		if conn == nil {
			// Connection lost; wait for writePump to reconnect.
			select {
			case <-d.ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}

		_, message, err := conn.ReadMessage()
		if err != nil {
			// Connection may be closed; let writePump handle reconnection.
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				slog.Warn("deepgram: read error", "error", err)
			}

			d.mu.Lock()
			if d.wsConn == conn {
				conn.Close()
				d.wsConn = nil
			}
			d.mu.Unlock()

			select {
			case <-d.ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}

		d.parseAndEmit(message)
	}
}

// fluxResponse represents the JSON structure returned by the Deepgram /v2/listen (Flux) WebSocket API.
// Flux uses TurnInfo events with Update for interim and EndOfTurn for final transcription.
type fluxResponse struct {
	Type                string  `json:"type"`  // "TurnInfo"
	Event               string  `json:"event"` // "Update", "StartOfTurn", "EndOfTurn", "EagerEndOfTurn", "TurnResumed"
	TurnIndex           int     `json:"turn_index"`
	Transcript          string  `json:"transcript"`
	EndOfTurnConfidence float64 `json:"end_of_turn_confidence"`
	AudioWindowStart    float64 `json:"audio_window_start"`
	AudioWindowEnd      float64 `json:"audio_window_end"`
	SequenceID          int     `json:"sequence_id"`
}

// parseAndEmit parses a Deepgram Flux TurnInfo JSON message and emits the appropriate STTEvent.
// Update events → interim (IsFinal=false), EndOfTurn → final (IsFinal=true).
// StartOfTurn and TurnResumed are skipped (no transcript).
func (d *DeepgramProvider) parseAndEmit(message []byte) {
	var resp fluxResponse
	if err := json.Unmarshal(message, &resp); err != nil {
		slog.Warn("deepgram: failed to parse response", "error", err, "raw", string(message))
		return
	}

	// Only process TurnInfo type messages; ignore KeepAlive, etc.
	if resp.Type != "TurnInfo" {
		return
	}

	// Skip StartOfTurn and TurnResumed — they carry no transcript.
	if resp.Event == "StartOfTurn" || resp.Event == "TurnResumed" {
		return
	}

	transcript := resp.Transcript

	// Empty transcript — nothing to emit.
	if transcript == "" {
		return
	}

	// EndOfTurn → final, everything else (Update, EagerEndOfTurn) → interim.
	isFinal := (resp.Event == "EndOfTurn")

	event := common.STTEvent{
		Text:      transcript,
		IsFinal:   isFinal,
		ChannelID: "speaker", // Flux is single-channel; caller can override downstream
		Timestamp: time.Now(),
	}

	select {
	case d.textCh <- event:
	case <-d.ctx.Done():
	default:
		slog.Warn("deepgram: text channel full, dropping event")
	}
}

// emitError sends an error-bearing STTEvent on the text channel.
// If the channel is full or the context is done, the error is logged instead.
func (d *DeepgramProvider) emitError(err error) {
	event := common.STTEvent{
		Error:     err,
		Timestamp: time.Now(),
	}

	select {
	case d.textCh <- event:
	case <-d.ctx.Done():
		slog.Warn("deepgram: context done, error not emitted", "error", err)
	default:
		slog.Warn("deepgram: text channel full, error not emitted", "error", err)
	}
}

// minDuration returns the smaller of two durations (not available in Go 1.22's time package).
func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// Compile-time interface check.
var _ STTProvider = (*DeepgramProvider)(nil)
