package stt

import (
	"context"
	"testing"
	"time"

	"github.com/mastererik/translator/internal/common"
)

// TestSherpaOnnxProvider_SatisfiesInterface verifies that SherpaOnnxProvider
// satisfies the STTProvider interface at compile time.
func TestSherpaOnnxProvider_SatisfiesInterface(t *testing.T) {
	var p STTProvider = NewSherpaOnnxProvider()
	if p == nil {
		t.Fatal("NewSherpaOnnxProvider returned nil")
	}
}

// TestSherpaOnnxProvider_StartReturnsError verifies that Start returns the
// expected "not yet implemented" error.
func TestSherpaOnnxProvider_StartReturnsError(t *testing.T) {
	p := NewSherpaOnnxProvider()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	err := p.Start(ctx)
	if err == nil {
		t.Fatal("expected error from Start, got nil")
	}
	if err.Error() != "sherpa-onnx not yet implemented" {
		t.Errorf("expected 'sherpa-onnx not yet implemented', got '%s'", err.Error())
	}
}

// TestSherpaOnnxProvider_AudioStreamReturnsValidChannel verifies that
// AudioStream returns a non-nil channel.
func TestSherpaOnnxProvider_AudioStreamReturnsValidChannel(t *testing.T) {
	p := NewSherpaOnnxProvider()
	ch := p.AudioStream()
	if ch == nil {
		t.Fatal("AudioStream returned nil channel")
	}
}

// TestSherpaOnnxProvider_TextStreamReturnsValidChannel verifies that
// TextStream returns a non-nil channel.
func TestSherpaOnnxProvider_TextStreamReturnsValidChannel(t *testing.T) {
	p := NewSherpaOnnxProvider()
	ch := p.TextStream()
	if ch == nil {
		t.Fatal("TextStream returned nil channel")
	}
}

// TestSherpaOnnxProvider_StopIsNoop verifies that Stop does not panic or error.
func TestSherpaOnnxProvider_StopIsNoop(t *testing.T) {
	p := NewSherpaOnnxProvider()
	err := p.Stop()
	if err != nil {
		t.Errorf("Stop returned error: %v", err)
	}

	// Double Stop should also be safe.
	err = p.Stop()
	if err != nil {
		t.Errorf("double Stop returned error: %v", err)
	}
}

// TestSherpaOnnxProvider_NoPanicOnUsage verifies that using the provider
// without Start does not panic.
func TestSherpaOnnxProvider_NoPanicOnUsage(t *testing.T) {
	p := NewSherpaOnnxProvider()

	// These should not panic even without calling Start.
	_ = p.AudioStream()
	_ = p.TextStream()
	_ = p.Stop()

	// Sending on AudioStream should block (unbuffered), so we do it in a goroutine.
	done := make(chan struct{})
	go func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("panic on AudioStream send: %v", r)
			}
			close(done)
		}()
		// This will block forever since nothing reads, so we use select.
		select {
		case p.AudioStream() <- []byte{1, 2, 3}:
			// Won't happen — nothing reads.
		case <-time.After(100 * time.Millisecond):
			// Expected — channel is unbuffered, no reader.
		}
	}()
	<-done
}

// TestSherpaOnnxProvider_InterfaceCompliance covers all interface methods
// to ensure they exist with correct signatures.
func TestSherpaOnnxProvider_InterfaceCompliance(t *testing.T) {
	// Test that all interface methods are callable without panic.
	var p STTProvider = NewSherpaOnnxProvider()

	// Start — returns error.
	ctx := context.Background()
	err := p.Start(ctx)
	if err == nil {
		t.Fatal("expected error")
	}

	// AudioStream — returns non-nil channel.
	audioCh := p.AudioStream()
	if audioCh == nil {
		t.Fatal("AudioStream is nil")
	}

	// TextStream — returns non-nil channel.
	textCh := p.TextStream()
	if textCh == nil {
		t.Fatal("TextStream is nil")
	}

	// Verify channel types — these compile-time checks confirm correct types.
	var _ chan<- []byte = audioCh
	var _ <-chan common.STTEvent = textCh

	// Stop — no-op.
	if err := p.Stop(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
