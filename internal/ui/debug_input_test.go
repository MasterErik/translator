package ui

import (
	"image"
	"testing"
	"time"

	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"github.com/mastererik/translator/internal/logger"
)

// ── Debug-режим ручного ввода (DEBUG_INPUT): зона ввода и Submit ──
//
// Ручной рендер зоны вне Run (offscreen): layout.List и widget.Editor не
// потокобезопасны, Run крутит кадры в горутине окна, поэтому тесты вызывают
// layoutDebugInput/render напрямую, как clarifications_test.go.
//
// Протокол ввода Gio (выяснен экспериментально, см. отчёт):
// widget.Editor регистрирует key-фильтры ТОЛЬКО в Update/Layout кадра, а
// input.Router.Queue сопоставляет key.Event с фильтрами, ЗАФИКСИРОВАННЫМИ
// последним Router.Frame. Поэтому key.Event{Return} нужно ставить в очередь
// ПОСЛЕ кадра, в котором Layout выполнен и Router.Frame закоммитил фильтр, а
// читать SubmitEvent — следующим Update. Рецепт в newDebugHarness/enter.

// debugHarness — минимальная обвязка для offscreen-теста зоны ручного ввода:
// Router (источник ввода), один Overlay с установленным каналом и общий сборщик
// gtx. Theme с настоящим Shaper (material.NewTheme по умолчанию шейпер уже имеет,
// явный text.NewShaper не нужен, но фиксируем для предсказуемости).
type debugHarness struct {
	t  *testing.T
	o  *Overlay
	r  *input.Router
	th *material.Theme
	ch chan string
}

func newDebugHarness(t *testing.T, ch chan<- string) *debugHarness {
	t.Helper()
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: 18, DebugInput: true},
		logger.NewNopSessionLogger())
	th := material.NewTheme()
	th.Shaper = text.NewShaper()
	var r input.Router
	o.SetManualInput(ch)
	return &debugHarness{t: t, o: o, r: &r, th: th}
}

// gtx — свежий offscreen layout.Context с источником ввода от Router.
func (h *debugHarness) gtx(width, height int) (layout.Context, *op.Ops) {
	ops := new(op.Ops)
	return layout.Context{
		Ops:         ops,
		Constraints: layout.Constraints{Min: image.Pt(0, 0), Max: image.Pt(width, height)},
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Source:      h.r.Source(),
	}, ops
}

// focusFrame — кадр, в котором editor получает фокус: FocusCmd + Layout +
// Router.Frame (коммит обработчиков/фильтров).
func (h *debugHarness) focusFrame() {
	h.t.Helper()
	gtx, ops := h.gtx(800, 60)
	gtx.Execute(key.FocusCmd{Tag: &h.o.debugEditor})
	if _, _ = h.o.layoutDebugInput(gtx, h.th); !gtx.Focused(&h.o.debugEditor) {
		h.t.Fatalf("editor не получил фокус после FocusCmd")
	}
	h.r.Frame(ops)
}

// typeText — кадр вставки текста через key.EditEvent (полный протокол Gio).
func (h *debugHarness) typeText(s string) {
	h.t.Helper()
	gtx, ops := h.gtx(800, 60)
	h.r.Queue(key.EditEvent{Range: key.Range{Start: 0, End: 0}, Text: s})
	gtx.Execute(key.FocusCmd{Tag: &h.o.debugEditor})
	h.o.layoutDebugInput(gtx, h.th)
	h.r.Frame(ops)
	if got := h.o.debugEditor.Text(); got != s {
		h.t.Fatalf("editor.Text()=%q, want %q (EditEvent не применился)", got, s)
	}
}

