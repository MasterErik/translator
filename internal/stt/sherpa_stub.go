package stt

import (
	"context"
	"fmt"

	"github.com/mastererik/translator/internal/common"
)

// SherpaOnnxProvider is a future local STT provider using sherpa-onnx-go.
// Currently a stub — all methods are no-ops that return errors or valid empty channels.
//
// When sherpa-onnx-go is integrated, this provider will load ONNX models locally
// and perform offline speech-to-text with zero API costs and no network dependency.
// The stub exists so that the rest of the pipeline can be built and tested against
// the STTProvider interface without a real Deepgram API key.
type SherpaOnnxProvider struct {
	audioCh chan []byte
	textCh  chan common.STTEvent
}

// NewSherpaOnnxProvider creates a new SherpaOnnxProvider stub.
func NewSherpaOnnxProvider() *SherpaOnnxProvider {
	return &SherpaOnnxProvider{
		audioCh: make(chan []byte),
		textCh:  make(chan common.STTEvent),
	}
}

// Start returns an error because sherpa-onnx is not yet implemented.
func (s *SherpaOnnxProvider) Start(ctx context.Context) error {
	return fmt.Errorf("sherpa-onnx not yet implemented")
}

// AudioStream returns a valid unbuffered channel for audio input.
// Writing to this channel will block indefinitely since no reader is started.
func (s *SherpaOnnxProvider) AudioStream() chan<- []byte {
	return s.audioCh
}

// TextStream returns a valid unbuffered channel for text output.
// No events will ever be emitted on this channel in the stub.
func (s *SherpaOnnxProvider) TextStream() <-chan common.STTEvent {
	return s.textCh
}

// Stop is a no-op in the stub.
func (s *SherpaOnnxProvider) Stop() error {
	return nil
}

// Compile-time interface check.
var _ STTProvider = (*SherpaOnnxProvider)(nil)
