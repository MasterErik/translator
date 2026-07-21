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
// The model parameter selects the Deepgram model (e.g., "nova-2", "nova-3").
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

// Stop closes the WebSocket connection, cancels the context, and drains resources.
// Subsequent calls to Stop are no-ops.
func (d *DeepgramProvider) Stop() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.cancel == nil {
		return nil
	}

	// Cancel context first so goroutines exit.
	d.cancel()

	if d.wsConn != nil {
		// Send close frame (best-effort).
		_ = d.wsConn.WriteMessage(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		)
		_ = d.wsConn.Close()
		d.wsConn = nil
	}

	return nil
}

// connect establishes a WebSocket connection to the Deepgram API with the configured
// model and parameters. It uses the API key in the Authorization header.
func (d *DeepgramProvider) connect() (*websocket.Conn, error) {
	url := fmt.Sprintf(
		"wss://api.deepgram.com/v1/listen?model=%s&encoding=linear16&sample_rate=16000&channels=1&interim_results=true",
		d.model,
	)

	header := http.Header{}
	header.Set("Authorization", "Token "+d.apiKey)

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, _, err := dialer.Dial(url, header)
	if err != nil {
		return nil, fmt.Errorf("websocket dial: %w", err)
	}

	// Send keepalive configuration via Deepgram's protocol.
	// The KeepAlive message instructs the server to send periodic ping-like messages.
	ka := map[string]string{"type": "KeepAlive"}
	kaData, _ := json.Marshal(ka)
	if err := conn.WriteMessage(websocket.TextMessage, kaData); err != nil {
		conn.Close()
		return nil, fmt.Errorf("keepalive config: %w", err)
	}

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

// deepgramResponse represents the JSON structure returned by the Deepgram WebSocket API.
type deepgramResponse struct {
	Type       string `json:"type"`
	IsFinal    bool   `json:"is_final"`
	SpeechFinal bool  `json:"speech_final"`
	Channel    struct {
		Alternatives []struct {
			Transcript string  `json:"transcript"`
			Confidence float64 `json:"confidence"`
		} `json:"alternatives"`
	} `json:"channel"`
	Duration    float64 `json:"duration"`
	Start       float64 `json:"start"`
	Metadata    struct {
		RequestID string `json:"request_id"`
		ModelInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"model_info"`
	} `json:"metadata"`
}

// parseAndEmit parses a Deepgram JSON message and emits the appropriate STTEvent.
func (d *DeepgramProvider) parseAndEmit(message []byte) {
	var resp deepgramResponse
	if err := json.Unmarshal(message, &resp); err != nil {
		slog.Warn("deepgram: failed to parse response", "error", err, "raw", string(message))
		return
	}

	// Only process Results type messages; ignore KeepAlive, etc.
	if resp.Type != "Results" {
		return
	}

	transcript := ""
	if len(resp.Channel.Alternatives) > 0 {
		transcript = resp.Channel.Alternatives[0].Transcript
	}

	// Empty transcript — nothing to emit.
	if transcript == "" {
		return
	}

	isFinal := resp.IsFinal || resp.SpeechFinal

	event := common.STTEvent{
		Text:      transcript,
		IsFinal:   isFinal,
		ChannelID: "speaker", // default channel; caller can override downstream
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