// primeFilters — кадры, закрепляющие key-фильтры editor'а в Router (Layout +
// Frame) БЕЗ отправки Enter. Двух кадров достаточно и необходимо: первый
// сливает pending ChangeEvent (который иначе заставляет processKey вернуться
// раньше регистрации Return-фильтра), второй — регистрирует сам фильтр и
// коммитит его Router.Frame. После этого Queue(key.Event{Return}) матчится.
func (h *debugHarness) primeFilters() {
	h.t.Helper()
	for i := 0; i < 2; i++ {
		gtx, ops := h.gtx(800, 60)
		gtx.Execute(key.FocusCmd{Tag: &h.o.debugEditor})
		h.o.layoutDebugInput(gtx, h.th)
		h.r.Frame(ops)
	}
}

// enter — нажимает Enter и возвращает (dims, sent) из layoutDebugInput кадра,
// в котором SubmitEvent дошёл до дренажа. Порядок ровно как требует Router:
// primeFilters() уже выполнен, очередь ставится ПОСЛЕ последнего Frame.
func (h *debugHarness) enter() (layout.Dimensions, string) {
	h.t.Helper()
	h.r.Queue(key.Event{Name: key.NameReturn, State: key.Press})
	gtx, ops := h.gtx(800, 60)
	gtx.Execute(key.FocusCmd{Tag: &h.o.debugEditor})
	dims, sent := h.o.layoutDebugInput(gtx, h.th)
	h.r.Frame(ops)
	return dims, sent
}

// TestDebugInputZoneInLayout — (b): при DebugInput=true и установленном канале
// зона ввода присутствует в layout (ненулевые размеры), editor принимает текст
// через EditEvent, а при снятом канале зона исчезает (нулевые размеры).
func TestDebugInputZoneInLayout(t *testing.T) {
	h := newDebugHarness(t, make(chan string, 8))

	dims, sent := h.gtxDims()
	if dims.Size.X <= 0 || dims.Size.Y <= 0 {
		t.Fatalf("зона ввода: dims=%v, want ненулевые (DebugInput=true)", dims.Size)
	}
	if sent != "" {
		t.Fatalf("sent=%q на пустом вводе, want \"\"", sent)
	}

	// Editor принимает текст: вставка через EditEvent видна в Text().
	h.focusFrame()
	h.typeText("hello world")

	// Канал не установлен — зона не рендерится (нулевые размеры): ручной ввод
	// снят, места модуль не занимает.
	h2 := newDebugHarness(t, make(chan string, 8))
	h2.o.SetManualInput(nil)
	gtx, _ := h2.gtx(800, 60)
	if d, _ := h2.o.layoutDebugInput(gtx, h2.th); d.Size != (image.Point{}) {
		t.Fatalf("без канала dims=%v, want нулевые (зона не рендерится)", d.Size)
	}
}

// gtxDims — размеры зоны на одном «холодном» кадре (без вставки/Enter).
func (h *debugHarness) gtxDims() (layout.Dimensions, string) {
	gtx, ops := h.gtx(800, 60)
	gtx.Execute(key.FocusCmd{Tag: &h.o.debugEditor})
	dims, sent := h.o.layoutDebugInput(gtx, h.th)
	h.r.Frame(ops)
	return dims, sent
}

// TestDebugInputSubmitTrimsAndSends — (c): Submit (Enter) отправляет в канал
// строку, обрезанную TrimSpace; editor очищается после отправки; sent содержит
// отправленное значение.
func TestDebugInputSubmitTrimsAndSends(t *testing.T) {
	ch := make(chan string, 8)
	h := newDebugHarness(t, ch)

	h.focusFrame()
	h.typeText("  hello world  ")
	h.primeFilters()
	dims, sent := h.enter()

	if want := "hello world"; sent != want {
		t.Fatalf("sent=%q, want %q (TrimSpace перед отправкой)", sent, want)
	}
	if dims.Size.X <= 0 || dims.Size.Y <= 0 {
		t.Fatalf("dims=%v, want ненулевые (зона отрисована в кадре отправки)", dims.Size)
	}
	select {
	case got := <-ch:
		if got != "hello world" {
			t.Fatalf("канал получил %q, want %q", got, "hello world")
		}
	default:
		t.Fatal("канал пуст после Submit — строка не отправлена")
	}
	if got := h.o.debugEditor.Text(); got != "" {
		t.Fatalf("editor.Text()=%q после Submit, want \"\" (поле очищено)", got)
	}
}

