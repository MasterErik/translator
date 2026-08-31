package ui

import (
	"image"
	"testing"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

// measureLineHeight — высота строки Label в px при заданной ширине (w) и
// опциональной настройке label. При w=200 текст не переносится (одна строка,
// высота = глиф-бокс); при узкой ширине текст переносится — высота = сумма
// межстрочных продвижений, где и проявляется LineHeight.
func measureLineHeight(th *material.Theme, fs, w int, text string, configure func(*material.LabelStyle)) int {
	l := material.Label(th, unit.Sp(fs), text)
	if configure != nil {
		configure(&l)
	}
	ops := new(op.Ops)
	gtx := layout.Context{
		Ops: ops,
		Constraints: layout.Constraints{
			Min: image.Pt(0, 0),
			Max: image.Pt(w, 100000),
		},
		Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
	}
	return l.Layout(gtx).Size.Y
}

// TestBaselineLineHeight — база: высота одной (неперенесённой) строки Label при
// fs=18 — глиф-бокс (ascent+descent), H0 ≈ 26px.
func TestBaselineLineHeight(t *testing.T) {
	th := material.NewTheme()
	h0 := measureLineHeight(th, 18, 200, "Hg", nil)
	t.Logf("H0 (глиф-бокс строки fs=18) = %d px", h0)
	if h0 <= 0 {
		t.Fatalf("H0 = %d, ожидалась > 0", h0)
	}
	if h0 > 100 {
		t.Fatalf("H0 = %d — подозрительно велико", h0)
	}
}

// TestOverlayLabelCompactLineHeight — межстрочный интервал уменьшен вдвое:
// при переносе на несколько строк (узкая ширина) высота label'а из helper
// overlayLabel должна быть ≤ (H0_def * N) * 0.7 + допуск, где H0_def — высота
// по дефолтной метрике. Проще и нагляднее: компактный многострочный label
// ниже дефолтного при одинаковом тексте, и выигрыш ≈ половина прироста.
func TestOverlayLabelCompactLineHeight(t *testing.T) {
	th := material.NewTheme()
	const (
		fs = 18
		w  = 40 // узкая ширина -> перенос на 3+ строки
	)
	text := "word word word word"

	// Дефолтная метрика: много строк, каждая продвигает baseline на ~1.44em.
	def := measureLineHeight(th, fs, w, text, func(l *material.LabelStyle) {})
	got := measureLineHeight(th, fs, w, text, func(l *material.LabelStyle) {
		*l = overlayLabel(th, unit.Sp(fs), l.Text)
	})

	t.Logf("multi-line default = %d px, compact = %d px", def, got)
	if def <= 0 {
		t.Fatal("базовая высота многострочного label = 0")
	}
	// Компактный многострочный label ниже дефолтного как минимум на 20%.
	if got >= def*80/100 {
		t.Errorf("compact = %d, want < %d (80%% от дефолта %d)", got, def*80/100, def)
	}
	if got <= 0 {
		t.Errorf("compact = %d, ожидалась > 0", got)
	}
}
