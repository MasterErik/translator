package ui

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"strings"
	"sync"
	"time"

	"gioui.org/app"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"github.com/mastererik/translator/internal/logger"
)

// Overlay — прозрачное окно с четырьмя зонами.
type Overlay struct {
	cfg      OverlayConfig
	messages []UIMessage
	mu       sync.RWMutex
	shutdown chan struct{}

	invalidate  func()
	prevHistLen int // для автоскролла в конец

	// Персистентные списки — хранят позицию скролла между кадрами.
	translationList   layout.List
	transcriptionList layout.List
	answersList       layout.List

	// Для тестов: позиция скролла после последнего кадра.
	TranslationAtEnd   bool
	TranscriptionAtEnd bool

	sessLog logger.SessionLogger
}

func NewOverlay(cfg OverlayConfig, sessLog logger.SessionLogger) *Overlay {
	if cfg.Width <= 0 {
		cfg.Width = 1200
	}
	if cfg.Height <= 0 {
		cfg.Height = 650
	}
	if cfg.FontSize <= 0 {
		cfg.FontSize = 18
	}
	return &Overlay{
		cfg:               cfg,
		messages:          make([]UIMessage, 0),
		shutdown:          make(chan struct{}),
		translationList:   layout.List{Axis: layout.Vertical},
		transcriptionList: layout.List{Axis: layout.Vertical},
		answersList:       layout.List{Axis: layout.Vertical},
		sessLog:           sessLog,
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

	o.sessLog.LogDebug(fmt.Sprintf("UI-оверлей запущен: width=%d, height=%d", o.cfg.Width, o.cfg.Height))

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

func (o *Overlay) WaitShutdown() { <-o.shutdown }

func (o *Overlay) applyWindowStyles() {
	// Gio создаёт нативное окно асинхронно — ждём до 3 секунд с экспоненциальной задержкой.
	if !tryApplyStyles(findWindowByPID, 3*time.Second, 50*time.Millisecond) {
		o.sessLog.LogDebug("applyWindowStyles: окно не появилось за 3 секунды")
		return
	}
	if err := setNoActivate(lastFoundHwnd); err != nil {
		o.sessLog.LogDebug(fmt.Sprintf("applyWindowStyles: не удалось применить стили: error=%v", err))
	} else {
		o.sessLog.LogDebug(fmt.Sprintf("applyWindowStyles: NOACTIVATE+TRANSPARENT применены: hwnd=%v", lastFoundHwnd))
	}
}

// lastFoundHwnd — результат findWindowByPID после успешного поиска.
var lastFoundHwnd uintptr

// tryApplyStyles вызывает finder с экспоненциальной задержкой до deadline.
// Возвращает true если окно найдено, сохраняет HWND в lastFoundHwnd.
func tryApplyStyles(finder func() (uintptr, error), deadline time.Duration, initialDelay time.Duration) bool {
	dl := time.Now().Add(deadline)
	delay := initialDelay
	for time.Now().Before(dl) {
		hwnd, err := finder()
		if err == nil {
			lastFoundHwnd = hwnd
			return true
		}
		time.Sleep(delay)
		delay *= 2
		if delay > 500*time.Millisecond {
			delay = 500 * time.Millisecond
		}
	}
	return false
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

		// 4. AnswerCandidates — скролл, подсказки (EN+RU).
		layout.Flexed(0.20, func(gtx layout.Context) layout.Dimensions {
			if !hasAnswers {
				return layout.Dimensions{}
			}
			return layoutAnswers(gtx, th, answers, fs, &o.answersList)
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

// answerLine — одна строка в скролле подсказок: EN или RU.
type answerLine struct {
	text string
	isRU bool
}

// layoutAnswers — подсказки с EN и RU на отдельных строках, с вертикальным скроллом.
func layoutAnswers(gtx layout.Context, th *material.Theme, msg UIMessage, fs int, list *layout.List) layout.Dimensions {
	afs := fs - 2
	if afs < 10 {
		afs = 10
	}

	// Собираем плоский список: EN (белый), RU (зелёный).
	items := make([]answerLine, 0, len(msg.Answers)*2)
	for _, ans := range msg.Answers {
		en, ru := splitBilingual(ans)
		items = append(items, answerLine{text: en, isRU: false})
		if ru != "" {
			items = append(items, answerLine{text: ru, isRU: true})
		}
	}

	return list.Layout(gtx, len(items), func(gtx layout.Context, idx int) layout.Dimensions {
		line := items[idx]
		l := material.Label(th, unit.Sp(afs), line.text)
		if line.isRU {
			l.Color = color.NRGBA{R: 144, G: 238, B: 144, A: 255}
		} else {
			l.Color = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
		}
		l.Alignment = text.Start
		return l.Layout(gtx)
	})
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
