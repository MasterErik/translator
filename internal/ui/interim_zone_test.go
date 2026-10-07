package ui

import (
	"strings"
	"testing"

	"gioui.org/layout"
	"gioui.org/widget/material"

	"github.com/mastererik/translator/internal/logger"
)

// TestInterimZoneEmptyScaffold — пустая зона 1: каркас фиксированной высоты
// interimVisibleLines × emptyZoneHeight(fs), ширина окна (Task 1.2, п.4).
func TestInterimZoneEmptyScaffold(t *testing.T) {
	const fs = 18
	th := material.NewTheme()
	var list layout.List
	gtx, _ := newTestContext(800, 650)

	dims := layoutInterim(gtx, th, UIMessage{}, fs, &list, false)
	if dims.Size.X != 800 {
		t.Errorf("empty interim width = %d, want 800", dims.Size.X)
	}
	if want := emptyZoneHeight(fs) * interimVisibleLines; dims.Size.Y != want {
		t.Errorf("empty interim height = %d, want %d (%d строк × %d)", dims.Size.Y, want, interimVisibleLines, emptyZoneHeight(fs))
	}
}

// TestInterimZoneScrollableFullText — Task 1.2 (RED→GREEN): зона 1 скроллируется.
// Interim длиннее видимой высоты рендерится целиком через персистентный
// interimList; после кадра список доскроллен к концу (Position.BeforeEnd == false),
// полный текст сохранён без обрезки (нет MaxLines), окно не раздувается.
func TestInterimZoneScrollableFullText(t *testing.T) {
	const fs = 18
	th := material.NewTheme()
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: fs}, logger.NewNopSessionLogger())

	// Фраза заведомо выше видимой высоты зоны 1 (3 строки) при ширине 800.
	long := strings.Repeat("word ", 300) + "END_MARKER"
	o.AddMessage(UIMessage{Type: Interim, Text: long})

	gtx, _ := newTestContext(800, 650)
	o.render(gtx, th)
	if gtx.Constraints.Max.X != 800 || gtx.Constraints.Max.Y != 650 {
		t.Fatalf("render занимает %v, want 800x650 (зона 1 не раздувает окно)", gtx.Constraints.Max)
	}

	// Полный текст без обрезки в буфере overlay (нет MaxLines в зоне 1).
	if m := o.lastInterim(); m.Text != long {
		t.Errorf("lastInterim.Text обрезан: len=%d, want len=%d", len(m.Text), len(long))
	}

	// Персистентный список доскроллен к концу (автоскролл зоны 1).
	if o.interimList.Position.BeforeEnd {
		t.Errorf("interimList.Position.BeforeEnd = true, want false (автоскролл к концу)")
	}

	// Контент действительно длиннее видимой высоты (иначе тест не о том).
	visible := emptyZoneHeight(fs) * interimVisibleLines
	if o.interimList.Position.Length <= visible {
		t.Fatalf("контент (%d) не превышает видимую высоту (%d) — тест некорректен", o.interimList.Position.Length, visible)
	}
}

// TestInterimZoneFixedHeightInRender — зона 1 Rigid: вне зависимости от длины
// текста каркас окна неизменен (800x650), текст скроллится внутри.
func TestInterimZoneFixedHeightInRender(t *testing.T) {
	th := material.NewTheme()
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: 18}, logger.NewNopSessionLogger())
	o.AddMessage(UIMessage{Type: Interim, Text: strings.Repeat("word ", 400)})

	gtx, _ := newTestContext(800, 650)
	o.render(gtx, th)
	if gtx.Constraints.Max.X != 800 || gtx.Constraints.Max.Y != 650 {
		t.Errorf("render занимает %v, want 800x650", gtx.Constraints.Max)
	}

	// Зона 1 (Rigid) фиксирована: при тексте высота = interimVisibleLines строк.
	visGtx, _ := newTestContext(800, emptyZoneHeight(18)*interimVisibleLines)
	var list layout.List
	zd := layoutInterim(visGtx, th, o.lastInterim(), 18, &list, true)
	if want := emptyZoneHeight(18) * interimVisibleLines; zd.Size.Y != want {
		t.Errorf("зона 1 при тексте: высота = %d, want %d (фиксированная)", zd.Size.Y, want)
	}
}
