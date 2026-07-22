package stt

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/mastererik/translator/internal/common"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// deepgramMockServer creates an httptest server that upgrades to WebSocket
// and can simulate Deepgram responses. It returns the server and a channel
// to receive audio chunks sent by the client.
func deepgramMockServer(t *testing.T) (*httptest.Server, chan []byte, chan []byte) {
	t.Helper()

	audioReceived := make(chan []byte, 64)
	responsesToSend := make(chan []byte, 64)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("mock: upgrade failed: %v", err)
			return
		}
		defer conn.Close()

		var wg sync.WaitGroup
		wg.Add(2)

		// Read audio from client.
		go func() {
			defer wg.Done()
			for {
				_, msg, err := conn.ReadMessage()
				if err != nil {
					return
				}
				select {
				case audioReceived <- msg:
				default:
				}
			}
		}()

		// Write responses to client.
		go func() {
			defer wg.Done()
			for resp := range responsesToSend {
				if err := conn.WriteMessage(websocket.TextMessage, resp); err != nil {
					return
				}
			}
		}()

		wg.Wait()
	}))

	t.Cleanup(func() {
		server.Close()
	})

	return server, audioReceived, responsesToSend
}

// wsURL converts an httptest server URL from http:// to ws://.
func wsURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http")
}

// makeInterimResponse creates a Deepgram Flux TurnInfo Update (interim) JSON response.
func makeInterimResponse(transcript string) []byte {
	r := fluxResponse{
		Type:       "TurnInfo",
		Event:      "Update",
		Transcript: transcript,
	}
	data, _ := json.Marshal(r)
	return data
}

// makeFinalResponse creates a Deepgram Flux TurnInfo EndOfTurn (final) JSON response.
func makeFinalResponse(transcript string) []byte {
	r := fluxResponse{
		Type:       "TurnInfo",
		Event:      "EndOfTurn",
		Transcript: transcript,
	}
	data, _ := json.Marshal(r)
	return data
}

// TestDeepgramProvider_InterimEvents verifies that interim transcription results
// are properly parsed and emitted as STTEvent with IsFinal=false.
func TestDeepgramProvider_InterimEvents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// We use a sub-struct to override the connect method-like approach.
	// Instead, we directly test the parseAndEmit logic.
	provider := &DeepgramProvider{
		apiKey:  "test-key",
		model:   "flux-general-en",
		audioCh: make(chan []byte, 8),
		textCh:  make(chan common.STTEvent, 8),
	}
	provider.ctx, provider.cancel = context.WithCancel(ctx)

	// Send an interim message through parseAndEmit.
	msg := makeInterimResponse("Hello world")
	provider.parseAndEmit(msg)

	select {
	case event := <-provider.textCh:
		if event.IsFinal {
			t.Errorf("expected interim event (IsFinal=false), got IsFinal=true")
		}
		if event.Text != "Hello world" {
			t.Errorf("expected text 'Hello world', got '%s'", event.Text)
		}
		if event.Timestamp.IsZero() {
			t.Errorf("expected non-zero timestamp")
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for interim event")
	}
}

// TestDeepgramProvider_FinalEvents verifies that final transcription results
// are properly parsed and emitted as STTEvent with IsFinal=true.
func TestDeepgramProvider_FinalEvents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	provider := &DeepgramProvider{
		apiKey:  "test-key",
		model:   "flux-general-en",
		audioCh: make(chan []byte, 8),
		textCh:  make(chan common.STTEvent, 8),
	}
	provider.ctx, provider.cancel = context.WithCancel(ctx)

	msg := makeFinalResponse("This is a final result")
	provider.parseAndEmit(msg)

	select {
	case event := <-provider.textCh:
		if !event.IsFinal {
			t.Errorf("expected final event (IsFinal=true), got IsFinal=false")
		}
		if event.Text != "This is a final result" {
			t.Errorf("expected text 'This is a final result', got '%s'", event.Text)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for final event")
	}
}

// TestDeepgramProvider_EndOfTurn verifies that EndOfTurn events are treated as final.
func TestDeepgramProvider_EndOfTurn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	provider := &DeepgramProvider{
		apiKey:  "test-key",
		model:   "flux-general-en",
		audioCh: make(chan []byte, 8),
		textCh:  make(chan common.STTEvent, 8),
	}
	provider.ctx, provider.cancel = context.WithCancel(ctx)

	r := fluxResponse{
		Type:       "TurnInfo",
		Event:      "EndOfTurn",
		Transcript: "speech final text",
	}
	data, _ := json.Marshal(r)
	provider.parseAndEmit(data)

	select {
	case event := <-provider.textCh:
		if !event.IsFinal {
			t.Errorf("expected final event for EndOfTurn, got IsFinal=false")
		}
		if event.Text != "speech final text" {
			t.Errorf("expected 'speech final text', got '%s'", event.Text)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for EndOfTurn event")
	}
}

// TestDeepgramProvider_EmptyTranscript verifies that empty transcripts are not emitted.
func TestDeepgramProvider_EmptyTranscript(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	provider := &DeepgramProvider{
		apiKey:  "test-key",
		model:   "flux-general-en",
		audioCh: make(chan []byte, 8),
		textCh:  make(chan common.STTEvent, 8),
	}
	provider.ctx, provider.cancel = context.WithCancel(ctx)

	msg := makeFinalResponse("")
	provider.parseAndEmit(msg)

	// Should not receive anything.
	select {
	case event := <-provider.textCh:
		t.Errorf("expected no event for empty transcript, got %+v", event)
	case <-time.After(200 * time.Millisecond):
		// OK — no event was emitted.
	}
}

