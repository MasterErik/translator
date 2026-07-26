// Package ui implements the GioUI overlay window for the Translator application.
// Три зоны: речь (interim), перевод (скролл), подсказки.
package ui

import "time"

// OverlayConfig holds configuration parameters for the GioUI overlay window.
type OverlayConfig struct {
	Width    int // Default: 1200
	Height   int // Default: 650
	FontSize int // Default: 18
	MaxLines int // строк перевода в истории. Default: 10.
}

// UIMessageType classifies the kind of message sent to the overlay.
type UIMessageType string

const (
	Interim          UIMessageType = "Interim"          // текущая речь (верхняя зона)
	Translation      UIMessageType = "Translation"      // перевод (средняя зона)
	AnswerCandidates UIMessageType = "AnswerCandidates" // подсказки (нижняя зона)
	History          UIMessageType = "History"          // история оригиналов (нижняя зона, скролл)
	Status           UIMessageType = "Status"           // статус (для тестов)
)

// UIMessage represents a single message displayed in the overlay.
type UIMessage struct {
	Type      UIMessageType
	Text      string
	Answers   []string
	Timestamp time.Time

	// MsgStatus: "pending" | "streaming" | "done" | "" — для Translation.
	MsgStatus string

	// Translation — перевод для History-сообщений (чтобы показывать и оригинал, и перевод).
	Translation string
}
