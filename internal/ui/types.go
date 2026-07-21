// Package ui implements the GioUI overlay window for the Translator application.
// The overlay provides a two-zone transparent window (translation subtitle + answer hints)
// that stays on top of all other windows using Win32 WS_EX_TOPMOST and WS_EX_LAYERED flags.
package ui

import "time"

// OverlayConfig holds configuration parameters for the GioUI overlay window.
type OverlayConfig struct {
	// Title is the window title used for identification and Win32 HWND lookup.
	Title string

	// Width is the overlay window width in pixels.
	Width int

	// Height is the overlay window height in pixels.
	Height int

	// FontSize is the base font size used for rendering text in the overlay.
	// Default: 18.
	FontSize int

	// TopZoneRatio determines what fraction of the window height (0.0–1.0)
	// is allocated to the upper translation zone. The remainder goes to
	// the lower answer-candidates zone. Default: 0.6.
	TopZoneRatio float64

	// RefreshRate is the target frame rate for UI repaints in frames per second.
	// A value of 0 means use the display's default refresh rate.
	RefreshRate int
}

// UIMessageType classifies the kind of message sent to the overlay.
type UIMessageType string

const (
	// Translation is a translated subtitle to display prominently in the top zone.
	Translation UIMessageType = "Translation"

	// AnswerCandidates is a list of suggested answers to a detected question.
	AnswerCandidates UIMessageType = "AnswerCandidates"

	// Status is a transient status message (e.g. "connected", "error").
	Status UIMessageType = "Status"
)

// UIMessage represents a single message displayed in the overlay.
// It is the internal representation produced from external UIEvent values.
type UIMessage struct {
	// Type classifies the message purpose.
	Type UIMessageType

	// Text is the primary text content (the translation or status text).
	Text string

	// Answers holds candidate answer strings, only meaningful for AnswerCandidates.
	Answers []string

	// Timestamp records when the message was received.
	Timestamp time.Time
}
