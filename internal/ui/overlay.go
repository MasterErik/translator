package ui

import (
	"context"
	"image"
	"image/color"
	"log/slog"
	"sync"

	"gioui.org/app"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

// Overlay — прозрачное окно с четырьмя зонами.
type Overlay struct {
	cfg      OverlayConfig
	messages []UIMessage
	mu       sync.RWMutex
	shutdown chan struct{}

	invalidate func()
}

func NewOverlay(cfg OverlayConfig) *Overlay {
	if cfg.Width <= 0 {
		cfg.Width = 800
	}
	if cfg.Height <= 0 {
		cfg.Height = 400
	}
	if cfg.FontSize <= 0 {
		cfg.FontSize = 18
	}
	if cfg.MaxLines <= 0 {
		cfg.MaxLines = 10
	}
	return &Overlay{
		cfg:      cfg,
		messages: make([]UIMessage, 0),
		shutdown: make(chan struct{}),
	}
}

// AddMessage — Interim и AnswerCandidates заменяются. Translation "done" добавляется,
// pending/streaming заменяют последний незавершённый перевод. History — просто append.
func (o *Overlay) AddMessage(msg UIMessage) {
	o.mu.Lock()
	defer o.mu.Unlock()

	switch msg.Type {
	case Interim:
		// Только последний interim.
		for i := len(o.messages) - 1; i >= 0; i-- {
			if o.messages[i].Type == Interim {
				o.messages[i] = msg
				o.invalidateIf()
				return
			}
		}
		o.messages = append(o.messages, msg)

	case Translation:
		if msg.MsgStatus == "done" {
			// Удаляем pending/streaming, добавляем done.
			o.removePendingTranslations()
			o.messages = append(o.messages, msg)
		} else {
			// pending/streaming: заменяем последний незавершённый.
			for i := len(o.messages) - 1; i >= 0; i-- {
				if o.messages[i].Type == Translation && o.messages[i].MsgStatus != "done" {
					o.messages[i] = msg
					o.invalidateIf()
					return
				}
			}
			o.messages = append(o.messages, msg)
		}

	case AnswerCandidates:
		for i := len(o.messages) - 1; i >= 0; i-- {
			if o.messages[i].Type == AnswerCandidates {
				o.messages[i] = msg
				o.invalidateIf()
				return
			}
		}
		o.messages = append(o.messages, msg)

	case History:
		// История: просто добавляем, не заменяем.
		o.messages = append(o.messages, msg)

	default:
		o.messages = append(o.messages, msg)
	}

	o.invalidateIf()
}

func (o *Overlay) invalidateIf() {
	if o.invalidate != nil {
		o.invalidate()
	}
}

func (o *Overlay) removePendingTranslations() {
	for i := len(o.messages) - 1; i >= 0; i-- {
		if o.messages[i].Type == Translation && o.messages[i].MsgStatus != "done" {
			o.messages = append(o.messages[:i], o.messages[i+1:]...)
		}
	}
}

func (o *Overlay) GetMessages() []UIMessage {
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := make([]UIMessage, len(o.messages))
	copy(out, o.messages)
	return out
}

// ── GioUI Window ──

func (o *Overlay) Run(ctx context.Context) error {
	var w app.Window
	w.Option(
		app.Title(""),
		app.Size(unit.Dp(o.cfg.Width), unit.Dp(o.cfg.Height)),
		app.TopMost(true),
	)
	o.invalidate = func() { w.Invalidate() }

	th := material.NewTheme()
	var ops op.Ops

	go o.applyWindowStyles()
	defer close(o.shutdown)

	slog.Info("UI-оверлей запущен", "width", o.cfg.Width, "height", o.cfg.Height)

	for {
		select {
		case <-ctx.Done():
			w.Perform(system.ActionClose)
			for {
				evt := w.Event()
				if _, ok := evt.(app.DestroyEvent); ok {
					return ctx.Err()
				}
			}
		default:
		}

		evt := w.Event()
		switch e := evt.(type) {
		case app.DestroyEvent:
			return e.Err
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			o.render(gtx, th)
			e.Frame(gtx.Ops)
		}
	}
}

func (o *Overlay) WaitShutdown()      { <-o.shutdown }
func (o *Overlay) applyWindowStyles() {}

// ── Rendering: четыре зоны ──

func (o *Overlay) render(gtx layout.Context, th *material.Theme) layout.Dimensions {
	o.mu.RLock()
	interim := o.lastInterim()
	streaming, hasStreaming := o.streamingTranslation()
	lastDone, hasDone := o.lastDone()
	answers, hasAnswers := o.lastAnswers()
	history := o.historyMessages()
	o.mu.RUnlock()

	bg := color.NRGBA{R: 0, G: 0, B: 0, A: 180}
	if hasStreaming {
		bg = statusColor(streaming.MsgStatus)
	}
	paintBackground(gtx, bg)

	fs := o.cfg.FontSize

	layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		// 1. Речь.
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layoutInterim(gtx, th, interim, fs+2)
		}),

		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layoutZoneSeparator(gtx)
		}),

		// 2. Перевод.
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layoutTranslation(gtx, th, lastDone, hasDone, streaming, hasStreaming, fs)
		}),

		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layoutZoneSeparator(gtx)
		}),

		// 3. Подсказки.
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if !hasAnswers {
				return layout.Dimensions{}
			}
			return layoutAnswers(gtx, th, answers, fs)
		}),

		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layoutZoneSeparator(gtx)
		}),

		// 4. История (скролл).
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layoutHistory(gtx, th, history, fs)
		}),
	)

	return layout.Dimensions{Size: gtx.Constraints.Max}
}

