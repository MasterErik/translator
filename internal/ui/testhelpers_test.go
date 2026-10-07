package ui

import (
	"image"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

// answersFrom builds a []Answer from raw source strings, leaving Target empty.
// Shared by ui tests that only care about the number/text of hints.
func answersFrom(sources ...string) []Answer {
	out := make([]Answer, len(sources))
	for i, s := range sources {
		out[i] = Answer{Source: s}
	}
	return out
}

// newTestContext — layout.Context с фиксированными размерами окна,
// без реального окна (для юнит-тестов render). Консолидирован здесь (DRY):
// подготовка offscreen-контекста использовалась в нескольких файлах.
func newTestContext(width, height int) (layout.Context, *op.Ops) {
	ops := new(op.Ops)
	gtx := layout.Context{
		Ops:         ops,
		Constraints: layout.Exact(image.Pt(width, height)),
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
	}
	return gtx, ops
}

// newLabelContext — layout.Context с фиксированной шириной и метрикой
// PxPerSp=1: размеры однострочного label в px совпадают со sp.
func newLabelContext(width int) (layout.Context, *op.Ops) {
	ops := new(op.Ops)
	gtx := layout.Context{
		Ops: ops,
		Constraints: layout.Constraints{
			Min: image.Pt(0, 0),
			Max: image.Pt(width, 100000),
		},
		Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
	}
	return gtx, ops
}

// renderFrame — общий шаг «offscreen-рендер кадра»: контекст заданного размера,
// рендер оверлея, возврат метрик кадра. Консолидирует повторяющуюся подготовку
// newTestContext + render (DRY).
func renderFrame(o *Overlay, width, height int) FrameMetrics {
	th := material.NewTheme()
	gtx, _ := newTestContext(width, height)
	return o.render(gtx, th)
}
