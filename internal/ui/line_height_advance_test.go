package ui

import (
	"math"
	"testing"

	"gioui.org/unit"
	"gioui.org/widget/material"
)

// measureLineAdvance — фактическое межстрочное продвижение (advance) на строку
// при метрике overlayLabel: высота label с 8 явными строками при широкой
// ширине (без переноса) / 8. Совпадает с высотой, которую резервирует зона
// на строку при отрисовке текста.
func measureLineAdvance(fs int) float64 {
	th := material.NewTheme()
	full := ""
	for i := 0; i < 8; i++ {
		full += "Hg\n"
	}
	full = full[:len(full)-1]
	got := measureLabel(th, fs, 500, full, func(l *material.LabelStyle) {
		*l = overlayLabel(th, unit.Sp(fs), l.Text)
	})
	return float64(got.Y) / 8.0
}

// TestLineHeightAtMatchesMeasuredAdvance — lineHeightAt(fs) равен измеренному
// advance одной строки (в пределах 1px): каркасные высоты совпадают с теми,
// куда зоны придут при появлении текста.
func TestLineHeightAtMatchesMeasuredAdvance(t *testing.T) {
	for _, fs := range []int{18, 10, 24} {
		adv := measureLineAdvance(fs)
		got := lineHeightAt(fs)
		if math.Abs(float64(got)-adv) > 1.0 {
			t.Errorf("lineHeightAt(%d) = %d, измеренный advance = %.2f (расхождение > 1px)", fs, got, adv)
		}
		if got != emptyZoneHeight(fs) {
			t.Errorf("emptyZoneHeight(%d) = %d, want == lineHeightAt = %d", fs, emptyZoneHeight(fs), got)
		}
		t.Logf("fs=%d: lineHeightAt=%d, advance=%.2f", fs, got, adv)
	}
}

// TestHistoryVisibleHeightPxMatchesMeasuredAdvance — historyVisibleHeightPx(fs)
// == measured advance(hfs) × historyVisibleLines (в пределах 2px).
func TestHistoryVisibleHeightPxMatchesMeasuredAdvance(t *testing.T) {
	for _, fs := range []int{18, 10, 24} {
		hfs := fs - 2
		if hfs < 10 {
			hfs = 10
		}
		advH := measureLineAdvance(hfs)
		want := advH * float64(historyVisibleLines)
		got := historyVisibleHeightPx(fs)
		if math.Abs(float64(got)-want) > 2.0 {
			t.Errorf("historyVisibleHeightPx(%d) = %d, want measured advance(hfs=%d)×4 = %.2f (расхождение > 2px)", fs, got, hfs, want)
		}
		t.Logf("fs=%d: historyVisibleHeightPx=%d, advance(hfs=%d)=%.2f ×4=%.2f", fs, got, hfs, advH, want)
	}
}