func (o *Overlay) lastInterim() UIMessage {
	for i := len(o.messages) - 1; i >= 0; i-- {
		if o.messages[i].Type == Interim {
			return o.messages[i]
		}
	}
	return UIMessage{}
}

func (o *Overlay) streamingTranslation() (UIMessage, bool) {
	for i := len(o.messages) - 1; i >= 0; i-- {
		if o.messages[i].Type == Translation && o.messages[i].MsgStatus != "done" {
			return o.messages[i], true
		}
	}
	return UIMessage{}, false
}

func (o *Overlay) lastDone() (UIMessage, bool) {
	for i := len(o.messages) - 1; i >= 0; i-- {
		if o.messages[i].Type == Translation && o.messages[i].MsgStatus == "done" {
			return o.messages[i], true
		}
	}
	return UIMessage{}, false
}

func (o *Overlay) lastAnswers() (UIMessage, bool) {
	for i := len(o.messages) - 1; i >= 0; i-- {
		if o.messages[i].Type == AnswerCandidates && len(o.messages[i].Answers) > 0 {
			return o.messages[i], true
		}
	}
	return UIMessage{}, false
}

func (o *Overlay) historyMessages() []UIMessage {
	var out []UIMessage
	for _, m := range o.messages {
		if m.Type == History {
			out = append(out, m)
		}
	}
	return out
}

// ── Zone renderers ──

// layoutZoneSeparator — разделитель между зонами (3px).
func layoutZoneSeparator(gtx layout.Context) layout.Dimensions {
	h := 3
	rect := clip.Rect{Max: image.Pt(gtx.Constraints.Max.X, h)}
	defer rect.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, color.NRGBA{R: 60, G: 60, B: 80, A: 255})
	return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, h)}
}

func layoutInterim(gtx layout.Context, th *material.Theme, msg UIMessage, fs int) layout.Dimensions {
	if msg.Text == "" {
		return layout.Dimensions{Size: gtx.Constraints.Max}
	}
	label := material.Label(th, unit.Sp(fs), msg.Text)
	label.Color = color.NRGBA{R: 200, G: 200, B: 200, A: 255}
	label.Alignment = text.Start
	label.MaxLines = 2
	return label.Layout(gtx)
}

