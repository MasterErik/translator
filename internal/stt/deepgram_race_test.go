package stt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/mastererik/translator/internal/common"
)

// TestDeepgramProvider_RaceConcurrentSends validates that concurrent writes to
// AudioStream do not cause data races when the writePump is running.
func TestDeepgramProvider_RaceConcurrentSends(t *testing.T) {
	// Mock server that accepts connection and discards audio.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Read messages until connection closes.
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
		}
	}))
	defer server.Close()

	provider := &DeepgramProvider{
		apiKey:  "test-key",
		model:   "flux-general-en",
		audioCh: make(chan []byte, 128),
		textCh:  make(chan common.STTEvent, 128),
	}

	// Manually connect to mock server.
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	header := http.Header{}
	header.Set("Authorization", "Token test-key")
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, _, err := dialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	// No KeepAlive in Flux v2 — skip it.

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	provider.ctx, provider.cancel = context.WithCancel(ctx)
	provider.wsConn = conn

	go provider.writePump()

	// Concurrently send audio chunks from many goroutines.
	var wg sync.WaitGroup
	numGoroutines := 10
	chunksPerGoroutine := 50

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < chunksPerGoroutine; j++ {
				chunk := make([]byte, 1280) // 80ms at 16kHz mono
				for k := range chunk {
					chunk[k] = byte((id*j + k) % 256)
				}
				select {
				case provider.audioCh <- chunk:
				case <-ctx.Done():
					return
				}
			}
		}(i)
	}

	wg.Wait()
	provider.Stop()
}

// TestDeepgramProvider_RaceConcurrentReads validates that concurrent reads from
// TextStream do not cause data races when events are emitted.
func TestDeepgramProvider_RaceConcurrentReads(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	provider := &DeepgramProvider{
		apiKey:  "test-key",
		model:   "flux-general-en",
		audioCh: make(chan []byte, 8),
		textCh:  make(chan common.STTEvent, 128),
	}
	provider.ctx, provider.cancel = context.WithCancel(ctx)

	// Spawn multiple readers.
	var readWg sync.WaitGroup
	numReaders := 5
	eventsPerReader := 20

	for i := 0; i < numReaders; i++ {
		readWg.Add(1)
		go func(id int) {
			defer readWg.Done()
			received := 0
			for received < eventsPerReader {
				select {
				case <-provider.textCh:
					received++
				case <-ctx.Done():
					return
				}
			}
		}(i)
	}

	// Emit events from a single writer goroutine.
	go func() {
		for i := 0; i < numReaders*eventsPerReader; i++ {
			msg := makeFinalResponse("test text")
			provider.parseAndEmit(msg)
		}
	}()

	done := make(chan struct{})
	go func() {
		readWg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// OK — all readers consumed their events.
	case <-ctx.Done():
		t.Fatal("timeout waiting for readers")
	}
}

// TestDeepgramProvider_RaceSendAndReceive validates concurrent audio sends
// AND text receives simultaneously, simulating the real runtime behavior.
func TestDeepgramProvider_RaceSendAndReceive(t *testing.T) {
	// Mock server that echoes audio count back as transcriptions.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// No KeepAlive in Flux v2 — start reading audio directly.
		count := 0
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
			count++
			// Send interim + final for each chunk.
			interim := makeInterimResponse("interim")
			final := makeFinalResponse("final")
			conn.WriteMessage(websocket.TextMessage, interim)
			conn.WriteMessage(websocket.TextMessage, final)
			if count >= 100 {
				return
			}
		}
	}))
	defer server.Close()

	provider := &DeepgramProvider{
		apiKey:  "test-key",
		model:   "flux-general-en",
		audioCh: make(chan []byte, 128),
		textCh:  make(chan common.STTEvent, 128),
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	provider.ctx, provider.cancel = context.WithCancel(ctx)
	provider.wsConn = conn

	go provider.writePump()
	go provider.readPump()

	var wg sync.WaitGroup

	// Senders.
	numSenders := 4
	chunksPerSender := 25
	wg.Add(numSenders)
	for i := 0; i < numSenders; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < chunksPerSender; j++ {
				chunk := make([]byte, 1280) // 80ms at 16kHz mono
				for k := range chunk {
					chunk[k] = byte((id + j + k) % 256)
				}
				select {
				case provider.audioCh <- chunk:
				case <-ctx.Done():
					return
				}
			}
		}(i)
	}

	// Receivers.
	numReceivers := 3
	wg.Add(numReceivers)
	for i := 0; i < numReceivers; i++ {
		go func(id int) {
			defer wg.Done()
			received := 0
			for received < 50 {
				select {
				case event := <-provider.textCh:
					if event.Error != nil {
						t.Errorf("receiver %d got error: %v", id, event.Error)
					}
					received++
				case <-ctx.Done():
					return
				case <-time.After(200 * time.Millisecond):
					// Keep waiting.
				}
			}
		}(i)
	}

	wg.Wait()
	provider.Stop()
}
