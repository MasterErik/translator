package ui

import (
	"image"
	"testing"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

// measureLabel — размеры Label в px при заданной ширине (w) и опциональной
// настройке. При w=200 текст не переносится (одна строка); при узкой ширине
// текст переносится — высота = сумма межстрочных продвижений (LineHeight).
func measureLabel(th *material.Theme, fs, w int, text string, configure func(*material.LabelStyle)) image.Point {
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
	return l.Layout(gtx).Size
}

// TestOverlayLabelSingleLineUnscaled — шрифт НЕ уменьшен: однострочный label
// через overlayLabel имеет те же размеры, что дефолтный material.Label
// (LineHeightScale не переопределяется, глифы не масштабируются и не режутся).
func TestOverlayLabelSingleLineUnscaled(t *testing.T) {
	th := material.NewTheme()
	const fs = 18
	text := "Hg"

	def := measureLabel(th, fs, 200, text, nil)
	got := measureLabel(th, fs, 200, text, func(l *material.LabelStyle) {
		*l = overlayLabel(th, unit.Sp(fs), l.Text)
	})

	t.Logf("single-line: default %v, overlay %v", def, got)
	if got != def {
		t.Errorf("single-line overlay = %v, want %v (шрифт не должен масштабироваться)", got, def)
	}
}

// TestOverlayLabelMultiLineCompact — многострочный label через overlayLabel
// ниже дефолтного (межстрочный зазор сокращён), но строки не обрезаются:
// высота ≥ (число строк) × (высота глиф-бокса однострочного label).
func TestOverlayLabelMultiLineCompact(t *testing.T) {
	th := material.NewTheme()
	const (
		fs = 18
		w  = 40 // узкая ширина -> перенос
	)
	text := "word word word word word"

	glyphBox := measureLabel(th, fs, 200, "Hg", nil).Y

	def := measureLabel(th, fs, w, text, nil)
	got := measureLabel(th, fs, w, text, func(l *material.LabelStyle) {
		*l = overlayLabel(th, unit.Sp(fs), l.Text)
	})

	lines := def.Y / glyphBox
	t.Logf("glyph-box=%d, default=%d, overlay=%d, lines≈%d", glyphBox, def.Y, got.Y, lines)

	if lines < 3 {
		t.Fatalf("тест некорректен: перенос не произошёл (lines=%d)", lines)
	}
	// Интервал сокращён: компактный ниже дефолтного.
	if got.Y >= def.Y {
		t.Errorf("overlay multi-line = %d, want < default %d (интервал должен сократиться)", got.Y, def.Y)
	}
	// Строки не схлопываются и перекрытие глифов ограничено: фактический
	// advance на строку (высота/кол-во строк) ≥ 80% дефолтного продвижения.
	adv := float32(got.Y) / float32(lines)
	defAdv := float32(def.Y) / float32(lines)
	if adv < defAdv*0.8 {
		t.Errorf("advance/строку = %.1f < 80%% дефолтного %.1f — чрезмерное перекрытие", adv, defAdv)
	}
}