func layoutSeparator(gtx layout.Context) layout.Dimensions {
	h := 2
	rect := clip.Rect{Max: image.Pt(gtx.Constraints.Max.X, h)}
	defer rect.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, color.NRGBA{R: 100, G: 100, B: 100, A: 100})
	return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, h)}
}

// layoutTranslation показывает ТОЛЬКО текущий перевод (streaming или последний done).
// Не накапливает историю переводов. MaxLines = 5.
func layoutTranslation(gtx layout.Context, th *material.Theme, lastDone UIMessage, hasDone bool, streaming UIMessage, hasStreaming bool, fs int) layout.Dimensions {
	// Приоритет: streaming > последний done.
	if hasStreaming && streaming.Text != "" && streaming.Text != "[переводится...]" {
		l := material.Label(th, unit.Sp(fs), streaming.Text)
		l.Color = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
		l.Alignment = text.Start
		l.MaxLines = 5
		return l.Layout(gtx)
	}

	if hasStreaming {
		// Показываем "[переводится...]" (pending).
		l := material.Label(th, unit.Sp(fs), "[переводится...]")
		l.Color = color.NRGBA{R: 255, G: 200, B: 50, A: 255}
		l.Alignment = text.Start
		l.MaxLines = 5
		return l.Layout(gtx)
	}

	if hasDone && lastDone.Text != "" {
		l := material.Label(th, unit.Sp(fs), lastDone.Text)
		l.Color = color.NRGBA{R: 255, G: 255, B: 255, A: 220}
		l.Alignment = text.Start
		l.MaxLines = 5
		return l.Layout(gtx)
	}

	return layout.Dimensions{}
}

func layoutAnswers(gtx layout.Context, th *material.Theme, msg UIMessage, fs int) layout.Dimensions {
	afs := fs - 2
	if afs < 10 {
		afs = 10
	}
	children := make([]layout.FlexChild, 0, len(msg.Answers)+1)

	// Разделитель перед подсказками.
	children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layoutSeparator(gtx)
	}))

	for i, ans := range msg.Answers {
		idx, ansText := i, ans
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Label(th, unit.Sp(afs), formatAnswer(idx+1, ansText))
			l.Color = color.NRGBA{R: 144, G: 238, B: 144, A: 255}
			l.Alignment = text.Start
			l.MaxLines = 1
			return l.Layout(gtx)
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

// layoutHistory — вертикальный список всех History-сообщений со скроллом.
// Каждый элемент показывает оригинал (серым) и перевод (белым).
func layoutHistory(gtx layout.Context, th *material.Theme, history []UIMessage, fs int) layout.Dimensions {
	if len(history) == 0 {
		return layout.Dimensions{}
	}

	hfs := fs - 2
	if hfs < 10 {
		hfs = 10
	}

	list := &layout.List{Axis: layout.Vertical}
	return list.Layout(gtx, len(history), func(gtx layout.Context, i int) layout.Dimensions {
		m := history[i]

		// Показываем оригинал (серым) и перевод (белым) в две строки.
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, unit.Sp(hfs), m.Text)
				l.Color = color.NRGBA{R: 180, G: 180, B: 180, A: 200}
				l.Alignment = text.Start
				l.MaxLines = 1
				return l.Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if m.Translation == "" {
					return layout.Dimensions{}
				}
				l := material.Label(th, unit.Sp(hfs), m.Translation)
				l.Color = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
				l.Alignment = text.Start
				l.MaxLines = 2
				return l.Layout(gtx)
			}),
		)
	})
}

// ── Helpers ──

func statusColor(status string) color.NRGBA {
	switch status {
	case "pending":
		return color.NRGBA{R: 80, G: 60, B: 0, A: 200}
	case "streaming":
		return color.NRGBA{R: 0, G: 80, B: 40, A: 200}
	default:
		return color.NRGBA{R: 0, G: 0, B: 0, A: 180}
	}
}

func paintBackground(gtx layout.Context, c color.NRGBA) {
	defer clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, c)
}

func formatAnswer(num int, text string) string {
	if num >= 1 && num <= 9 {
		return string(rune('0'+num)) + ". " + text
	}
	return text
}
