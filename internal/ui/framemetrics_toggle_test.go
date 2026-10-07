package ui

import (
	"fmt"
	"testing"

	"github.com/mastererik/translator/internal/logger"
)

// TestToggleHistoryFrameMetricsTogether — F4-тумблер и FrameMetrics вместе:
// один и тот же оверлей с данными во всех зонах рендерится при скрытой зоне 4
// (2 separator, зона истории не участвует) и при видимой (3 separator, зона 4
// доскроллена к концу). Метрики каждого кадра — свои, не текут между кадрами.
func TestToggleHistoryFrameMetricsTogether(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: 18}, logger.NewNopSessionLogger())

	// Данные во всех зонах; история заведомо длиннее видимой высоты (Section 4).
	o.AddMessage(UIMessage{Type: Interim, Text: "I have five years of experience"})
	for i := 1; i <= 20; i++ {
		o.AddMessage(UIMessage{Type: Translation, Text: fmt.Sprintf("перевод %d", i)})
		o.AddMessage(UIMessage{Type: History, Text: fmt.Sprintf("original %d", i)})
	}
	for i := 1; i <= 20; i++ {
		o.AddMessage(UIMessage{Type: AnswerCandidates, Answers: answersFrom(fmt.Sprintf("hint %d", i))})
	}

	// Кадр 1: история скрыта (по умолчанию) — 2 separator; зона 4 не раскладывается,
	// поэтому на неё не полагаемся (TranscriptionAtEnd здесь не определён:
	// нераскладываемый список сохраняет нулевую позицию). Проверяем только зоны 1–3.
	m1 := renderFrame(o, 800, 650)
	if m1.SeparatorCount != 2 {
		t.Errorf("скрыта: SeparatorCount = %d, want 2", m1.SeparatorCount)
	}
	if !m1.TranslationsAtEnd || !m1.AnswersAtEnd || !m1.InterimAtEnd {
		t.Errorf("скрыта: зоны 1–3 должны быть у конца: %+v", m1)
	}

	// F4: зона 4 включается — 3 separator, история доскроллена к концу.
	o.ToggleTranscriptionHistory()
	m2 := renderFrame(o, 800, 650)
	if m2.SeparatorCount != 3 {
		t.Errorf("видима: SeparatorCount = %d, want 3", m2.SeparatorCount)
	}
	if !m2.TranscriptionAtEnd {
		t.Errorf("видима: TranscriptionAtEnd = false, want true (автоскролл в конец)")
	}

	// F4 снова: возврат к 2 separator, метрики независимы от предыдущего кадра.
	o.ToggleTranscriptionHistory()
	m3 := renderFrame(o, 800, 650)
	if m3.SeparatorCount != 2 {
		t.Errorf("после 2-го F4: SeparatorCount = %d, want 2", m3.SeparatorCount)
	}
}