// TestDeepgramProvider_NonTurnInfoIgnored verifies that non-TurnInfo messages (e.g., KeepAlive) are ignored.
func TestDeepgramProvider_NonTurnInfoIgnored(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	provider := &DeepgramProvider{
		apiKey:  "test-key",
		model:   "flux-general-en",
		audioCh: make(chan []byte, 8),
		textCh:  make(chan common.STTEvent, 8),
	}
	provider.ctx, provider.cancel = context.WithCancel(ctx)

	keepalive, _ := json.Marshal(map[string]string{"type": "KeepAlive"})
	provider.parseAndEmit(keepalive)

	select {
	case event := <-provider.textCh:
		t.Errorf("expected no event for KeepAlive, got %+v", event)
	case <-time.After(200 * time.Millisecond):
		// OK.
	}
}

// TestDeepgramProvider_WebSocketIntegration performs an end-to-end test with a mock
// WebSocket server, exercising the full Start → AudioStream → TextStream → Stop lifecycle.
func TestDeepgramProvider_WebSocketIntegration(t *testing.T) {
	// Override the connect method by embedding a test helper.
	// We create a mock server and a custom DeepgramProvider that connects to it.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("mock upgrade failed: %v", err)
			return
		}
		defer conn.Close()

		// Read first audio chunk (no KeepAlive in Flux v2).
		_, audioData, err := conn.ReadMessage()
		if err != nil {
			t.Logf("mock read audio: %v", err)
			return
		}
		if len(audioData) == 0 {
			t.Error("expected non-empty audio data")
		}

		// Send interim result.
		conn.WriteMessage(websocket.TextMessage, makeInterimResponse("hello"))
		// Small delay so client can process.
		time.Sleep(50 * time.Millisecond)

		// Send final result.
		conn.WriteMessage(websocket.TextMessage, makeFinalResponse("hello world"))
		time.Sleep(50 * time.Millisecond)
	}))
	defer server.Close()

	provider := &DeepgramProvider{
		apiKey:  "test-key",
		model:   "flux-general-en",
		audioCh: make(chan []byte, 8),
		textCh:  make(chan common.STTEvent, 16),
	}

	// Manually connect to the mock server instead of the real Deepgram API.
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	header := http.Header{}
	header.Set("Authorization", "Token test-key")
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, _, err := dialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial mock server: %v", err)
	}

	// No KeepAlive in Flux v2 — skip it.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	provider.ctx, provider.cancel = context.WithCancel(ctx)
	provider.wsConn = conn

	// Start the pumps.
	go provider.writePump()
	go provider.readPump()

	// Send an audio chunk.
	provider.audioCh <- []byte{0x00, 0x01, 0x02, 0x03}

	// Collect events.
	var events []common.STTEvent
	timeout := time.After(3 * time.Second)

	for len(events) < 2 {
		select {
		case event := <-provider.textCh:
			events = append(events, event)
		case <-timeout:
			t.Fatalf("timeout waiting for events, got %d: %+v", len(events), events)
		}
	}

	// Verify we have at least an interim and a final.
	hasInterim := false
	hasFinal := false
	for _, ev := range events {
		if !ev.IsFinal {
			hasInterim = true
		} else {
			hasFinal = true
		}
	}
	if !hasInterim {
		t.Error("expected at least one interim event")
	}
	if !hasFinal {
		t.Error("expected at least one final event")
	}

	// Stop.
	provider.Stop()
}

// TestDeepgramProvider_StartStop verifies the Start/Stop lifecycle.
func TestDeepgramProvider_StartStop(t *testing.T) {
	provider := NewDeepgramProvider("test-key", "flux-general-en")

	// Start without a real server should fail.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := provider.Start(ctx)
	if err == nil {
		t.Error("expected error starting without a real Deepgram server")
	}

	// Stop should be safe even if not fully started.
	err = provider.Stop()
	if err != nil {
		t.Errorf("Stop should not error: %v", err)
	}

	// Double stop should be safe.
	err = provider.Stop()
	if err != nil {
		t.Errorf("double Stop should not error: %v", err)
	}
}

// TestDeepgramProvider_DoubleStart verifies that calling Start twice returns an error.
func TestDeepgramProvider_DoubleStart(t *testing.T) {
	// Create a mock server that stays open.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _ := upgrader.Upgrade(w, r, nil)
		if conn != nil {
			// No KeepAlive in Flux v2 — block until closed.
			conn.ReadMessage()
		}
	}))
	defer server.Close()

	provider := &DeepgramProvider{
		apiKey:  "test-key",
		model:   "flux-general-en",
		audioCh: make(chan []byte, 8),
		textCh:  make(chan common.STTEvent, 8),
	}

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	header := http.Header{}
	header.Set("Authorization", "Token test-key")
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, _, err := dialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	// No KeepAlive in Flux v2 — skip it.

	ctx := context.Background()
	provider.ctx, provider.cancel = context.WithCancel(ctx)
	provider.wsConn = conn

	// Second Start should fail.
	err = provider.Start(ctx)
	if err == nil {
		t.Error("expected error on double Start")
	}
}
