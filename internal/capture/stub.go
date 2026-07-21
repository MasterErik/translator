// Package capture provides dual-channel audio capture via malgo WASAPI
// loopback and microphone devices, with resampling from 48kHz Stereo to
// 16kHz Mono.
//
// This file provides a stub capture implementation for testing without
// malgo/CGo dependencies. Production code uses capture_cgo.go.
package capture

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// StubCapture simulates dual-channel audio capture without real audio hardware.
// It produces synthetic PCM data on both channels and supports graceful
// shutdown via context cancellation. Used for testing channel plumbing,
// shutdown ordering, and goroutine lifecycle without malgo/CGo.
type StubCapture struct {
	config        CaptureConfig
	loopbackData  []byte
	micData       []byte
	frameInterval time.Duration
}

// NewStubCapture creates a StubCapture that emits the given PCM data on each
// channel at the specified frame interval. If data is nil, a silent frame is used.
func NewStubCapture(config CaptureConfig, loopbackData, micData []byte, frameInterval time.Duration) *StubCapture {
	if config.BufferSizeMs <= 0 {
		config.BufferSizeMs = defaultBufferSizeMs
	}
	if frameInterval <= 0 {
		frameInterval = time.Duration(config.BufferSizeMs) * time.Millisecond
	}
	return &StubCapture{
		config:        config,
		loopbackData:  loopbackData,
		micData:       micData,
		frameInterval: frameInterval,
	}
}

// Start begins simulated dual-channel capture. It returns two read-only
// channels — loopbackChan and micChan — that receive the configured PCM data
// at the configured frame interval. Both goroutines stop when ctx is cancelled;
// the channels are closed after draining.
func (s *StubCapture) Start(ctx context.Context) (<-chan []byte, <-chan []byte, error) {
	if s.loopbackData == nil {
		return nil, nil, fmt.Errorf("stub capture: loopbackData is nil")
	}
	if s.micData == nil {
		return nil, nil, fmt.Errorf("stub capture: micData is nil")
	}

	loopbackCh := make(chan []byte, channelBufferSize)
	micCh := make(chan []byte, channelBufferSize)

	var wg sync.WaitGroup
	wg.Add(2)

	go s.runChannel(ctx, &wg, loopbackCh, s.loopbackData, "loopback")
	go s.runChannel(ctx, &wg, micCh, s.micData, "microphone")

	// Close channels after both goroutines exit.
	go func() {
		wg.Wait()
		close(loopbackCh)
		close(micCh)
	}()

	return loopbackCh, micCh, nil
}

// runChannel periodically sends data to ch until ctx is cancelled.
func (s *StubCapture) runChannel(ctx context.Context, wg *sync.WaitGroup, ch chan<- []byte, data []byte, name string) {
	defer wg.Done()

	ticker := time.NewTicker(s.frameInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Make a copy so the caller owns the data.
			frame := make([]byte, len(data))
			copy(frame, data)
			select {
			case ch <- frame:
			case <-ctx.Done():
				return
			default:
				// Channel full, drop frame.
			}
		}
	}
}
