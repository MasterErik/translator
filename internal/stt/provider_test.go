package stt_test

import (
	"testing"

	"github.com/mastererik/translator/internal/common"
	"github.com/mastererik/translator/internal/stt"
)

// Compile-time interface compliance check: ensure STTProvider interface
// is properly defined and can be referenced.
func TestSTTProviderInterface(t *testing.T) {
	// This is a compile-time check: if STTProvider is not an interface
	// or doesn't have the right methods, this won't compile.
	var _ stt.STTProvider = nil //nolint:staticcheck // intentional compile-time check

	// Verify that common.STTEvent can be used with the interface.
	_ = common.STTEvent{
		Text:      "test",
		Event:     common.EventEndOfTurn,
		ChannelID: "speaker",
	}
}
