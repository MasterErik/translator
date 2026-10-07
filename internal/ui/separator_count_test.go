package ui

import (
	"fmt"
	"testing"

	"gioui.org/widget/material"

	"github.com/mastererik/translator/internal/logger"
)

// TestSeparatorCountPerOverlayIndependent — RED (Task 3.2): метрика separator
// принадлежит кадру конкретного оверлея, а не глобальной переменной. Два
// оверлея рендерятся по очереди; каждый кадр вернул своё значение, и второй
// рендер первого оверлея не зависит от состояния второго.
//
// С глобальным zoneSeparatorCount второй оверлей перезаписывал бы счётчик, и
// проверки первого «утекли» бы; здесь значения независимы по построению.
func TestSeparatorCountPerOverlayIndependent(t *testing.T) {
	th := material.NewTheme()

	a := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: 18}, logger.NewNopSessionLogger())
	b := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: 18}, logger.NewNopSessionLogger())

	// A: история скрыта → 2 separator.
	gtxA1, _ := newTestContext(800, 650)
	if got := a.render(gtxA1, th).SeparatorCount; got != 2 {
		t.Fatalf("A (hidden) separator count = %d, want 2", got)
	}

	// B: история видима → 3 separator.
	b.ToggleTranscriptionHistory()
	gtxB, _ := newTestContext(800, 650)
	if got := b.render(gtxB, th).SeparatorCount; got != 3 {
		t.Fatalf("B (visible) separator count = %d, want 3", got)
	}

	// Повторный рендер A снова даёт 2 — счётчик не утёк от B.
	gtxA2, _ := newTestContext(800, 650)
	if got := a.render(gtxA2, th).SeparatorCount; got != 2 {
		t.Errorf("A (hidden, 2-й кадр) separator count = %d, want 2 (метрика не должна зависеть от B)", got)
	}

	// И B остаётся 3.
	gtxB2, _ := newTestContext(800, 650)
	if got := b.render(gtxB2, th).SeparatorCount; got != 3 {
		t.Errorf("B (visible, 2-й кадр) separator count = %d, want 3", got)
	}
}

// TestSeparatorCountNotAccumulatedAcrossFrames — счётчик кадра, а не накопление:
// несколько кадров подряд дают одно и то же число (глобал сбрасывался в render,
// а теперь счётчик вообще локальный).
func TestSeparatorCountNotAccumulatedAcrossFrames(t *testing.T) {
	th := material.NewTheme()
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: 18}, logger.NewNopSessionLogger())
	o.AddMessage(UIMessage{Type: Interim, Text: fmt.Sprintf("line")})

	for frame := 1; frame <= 3; frame++ {
		gtx, _ := newTestContext(800, 650)
		if got := o.render(gtx, th).SeparatorCount; got != 2 {
			t.Errorf("кадр %d: separator count = %d, want 2 (счётчик не накапливается)", frame, got)
		}
	}
}
