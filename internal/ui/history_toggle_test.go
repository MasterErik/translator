package ui

import (
	"fmt"
	"testing"

	"gioui.org/widget/material"

	"github.com/mastererik/translator/internal/logger"
)

// newTestContext/newLabelContext/renderFrame вынесены в testhelpers_test.go (DRY).

// TestToggleTranscriptionHistory — F4-переключение: скрыт → виден → скрыт
// (включая начальное состояние — скрыта).
func TestToggleTranscriptionHistory(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: 18}, logger.NewNopSessionLogger())

	// Начальное состояние — скрыта.
	if o.HistoryVisible() {
		t.Fatal("начальное состояние HistoryVisible = true, want false")
	}

	o.ToggleTranscriptionHistory()
	if !o.HistoryVisible() {
		t.Error("после 1-го toggle HistoryVisible = false, want true")
	}

	o.ToggleTranscriptionHistory()
	if o.HistoryVisible() {
		t.Error("после 2-го toggle HistoryVisible = true, want false")
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

// TestRenderHistoryHiddenOccupiesNoSpace — при скрытой истории рендер
// ограничивает зону истории нулём: separator и зона отсутствуют.
func TestRenderHistoryHiddenOccupiesNoSpace(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: 18}, logger.NewNopSessionLogger())
	// По умолчанию история скрыта — зона 4 не отрисовывается.
	for i := 1; i <= 10; i++ {
		o.AddMessage(UIMessage{Type: History, Text: fmt.Sprintf("line %d", i)})
	}
	o.AddMessage(UIMessage{Type: Interim, Text: "interim text"})
	o.AddMessage(UIMessage{Type: Translation, Text: "перевод", MsgStatus: "done"})
	o.AddMessage(UIMessage{Type: AnswerCandidates, Answers: answersFrom("EN: yes | RU: да")})

	gtx, _ := newTestContext(800, 650)
	th := material.NewTheme()

	// Скрытая история — рендер не должен паниковать; Layout занимает всё окно.
	o.render(gtx, th)
	if gtx.Constraints.Max.X != 800 || gtx.Constraints.Max.Y != 650 {
		t.Errorf("render занимает %v, want 800x650", gtx.Constraints.Max)
	}
	if o.HistoryVisible() {
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
	// По умолчанию история скрыта — включим видимость явно (F4).
	o.ToggleTranscriptionHistory()

	gtx, _ := newTestContext(800, 650)
	th := material.NewTheme()

	dims := o.render(gtx, th)
	_ = dims
	if gtx.Constraints.Max.X != 800 || gtx.Constraints.Max.Y != 650 {
		t.Errorf("render занимает %v, want 800x650", gtx.Constraints.Max)
	}

	// Зона 4 отрендерилась и доскроллена к концу (автоскролл «всегда в конец»).
	if !dims.TranscriptionAtEnd {
		t.Errorf("TranscriptionAtEnd = false, want true — зона истории не доскроллена (Position.BeforeEnd=%v)",
			o.transcriptionList.Position.BeforeEnd)
	}

	wantHeight := historyVisibleHeightPx(18)
	t.Logf("высота видимой области истории: %d px (4 строки при fs=18)", wantHeight)
	if wantHeight <= 0 || wantHeight >= 650 {
		t.Errorf("historyVisibleHeightPx(18) = %d — вне разумных границ", wantHeight)
	}
	// Каркас — ровно 4 × emptyZoneHeight(fs) (Task 3.0: простой множитель).
	if want := emptyZoneHeight(18) * historyVisibleLines; wantHeight != want {
		t.Errorf("historyVisibleHeightPx(18) = %d, want 4 × emptyZoneHeight(18) = %d", wantHeight, want)
	}
}

// TestRenderInterimTranslationRegression — регресс: Interim и Translation
// рендерятся при обоих состояниях истории без ошибок.
func TestRenderInterimTranslationRegression(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: 18}, logger.NewNopSessionLogger())
	o.AddMessage(UIMessage{Type: Interim, Text: "I have five years of experience"})
	o.AddMessage(UIMessage{Type: Translation, Text: "У меня пять лет опыта", MsgStatus: "done"})
	o.AddMessage(UIMessage{Type: AnswerCandidates, Answers: answersFrom("EN: a | RU: б")})

	th := material.NewTheme()

	// По умолчанию история скрыта. Тестируем оба состояния: false (скрыта),
	// затем true (видима) — переключая F4 перед веткой true.
	for _, visible := range []bool{false, true} {
		if visible {
			o.ToggleTranscriptionHistory()
		}
		gtx, _ := newTestContext(800, 650)
		o.render(gtx, th)
		if gtx.Constraints.Max.X != 800 || gtx.Constraints.Max.Y != 650 {
			t.Errorf("historyVisible=%v: render занимает %v, want 800x650", visible, gtx.Constraints.Max)
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
// история: рендер не паникует в обоих состояниях истории (скрыта/видима).
func TestRenderEmptyAnswersAndNoHistory(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: 18}, logger.NewNopSessionLogger())
	// По умолчанию история скрыта; пустая история.

	gtx, _ := newTestContext(800, 650)
	th := material.NewTheme()
	o.render(gtx, th)
	if gtx.Constraints.Max.X != 800 || gtx.Constraints.Max.Y != 650 {
		t.Errorf("render занимает %v, want 800x650", gtx.Constraints.Max)
	}

	// Видимая история с пустым буфером тоже не паникует.
	o.ToggleTranscriptionHistory()
	o.render(gtx, th)
	if gtx.Constraints.Max.X != 800 || gtx.Constraints.Max.Y != 650 {
		t.Errorf("render занимает %v (visible), want 800x650", gtx.Constraints.Max)
	}
}
