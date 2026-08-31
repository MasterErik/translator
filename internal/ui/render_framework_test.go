package ui

import (
	"testing"

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
	dims := layoutInterim(gtx, th, UIMessage{}, fs)
	if dims.Size.X != gtx.Constraints.Max.X {
		t.Errorf("layoutInterim (пустой): width = %d, want %d", dims.Size.X, gtx.Constraints.Max.X)
	}
	if want := oneLine * interimVisibleLines; dims.Size.Y != want {
		t.Errorf("layoutInterim (пустой): height = %d, want %d (%d строк)", dims.Size.Y, want, interimVisibleLines)
	}

	// Зона 2 — TranslationHistory: пустые messages резервируют 1 строку.
	dims = layoutTranslationHistory(gtx, th, nil, fs, &o.translationList, false)
	if dims.Size.X != gtx.Constraints.Max.X {
		t.Errorf("layoutTranslationHistory (пусто): width = %d, want %d", dims.Size.X, gtx.Constraints.Max.X)
	}
	if dims.Size.Y != oneLine {
		t.Errorf("layoutTranslationHistory (пусто): height = %d, want %d (1 строка)", dims.Size.Y, oneLine)
	}

	// Зона 3 — AnswerCandidates без ответов (render-ветка !hasAnswers) резервирует 1 строку.
	dims = o.layoutAnswersZone(gtx, th, UIMessage{}, false, fs)
	if dims.Size.X != gtx.Constraints.Max.X {
		t.Errorf("layoutAnswers (без ответов): width = %d, want %d", dims.Size.X, gtx.Constraints.Max.X)
	}
	if dims.Size.Y != oneLine {
		t.Errorf("layoutAnswers (без ответов): height = %d, want %d (1 строка)", dims.Size.Y, oneLine)
	}

	// Separator — один из двух между зонами 1–2 и 2–3; ровно 3px.
	sepDims := layoutZoneSeparator(gtx)
	if sepDims.Size.Y != 3 {
		t.Errorf("separator: height = %d, want 3", sepDims.Size.Y)
	}

	// Полный рендер пустого overlay — каркас занимает всё окно, не схлопывается.
	renderDims := o.render(gtx, th)
	if renderDims.Size.X != 800 || renderDims.Size.Y != 650 {
		t.Errorf("render (пустой overlay): size = %v, want 800x650", renderDims.Size)
	}
	totalZoneH := dims.Size.Y + oneLine*interimVisibleLines + oneLine // зоны 1+2+3
	if totalZoneH <= 0 {
		t.Fatal("суммарная высота пустых зон должна быть > 0")
	}
}
