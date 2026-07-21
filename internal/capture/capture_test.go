package capture

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"
)

// simpleSineTone generates a simple test PCM frame (stereo, int16).
// Returns a 4-byte stereo frame with a simple pattern.
func simpleSineTone() []byte {
	// Two int16 values: left=1000, right=2000.
	return []byte{0xE8, 0x03, 0xD0, 0x07} // LE: 1000, 2000
}

func TestStubCapture_ChannelsReceiveData(t *testing.T) {
	cfg := CaptureConfig{BufferSizeMs: 5}
	data := simpleSineTone()
	stub := NewStubCapture(cfg, data, data, 5*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	loopbackCh, micCh, err := stub.Start(ctx)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Read a few frames from each channel.
	receivedLoopback := 0
	receivedMic := 0

	timeout := time.After(200 * time.Millisecond)
loop:
	for receivedLoopback < 3 || receivedMic < 3 {
		select {
		case <-loopbackCh:
			receivedLoopback++
		case <-micCh:
			receivedMic++
		case <-timeout:
			t.Fatal("timeout waiting for data from channels")
		}
		// Also drain any extra data.
		if receivedLoopback >= 3 && receivedMic >= 3 {
			break loop
		}
	}

	if receivedLoopback < 3 {
		t.Errorf("expected at least 3 frames from loopback, got %d", receivedLoopback)
	}
	if receivedMic < 3 {
		t.Errorf("expected at least 3 frames from mic, got %d", receivedMic)
	}
}

func TestStubCapture_ShutdownStopsGoroutines(t *testing.T) {
	cfg := CaptureConfig{BufferSizeMs: 5}
	data := simpleSineTone()
	stub := NewStubCapture(cfg, data, data, 5*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())

	loopbackCh, micCh, err := stub.Start(ctx)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Let a few frames flow.
	time.Sleep(30 * time.Millisecond)

	// Cancel the context to initiate shutdown.
	cancel()

	// Both channels should close within a reasonable timeout.
	loopbackClosed := false
	micClosed := false
	timeout := time.After(2 * time.Second)

	for !loopbackClosed || !micClosed {
		select {
		case _, ok := <-loopbackCh:
			if !ok {
				loopbackClosed = true
			}
		case _, ok := <-micCh:
			if !ok {
				micClosed = true
			}
		case <-timeout:
			t.Fatal("timeout waiting for channels to close after shutdown")
		}
	}

	if !loopbackClosed {
		t.Error("loopback channel did not close after shutdown")
	}
	if !micClosed {
		t.Error("mic channel did not close after shutdown")
	}
}

func TestStubCapture_NoGoroutineLeak(t *testing.T) {
	// Use a longer interval so we know exactly how many goroutines run.
	cfg := CaptureConfig{BufferSizeMs: 50}
	data := simpleSineTone()

	goroutinesBefore := runtime.NumGoroutine()

	stub := NewStubCapture(cfg, data, data, 50*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())

	_, _, err := stub.Start(ctx)
	if err != nil {
		cancel()
		t.Fatalf("Start failed: %v", err)
	}

	// Let it run briefly.
	time.Sleep(80 * time.Millisecond)

	// Cancel and wait for cleanup.
	cancel()

	// Wait for goroutines to settle.
	time.Sleep(200 * time.Millisecond)

	goroutinesAfter := runtime.NumGoroutine()

	// Allow a small delta (e.g., runtime goroutines may fluctuate).
	delta := goroutinesAfter - goroutinesBefore
	if delta > 2 {
		t.Errorf("possible goroutine leak: %d goroutines before, %d after (delta=%d)",
			goroutinesBefore, goroutinesAfter, delta)
	}
}

func TestStubCapture_ConcurrentSendReceive(t *testing.T) {
	cfg := CaptureConfig{BufferSizeMs: 2}
	data := simpleSineTone()
	stub := NewStubCapture(cfg, data, data, 2*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	loopbackCh, micCh, err := stub.Start(ctx)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// Concurrent readers.
	go func() {
		defer wg.Done()
		count := 0
		for count < 10 {
			<-loopbackCh
			count++
		}
	}()

	go func() {
		defer wg.Done()
		count := 0
		for count < 10 {
			<-micCh
			count++
		}
	}()

	// Wait with timeout.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success.
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for concurrent reads")
	}
}

func TestStubCapture_NilData(t *testing.T) {
	cfg := CaptureConfig{BufferSizeMs: 20}
	stub := NewStubCapture(cfg, nil, nil, 20*time.Millisecond)

	ctx := context.Background()
	_, _, err := stub.Start(ctx)
	if err == nil {
		t.Fatal("expected error for nil loopbackData")
	}

	// Test mic nil but loopback set.
	stub2 := NewStubCapture(cfg, simpleSineTone(), nil, 20*time.Millisecond)
	_, _, err = stub2.Start(ctx)
	if err == nil {
		t.Fatal("expected error for nil micData")
	}
}
