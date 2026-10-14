package ui

import (
	"testing"

	"gioui.org/unit"
	"gioui.org/widget/material"

	"github.com/mastererik/translator/internal/logger"
)

// newLabelContext вынесен в testhelpers_test.go (DRY).

// oneLineHeight — высота однострочного label через overlayLabel при sp=fs.
func oneLineHeight(th *material.Theme, fs int) int {
	gtx, _ := newLabelContext(200)
	return overlayLabel(th, unit.Sp(fs), "Hg").Layout(gtx).Size.Y
}

// TestOverlayLabelDefaultSpacing — Task 3.0: overlayLabel задаёт стандартный
// межстрочный интервал через LineHeightScale=1.0 (дефолт Gio) и НЕ задаёт
// LineHeight явно (0). Крутилка на будущее — одно число.
func TestOverlayLabelDefaultSpacing(t *testing.T) {
	th := material.NewTheme()
	l := overlayLabel(th, unit.Sp(18), "text")

	if l.LineHeightScale != 1.0 {
		t.Errorf("LineHeightScale = %v, want 1.0 (стандартный интервал Gio)", l.LineHeightScale)
	}
	if l.LineHeight != 0 {
		t.Errorf("LineHeight = %v, want 0 (не задан явно)", l.LineHeight)
	}
	if l.TextSize != unit.Sp(18) {
		t.Errorf("TextSize = %v, want 18 (шрифт не масштабируется)", l.TextSize)
	}
}

// TestLineHeightPxMeasuredNotMultiplier — Task 7 (замена TestEmptyZoneHeightSimpleMultiplier):
// каркас — измеренная высота строки lineHeightPx(fs), а не эвристика fs*5/4.
// historyVisibleHeightPx = historyVisibleLines × строка.
func TestLineHeightPxMeasuredNotMultiplier(t *testing.T) {
	th := material.NewTheme()
	for _, fs := range []int{10, 16, 18, 20} {
		got := lineHeightPx(fs)
		measured := oneLineHeight(th, fs)
		if got != measured {
			t.Errorf("lineHeightPx(%d) = %d, want измеренную %d", fs, got, measured)
		}
		if old := fs * 5 / 4; got == old && measured != old {
			t.Errorf("lineHeightPx(%d) = %d совпал со старой эвристикой fs*5/4", fs, got)
		}
	}
	if got, want := historyVisibleHeightPx(18), lineHeightPx(18)*historyVisibleLines; got != want {
		t.Errorf("historyVisibleHeightPx(18) = %d, want %d", got, want)
	}
}

// TestUnifiedFontSize — Task 3.0: зоны 2–4 рендерят строки единым шрифтом fs
// (раньше — fs-2 с floor 10). Проверяем поведенчески: высота однострочного
// label'а, отрисованного зоной, совпадает с overlayLabel(fs), а НЕ с fs-2.
func TestUnifiedFontSize(t *testing.T) {
	const fs = 18
	th := material.NewTheme()

	wantFS := oneLineHeight(th, fs)
	wantSmall := oneLineHeight(th, fs-2)
	if wantFS == wantSmall {
		t.Skip("fs и fs-2 дают одинаковую высоту — проверка шрифта невозможна")
	}

	// Зона 2 (переводы): одна короткая строка. Position.Length — суммарная
	// высота контента (1 элемент), не шина List: единый шрифт fs ≠ fs-2.
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: fs}, logger.NewNopSessionLogger())
	gtx2, _ := newTestContext(800, 650)
	layoutTranslationHistory(gtx2, th, []UIMessage{{Type: Translation, Text: "single"}}, fs, &o.translationList, false)
	if got := o.translationList.Position.Length; got != wantFS {
		t.Errorf("зона 2 одна строка: высота контента = %d, want %d (единый fs=%d, не fs-2=%d)", got, wantFS, fs, fs-2)
	}

	// Зона 4 (история): одна короткая строка.
	gtx4, _ := newTestContext(800, 650)
	layoutTranscriptionHistory(gtx4, th, []UIMessage{{Type: History, Text: "single"}}, fs, &o.transcriptionList, false)
	if got := o.transcriptionList.Position.Length; got != wantFS {
		t.Errorf("зона 4 одна строка: высота контента = %d, want %d (единый fs=%d)", got, wantFS, fs)
	}

	// Зона 3 (подсказки): одна строка Source.
	gtx3, _ := newTestContext(800, 650)
	layoutAnswers(gtx3, th, UIMessage{Type: AnswerCandidates, Answers: answersFrom("single")}, fs, &o.answersList, false)
	if got := o.answersList.Position.Length; got != wantFS {
		t.Errorf("зона 3 одна строка: высота контента = %d, want %d (единый fs=%d)", got, wantFS, fs)
	}

	// Ошибка зоны 3 — тоже единый fs (flexible-контекст: натуральная высота).
	gtxErr, _ := newLabelContext(200)
	dimsErr := layoutError(gtxErr, th, UIMessage{Type: Error, Text: "x"}, fs)
	if dimsErr.Size.Y != wantFS {
		t.Errorf("зона 3 ошибка: высота = %d, want %d (единый fs=%d)", dimsErr.Size.Y, wantFS, fs)
	}
}
