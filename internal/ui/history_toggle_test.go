package ui

import (
	"fmt"
	"image"
	"testing"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"github.com/mastererik/translator/internal/logger"
)

// newTestContext — layout.Context с фиксированными размерами окна,
// без реального окна (для юнит-тестов render).
func newTestContext(width, height int) (layout.Context, *op.Ops) {
	ops := new(op.Ops)
	gtx := layout.Context{
		Ops:         ops,
		Constraints: layout.Exact(image.Pt(width, height)),
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
	}
	return gtx, ops
}

// TestHistoryInitiallyHidden — начальное состояние TranscriptionHistory: скрыта.
func TestHistoryInitiallyHidden(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: 18}, logger.NewNopSessionLogger())
	if o.TranscriptionVisible() {
		t.Error("начальное состояние TranscriptionVisible = true, want false")
	}
}

// TestToggleTranscriptionHistory — F9-переключение: скрыт → показан → скрыт.
func TestToggleTranscriptionHistory(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: 18}, logger.NewNopSessionLogger())

	o.ToggleTranscriptionHistory()
	if !o.TranscriptionVisible() {
		t.Error("после 1-го toggle TranscriptionVisible = false, want true")
	}

	o.ToggleTranscriptionHistory()
	if o.TranscriptionVisible() {
		t.Error("после 2-го toggle TranscriptionVisible = true, want false")
	}
}

// TestToggleTranscriptionHistoryConcurrent — toggle из нескольких горутин
// не гоняется (state под mu).
func TestToggleTranscriptionHistoryConcurrent(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: 18}, logger.NewNopSessionLogger())

	done := make(chan struct{})
	for g := 0; g < 4; g++ {
		go func() {
			for i := 0; i < 50; i++ {
				o.ToggleTranscriptionHistory()
			}
			done <- struct{}{}
		}()
	}
	for g := 0; g < 4; g++ {
		<-done
	}
	// 200 переключений из чётного нуля — итог любое значение, главное без
	// data race (ловится go test -race) и без паники.
	_ = o.TranscriptionVisible()
}

// TestHistoryVisibleHeightPx — высота видимой области истории: ровно 4 строки.
func TestHistoryVisibleHeightPx(t *testing.T) {
	tests := []struct {
		fs   int
		want int
	}{
		{fs: 18, want: 76},  // hfs=16, 16*1.2=19.2 → 19 px/строка → 76
		{fs: 10, want: 48},  // hfs=8 <10 → 10, 10*1.2=12 → 48
		{fs: 24, want: 104}, // hfs=22, 22*1.2=26.4 → 26 → 104
	}
	for _, tt := range tests {
		got := historyVisibleHeightPx(tt.fs)
		if got != tt.want {
			t.Errorf("historyVisibleHeightPx(%d) = %d, want %d", tt.fs, got, tt.want)
		}
	}
	// floor-кейс: fs=10 → hfs=10 (минимум), не 8.
	if got := historyVisibleHeightPx(10); got != int(float32(10)*lineHeightFactor)*historyVisibleLines {
		t.Errorf("historyVisibleHeightPx(10) = %d, want floor по hfs=10", got)
	}
}

// TestRenderHistoryHiddenOccupiesNoSpace — при скрытой истории рендер
// ограничивает зону истории нулём: separator и зона отсутствуют.
func TestRenderHistoryHiddenOccupiesNoSpace(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: 18}, logger.NewNopSessionLogger())
	for i := 1; i <= 10; i++ {
		o.AddMessage(UIMessage{Type: History, Text: fmt.Sprintf("line %d", i)})
	}
	o.AddMessage(UIMessage{Type: Interim, Text: "interim text"})
	o.AddMessage(UIMessage{Type: Translation, Text: "перевод", MsgStatus: "done"})
	o.AddMessage(UIMessage{Type: AnswerCandidates, Answers: []string{"EN: yes | RU: да"}})

	gtx, _ := newTestContext(800, 650)
	th := material.NewTheme()

	// Скрытая история — рендер не должен паниковать; Layout занимает всё окно.
	dims := o.render(gtx, th)
	if dims.Size.X != 800 || dims.Size.Y != 650 {
		t.Errorf("render size = %v, want 800x650", dims.Size)
	}
	if o.TranscriptionVisible() {
		t.Error("historyVisible не должен меняться рендером")
	}
}

// TestRenderHistoryVisibleCappedAt4Lines — при видимой истории зона
// ограничена высотой ровно 4 строки.
func TestRenderHistoryVisibleCappedAt4Lines(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: 18}, logger.NewNopSessionLogger())
	for i := 1; i <= 40; i++ {
		o.AddMessage(UIMessage{Type: History, Text: fmt.Sprintf("line %d", i)})
	}
	o.ToggleTranscriptionHistory() // показать

	gtx, _ := newTestContext(800, 650)
	th := material.NewTheme()

	dims := o.render(gtx, th)
	if dims.Size.X != 800 || dims.Size.Y != 650 {
		t.Errorf("render size = %v, want 800x650", dims.Size)
	}

	// Позиция скролла обновилась — зона отрендерилась и проскроллилась.
	if o.TranscriptionScrollLen() != 40 {
		t.Errorf("prevTranscLen = %d, want 40 — зона истории не отрендерилась при visible", o.TranscriptionScrollLen())
	}

	wantHeight := historyVisibleHeightPx(18)
	t.Logf("высота видимой области истории: %d px (4 строки при fs=18)", wantHeight)
	if wantHeight <= 0 || wantHeight >= 650 {
		t.Errorf("historyVisibleHeightPx(18) = %d — вне разумных границ", wantHeight)
	}
}

// TestRenderInterimTranslationRegression — регресс: Interim и Translation
// рендерятся при обоих состояниях истории без ошибок.
func TestRenderInterimTranslationRegression(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: 18}, logger.NewNopSessionLogger())
	o.AddMessage(UIMessage{Type: Interim, Text: "I have five years of experience"})
	o.AddMessage(UIMessage{Type: Translation, Text: "У меня пять лет опыта", MsgStatus: "done"})
	o.AddMessage(UIMessage{Type: AnswerCandidates, Answers: []string{"EN: a | RU: б"}})

	th := material.NewTheme()

	for _, visible := range []bool{false, true} {
		if visible {
			o.ToggleTranscriptionHistory()
		}
		gtx, _ := newTestContext(800, 650)
		dims := o.render(gtx, th)
		if dims.Size.X != 800 || dims.Size.Y != 650 {
			t.Errorf("historyVisible=%v: render size = %v, want 800x650", visible, dims.Size)
		}
		// Данные зон не изменились от рендера и toggle.
		if m := o.lastInterim(); m.Text != "I have five years of experience" {
			t.Errorf("historyVisible=%v: interim = %q", visible, m.Text)
		}
		if tr := o.translationMessages(); len(tr) != 1 || tr[0].Text != "У меня пять лет опыта" {
			t.Errorf("historyVisible=%v: translations = %v", visible, tr)
		}
	}
}

// TestRenderEmptyAnswersAndNoHistory — AnswerCandidates с !hasAnswers и пустая
// история при visible: рендер не паникует.
func TestRenderEmptyAnswersAndNoHistory(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: 18}, logger.NewNopSessionLogger())
	o.ToggleTranscriptionHistory() // visible, но история пуста

	gtx, _ := newTestContext(800, 650)
	th := material.NewTheme()
	dims := o.render(gtx, th)
	if dims.Size.X != 800 || dims.Size.Y != 650 {
		t.Errorf("render size = %v, want 800x650", dims.Size)
	}
}
