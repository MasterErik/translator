// Package ui implements the GioUI overlay window for the Translator application.
// Три зоны: речь (interim), перевод (скролл), подсказки.
package ui

import "time"

// OverlayConfig holds configuration parameters for the GioUI overlay window.
type OverlayConfig struct {
	Width    int // Default: 1200
	Height   int // Default: 650
	FontSize int // Default: 18
}

// UIMessageType classifies the kind of message sent to the overlay.
type UIMessageType string

const (
	Interim          UIMessageType = "Interim"          // текущая речь (верхняя зона)
	Translation      UIMessageType = "Translation"      // перевод (средняя зона)
	AnswerCandidates UIMessageType = "AnswerCandidates" // подсказки (нижняя зона)
	History          UIMessageType = "History"          // история оригиналов (нижняя зона, скролл)
	Status           UIMessageType = "Status"           // статус (для тестов)
	Error            UIMessageType = "Error"            // сообщение об ошибке генерации (зона 3)
)

// Answer is a single generated hint: the answer in the source language and its
// translation into the target language. The pair of languages is configurable
// (see common.Config.SourceLang/TargetLang), so the fields carry neutral names.
type Answer struct {
	Source string
	Target string

	// Поля ниже — контракт таймлайна вопросов (QuestionTimeline). Они позволяют
	// показывать метку «вопрос N из M» и статус генерации, не вводя в пакет ui
	// зависимость от dispatcher (значения State/Mode задаёт dispatcher).
	//
	// QuestionIdx — АБСОЛЮТНЫЙ номер вопроса в таймлайне (нумерация с 0, не
	// сбрасывается при вытеснении старых меток из кольца).
	// Total — число меток в окне таймлайна (окно кольца, не растёт бесконечно).
	// QuestionText — текст вопроса, к которому относится подсказка.
	// State — статус генерации: "idle", "queued", "generating", "ready", "failed".
	// Mode — режим генерации: "answer", "more context", "simpler".
	//
	// Нулевые значения сохраняют прежнее поведение рендера (поля не заполнены —
	// рендер работает как раньше).
	QuestionIdx  int
	Total        int
	QuestionText string
	State        string
	Mode         string
}

// UIMessage represents a single message displayed in the overlay.
type UIMessage struct {
	Type      UIMessageType
	Text      string
	Answers   []Answer
	Timestamp time.Time

	// Translation — перевод для History-сообщений (чтобы показывать и оригинал, и перевод).
	Translation string
}
