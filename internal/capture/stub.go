package capture

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// StubCapture simulates dual-channel audio capture without real audio hardware.
type StubCapture struct {
	config        CaptureConfig
	loopbackData  []byte
	micData       []byte
	frameInterval time.Duration
}

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

// Start begins simulated capture. Returns loopbackCh and micCh (both 16kHz mono).
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

	go func() {
		wg.Wait()
		close(loopbackCh)
		close(micCh)
	}()

	return loopbackCh, micCh, nil
}

func (s *StubCapture) runChannel(ctx context.Context, wg *sync.WaitGroup, ch chan<- []byte, data []byte, name string) {
	defer wg.Done()

	ticker := time.NewTicker(s.frameInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			frame := make([]byte, len(data))
			copy(frame, data)
			select {
			case ch <- frame:
			case <-ctx.Done():
				return
			default:
			}
		}
	}
}
