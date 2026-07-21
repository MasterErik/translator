package capture

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestCaptureRace_ConcurrentStartShutdown runs concurrent Start/Shutdown
// cycles to detect data races under -race.
func TestCaptureRace_ConcurrentStartShutdown(t *testing.T) {
	data := simpleSineTone()

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cfg := CaptureConfig{BufferSizeMs: 5}
			stub := NewStubCapture(cfg, data, data, 5*time.Millisecond)

			ctx, cancel := context.WithCancel(context.Background())
			loopbackCh, micCh, err := stub.Start(ctx)
			if err != nil {
				cancel()
				t.Errorf("Start failed: %v", err)
				return
			}

			// Read for a short while in a separate goroutine.
			var readWg sync.WaitGroup
			readWg.Add(2)
			go func() {
				defer readWg.Done()
				for range loopbackCh {
				}
			}()
			go func() {
				defer readWg.Done()
				for range micCh {
				}
			}()

			time.Sleep(20 * time.Millisecond)
			cancel()
			readWg.Wait()
		}()
	}

	wg.Wait()
}

// TestCaptureRace_ConcurrentReaders tests multiple concurrent readers on the
// same channels.
func TestCaptureRace_ConcurrentReaders(t *testing.T) {
	data := simpleSineTone()
	cfg := CaptureConfig{BufferSizeMs: 2}
	stub := NewStubCapture(cfg, data, data, 2*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	loopbackCh, micCh, err := stub.Start(ctx)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	var wg sync.WaitGroup
	// 3 concurrent readers per channel.
	numReaders := 3
	wg.Add(numReaders * 2)

	for i := 0; i < numReaders; i++ {
		go func() {
			defer wg.Done()
			count := 0
			for range loopbackCh {
				count++
				if count >= 5 {
					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			count := 0
			for range micCh {
				count++
				if count >= 5 {
					return
				}
			}
		}()
	}

	// Wait with timeout.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for concurrent readers")
	}
}

// TestCaptureRace_RapidStartStop stresses the start/stop lifecycle.
func TestCaptureRace_RapidStartStop(t *testing.T) {
	data := simpleSineTone()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cfg := CaptureConfig{BufferSizeMs: 1}
			stub := NewStubCapture(cfg, data, data, 1*time.Millisecond)

			ctx, cancel := context.WithCancel(context.Background())

			loopbackCh, micCh, err := stub.Start(ctx)
			if err != nil {
				cancel()
				return
			}

			time.Sleep(5 * time.Millisecond)
			cancel()

			// Drain channels.
			for range loopbackCh {
			}
			for range micCh {
			}
		}()
	}

	wg.Wait()
}
