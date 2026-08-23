package main

import (
	"sync"
	"time"

	"github.com/mastererik/translator/internal/ui"
)

// TimedMessage — сообщение UI с временной меткой получения.
type TimedMessage struct {
	ui.UIMessage
	ReceivedAt time.Time
}

// MockOverlay реализует dispatcher.OverlayUI, сохраняя каждое сообщение
// с временной меткой AddMessage. Используется в latency-тесте для замера
// задержек от отправки WAV до появления текста в каждой UI-зоне.
type MockOverlay struct {
	mu               sync.Mutex
	messages         []TimedMessage
	firstInterim     time.Time
	firstHistory     time.Time
	firstTranslation time.Time
	firstAnswer      time.Time
}

func (m *MockOverlay) AddMessage(msg ui.UIMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	m.messages = append(m.messages, TimedMessage{UIMessage: msg, ReceivedAt: now})
	switch msg.Type {
	case ui.Interim:
		if m.firstInterim.IsZero() {
			m.firstInterim = now
		}
	case ui.History:
		if m.firstHistory.IsZero() {
			m.firstHistory = now
		}
	case ui.Translation:
		if m.firstTranslation.IsZero() {
			m.firstTranslation = now
		}
	case ui.AnswerCandidates:
		if m.firstAnswer.IsZero() {
			m.firstAnswer = now
		}
	}
}

func (m *MockOverlay) FirstInterimAt() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.firstInterim
}

func (m *MockOverlay) FirstHistoryAt() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.firstHistory
}

func (m *MockOverlay) FirstTranslationAt() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.firstTranslation
}

func (m *MockOverlay) FirstAnswerAt() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.firstAnswer
}