// TestDebugInputEmptyNotSent — (d): пустой и пробельный ввод не отправляется
// (канал пуст), но поле всё равно очищается. Пробельные варианты — без '\n':
// '\n' внутри EditEvent сам по себе триггерит Submit (протокол Gio), это
// отдельный сценарий (см. TestDebugInputSubmitTrimsAndSends).
func TestDebugInputEmptyNotSent(t *testing.T) {
	cases := []struct{ name, in string }{
		{"empty", ""},
		{"spaces", "   "},
		{"tab_space", "	 "},
		{"newline_only", "\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch := make(chan string, 8)
			h := newDebugHarness(t, ch)

			h.focusFrame()
			if tc.in == "\n" {
				// '\n' внутри EditEvent = немедленный Submit пустой строки.
				gtx, ops := h.gtx(800, 60)
				h.r.Queue(key.EditEvent{Range: key.Range{Start: 0, End: 0}, Text: tc.in})
				gtx.Execute(key.FocusCmd{Tag: &h.o.debugEditor})
				h.o.layoutDebugInput(gtx, h.th)
				h.r.Frame(ops)
				h.primeFilters()
			} else {
				if tc.in != "" {
					h.typeText(tc.in)
				}
				h.primeFilters()
				h.enter()
			}

			select {
			case got := <-ch:
				t.Fatalf("канал получил %q при вводе %q, want пусто (пробельный не отправляется)", got, tc.in)
			default:
			}
			if got := h.o.debugEditor.Text(); got != "" {
				t.Fatalf("editor.Text()=%q после пустого ввода %q, want \"\" (поле очищено)", got, tc.in)
			}
		})
	}
}

// TestDebugInputFullChannelDoesNotBlock — (e): полный канал не блокирует рендер:
// layoutDebugInput возвращает управление, dims корректны, строка теряется
// (неблокирующая отправка select/default). Логгер — Nop (LogDebug пути потери).
func TestDebugInputFullChannelDoesNotBlock(t *testing.T) {
	ch := make(chan string) // небуферизованный, никто не читает → переполнен
	h := newDebugHarness(t, ch)

	h.focusFrame()
	h.typeText("blocked")
	h.primeFilters()

	done := make(chan struct{})
	var dims layout.Dimensions
	var sent string
	go func() {
		dims, sent = h.enter()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("layoutDebugInput заблокировался на полном канале (нужен select/default)")
	}

	if dims.Size.X <= 0 || dims.Size.Y <= 0 {
		t.Fatalf("dims=%v, want ненулевые (рендер состоялся при полном канале)", dims.Size)
	}
	if sent != "" {
		t.Fatalf("sent=%q при полном канале, want \"\" (строка потеряна, но кадр завершён)", sent)
	}
	if got := h.o.debugEditor.Text(); got != "" {
		t.Fatalf("editor.Text()=%q после потери при полном канале, want \"\" (поле очищено)", got)
	}
}

// TestDebugInputRenderZoneWhenEnabled — зона ввода проникает в render() при
// DebugInput=true (не только в изолированном layoutDebugInput): кадр рендерится,
// и появление зоны увеличивает число separator'ов (зона ввода добавляет свой).
func TestDebugInputRenderZoneWhenEnabled(t *testing.T) {
	base := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: 18}, logger.NewNopSessionLogger())
	baseMetrics := renderFrame(base, 800, 650)

	dbg := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: 18, DebugInput: true}, logger.NewNopSessionLogger())
	dbg.SetManualInput(make(chan string, 8))
	dbgMetrics := renderFrame(dbg, 800, 650)

	if dbgMetrics.SeparatorCount != baseMetrics.SeparatorCount+1 {
		t.Fatalf("separator'ов с debug-зоной=%d, без=%d, want +1 (зона ввода в Flex)",
			dbgMetrics.SeparatorCount, baseMetrics.SeparatorCount)
	}
}
