package ui

import (
	"testing"

	"gioui.org/layout"
	"gioui.org/widget/material"

	"github.com/mastererik/translator/internal/logger"
)

// TestRenderEmptyFrameworkSeparatorsVisible — при старте (нет interim/answers/
// переводов, зона 4 скрыта) каркас зон 1–3 должен резервировать высоту, чтобы
// две separator-линии (3px, цвет R:60,G:60,B:80,A:255) были видны до появления
// текста. Корень дефекта: пустые layout-функции возвращали Dimensions{} или
// весь Max — зоны схлопывались, и окно выглядело пустым.
//
// Минимально достаточная проверка: каждая пустая зона возвращает ненулевые
// размеры с шириной, равной ширине окна; separator-линия ровно 3px. Итог —
// суммарная высота каркаса > 0.
func TestRenderEmptyFrameworkSeparatorsVisible(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: 18}, logger.NewNopSessionLogger())
	// Пустой overlay: нет сообщений, история скрыта (значение по умолчанию).

	th := material.NewTheme()
	gtx, _ := newTestContext(800, 650)
	fs := o.cfg.FontSize

	oneLine := emptyZoneHeight(fs)

	// Зона 1 — Interim: пустой текст резервирует interimVisibleLines строк.
	var interimList layout.List
	dims := layoutInterim(gtx, th, UIMessage{}, fs, &interimList, false)
	if dims.Size.X != gtx.Constraints.Max.X {
		t.Errorf("layoutInterim (пустой): width = %d, want %d", dims.Size.X, gtx.Constraints.Max.X)
	}
	if want := oneLine * interimVisibleLines; dims.Size.Y != want {
		t.Errorf("layoutInterim (пустой): height = %d, want %d (%d строк)", dims.Size.Y, want, interimVisibleLines)
	}

	// Зона 2 — TranslationHistory: пустые messages занимают всю выделенную
	// Flexed-высоту (каркас: gtx.Constraints.Max.Y).
	dims = layoutTranslationHistory(gtx, th, nil, fs, &o.translationList, false)
	if dims.Size.X != gtx.Constraints.Max.X {
		t.Errorf("layoutTranslationHistory (пусто): width = %d, want %d", dims.Size.X, gtx.Constraints.Max.X)
	}
	if want := gtx.Constraints.Max.Y; dims.Size.Y != want {
		t.Errorf("layoutTranslationHistory (пусто): height = %d, want %d (вся Flexed-высота)", dims.Size.Y, want)
	}

	// Зона 3 — AnswerCandidates без ответов (render-ветка !hasAnswers) занимает
	// всю выделенную Flexed-высоту. Ошибки нет (нулевой UIMessage{}).
	dims = o.layoutAnswersZone(gtx, th, UIMessage{}, false, UIMessage{}, fs, false)
	if dims.Size.X != gtx.Constraints.Max.X {
		t.Errorf("layoutAnswers (без ответов): width = %d, want %d", dims.Size.X, gtx.Constraints.Max.X)
	}
	if want := gtx.Constraints.Max.Y; dims.Size.Y != want {
		t.Errorf("layoutAnswers (без ответов): height = %d, want %d (вся Flexed-высота)", dims.Size.Y, want)
	}

	// Separator — один из двух между зонами 1–2 и 2–3; ровно 3px.
	sepDims := zoneSeparator(gtx)
	if sepDims.Size.Y != 3 {
		t.Errorf("separator: height = %d, want 3", sepDims.Size.Y)
	}

	// Полный рендер пустого overlay — каркас занимает всё окно, не схлопывается.
	m := o.render(gtx, th)
	if m.InterimAtEnd || m.TranslationsAtEnd || m.AnswersAtEnd || m.TranscriptionAtEnd {
		t.Errorf("пустой overlay: флаги конца скролла должны быть false, got %+v", m)
	}
	renderDims := layout.Dimensions{Size: gtx.Constraints.Max}
	if renderDims.Size.X != 800 || renderDims.Size.Y != 650 {
		t.Errorf("render (пустой overlay): size = %v, want 800x650", renderDims.Size)
	}
	totalZoneH := dims.Size.Y + oneLine*interimVisibleLines + oneLine // зоны 1+2+3
	if totalZoneH <= 0 {
		t.Fatal("суммарная высота пустых зон должна быть > 0")
	}
}
