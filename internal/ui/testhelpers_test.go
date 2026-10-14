package ui

import (
	"image"
	"reflect"

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

// opsByteLen возвращает длину сериализованного op-потока Gio (байты) — примитив
// для поведенческой проверки «рендер байт-в-байт идентичен». Публичного доступа
// к длине op-потока в Gio v0.10.1 нет (op.Ops.Internal.data неэкспортировано),
// поэтому читаем поле через reflect; если структура Gio изменится и поля не
// найдётся, возвращаем -1 — вызывающий тест пропускает сравнение длины и
// опирается на Dimensions. Так тест не падает при апгрейде Gio, но и не молчит
// об отсутствии проверки.
func opsByteLen(o *op.Ops) int {
	v := reflect.ValueOf(o)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		return -1
	}
	data := v.Elem().FieldByName("Internal").FieldByName("data")
	if !data.IsValid() || data.Kind() != reflect.Slice {
		return -1
	}
	return data.Len()
}
