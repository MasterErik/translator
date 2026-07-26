package ui

import (
	"context"
	"image"
	"image/color"
	"log/slog"
	"strings"
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

	invalidate   func()
	prevHistLen  int // для автоскролла в конец

	// Персистентные списки — хранят позицию скролла между кадрами.
	translationList   layout.List
	transcriptionList layout.List

	// Для тестов: позиция скролла после последнего кадра.
	TranslationAtEnd bool
	TranscriptionAtEnd bool
}

func NewOverlay(cfg OverlayConfig) *Overlay {
	if cfg.Width <= 0 {
		cfg.Width = 1200
	}
	if cfg.Height <= 0 {
		cfg.Height = 650
	}
	if cfg.FontSize <= 0 {
		cfg.FontSize = 18
	}
	if cfg.MaxLines <= 0 {
		cfg.MaxLines = 10
	}
	return &Overlay{
		cfg:               cfg,
		messages:          make([]UIMessage, 0),
		shutdown:          make(chan struct{}),
		translationList:   layout.List{Axis: layout.Vertical},
		transcriptionList: layout.List{Axis: layout.Vertical},
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
func (o *Overlay) applyWindowStyles() {
	hwnd, err := findWindowByPID()
	if err != nil {
		slog.Warn("applyWindowStyles: не удалось найти HWND", "error", err)
		return
	}
	if err := setNoActivate(hwnd); err != nil {
		slog.Warn("applyWindowStyles: не удалось применить стили", "error", err)
	} else {
		slog.Info("applyWindowStyles: NOACTIVATE+TRANSPARENT применены", "hwnd", hwnd)
	}
}

// ── Rendering: четыре зоны ──

func (o *Overlay) render(gtx layout.Context, th *material.Theme) layout.Dimensions {
	o.mu.RLock()
	interim := o.lastInterim()
	answers, hasAnswers := o.lastAnswers()
	history := o.historyMessages()
	o.mu.RUnlock()

	// Автоскролл: запоминаем длину истории для следующего кадра.
	needScroll := len(history) > o.prevHistLen
	if needScroll {
		o.prevHistLen = len(history)
	}
	o.TranslationAtEnd = len(history) > 0
	o.TranscriptionAtEnd = len(history) > 0

	// Лог состояния зон для отладки.
	transN, origN := 0, len(history)
	for _, m := range history {
		if m.Translation != "" {
			transN++
		}
	}
	slog.Info("зоны", "interim", interim.Text, "trans", transN, "orig", origN, "answers", len(answers.Answers), "scrollEnd", o.TranslationAtEnd)

	bg := color.NRGBA{R: 0, G: 0, B: 0, A: 180}
	paintBackground(gtx, bg)

	fs := o.cfg.FontSize

	layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		// 1. Interim — речь, 2 строки, белый.
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layoutInterim(gtx, th, interim, fs)
		}),
		layout.Rigid(layoutZoneSeparator),

		// 2. Translation History — скролл, 10 строк, переводы.
		layout.Flexed(0.45, func(gtx layout.Context) layout.Dimensions {
			return layoutTranslationHistory(gtx, th, history, fs, &o.translationList, needScroll)
		}),
		layout.Rigid(layoutZoneSeparator),

		// 3. Transcription History — скролл, 8 строк, оригиналы.
		layout.Flexed(0.35, func(gtx layout.Context) layout.Dimensions {
			return layoutTranscriptionHistory(gtx, th, history, fs, &o.transcriptionList, needScroll)
		}),
		layout.Rigid(layoutZoneSeparator),

		// 4. AnswerCandidates — только для вопросов.
		layout.Flexed(0.20, func(gtx layout.Context) layout.Dimensions {
			if !hasAnswers {
				return layout.Dimensions{}
			}
			return layoutAnswers(gtx, th, answers, fs)
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
	label.Color = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	label.Alignment = text.Start
	label.MaxLines = 2
	return label.Layout(gtx)
}

// layoutAnswers — подсказка с EN и RU на отдельных строках.
func layoutAnswers(gtx layout.Context, th *material.Theme, msg UIMessage, fs int) layout.Dimensions {
	afs := fs - 2
	if afs < 10 {
		afs = 10
	}
	children := make([]layout.FlexChild, 0, len(msg.Answers)*2)
	for i, ans := range msg.Answers {
		idx := i
		en, ru := splitBilingual(ans)
		// EN — белым.
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Label(th, unit.Sp(afs), en)
			l.Color = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
			l.Alignment = text.Start
			l.MaxLines = 1
			return l.Layout(gtx)
		}))
		// RU — зелёным.
		if ru != "" {
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, unit.Sp(afs), ru)
				l.Color = color.NRGBA{R: 144, G: 238, B: 144, A: 255}
				l.Alignment = text.Start
				l.MaxLines = 1
				return l.Layout(gtx)
			}))
		}
		_ = idx
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

// splitBilingual разбивает строку формата "EN: ... | RU: ..." на две части.
func splitBilingual(s string) (en, ru string) {
	parts := strings.SplitN(s, "| RU:", 2)
	en = strings.TrimSpace(parts[0])
	if len(parts) == 2 {
		ru = "RU:" + parts[1]
	}
	return
}

// layoutTranslationHistory — скролл переводов из History (10 строк).
func layoutTranslationHistory(gtx layout.Context, th *material.Theme, history []UIMessage, fs int, list *layout.List, needScroll bool) layout.Dimensions {
	// Фильтруем только записи с переводом.
	var items []UIMessage
	for _, m := range history {
		if m.Translation != "" {
			items = append(items, m)
		}
	}
	if len(items) == 0 {
		return layout.Dimensions{}
	}

	hfs := fs - 2
	if hfs < 10 {
		hfs = 10
	}
	list.Axis = layout.Vertical
	if needScroll && len(items) > 0 {
		list.ScrollTo(len(items) - 1)
	}
	return list.Layout(gtx, len(items), func(gtx layout.Context, i int) layout.Dimensions {
		l := material.Label(th, unit.Sp(hfs), items[i].Translation)
		l.Color = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
		l.Alignment = text.Start
		l.MaxLines = 2
		return l.Layout(gtx)
	})
}

// layoutTranscriptionHistory — скролл оригиналов речи из History (5 строк).
func layoutTranscriptionHistory(gtx layout.Context, th *material.Theme, history []UIMessage, fs int, list *layout.List, needScroll bool) layout.Dimensions {
	if len(history) == 0 {
		return layout.Dimensions{}
	}

	hfs := fs - 2
	if hfs < 10 {
		hfs = 10
	}
	list.Axis = layout.Vertical
	if needScroll && len(history) > 0 {
		list.ScrollTo(len(history) - 1)
	}
	return list.Layout(gtx, len(history), func(gtx layout.Context, i int) layout.Dimensions {
		l := material.Label(th, unit.Sp(fs), history[i].Text)
		l.Color = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
		l.Alignment = text.Start
		l.MaxLines = 8
		return l.Layout(gtx)
	})
}

// ── Helpers ──

func paintBackground(gtx layout.Context, c color.NRGBA) {
	defer clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, c)
}
