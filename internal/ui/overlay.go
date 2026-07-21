package ui

import (
	"context"
	"image/color"
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
)

// Overlay manages a GioUI window that displays translated subtitles
// and answer candidates in a two-zone transparent overlay.
//
// It is safe for concurrent use: AddMessage and GetMessages are
// protected by a read-write mutex.
type Overlay struct {
	cfg      OverlayConfig
	messages []UIMessage
	mu       sync.RWMutex
	shutdown chan struct{}
}

// NewOverlay creates a new Overlay with the given configuration.
// The overlay is not started until Run is called.
func NewOverlay(cfg OverlayConfig) *Overlay {
	if cfg.FontSize <= 0 {
		cfg.FontSize = 18
	}
	if cfg.TopZoneRatio <= 0 || cfg.TopZoneRatio > 1 {
		cfg.TopZoneRatio = 0.6
	}
	if cfg.Title == "" {
		cfg.Title = "Translator Overlay"
	}
	return &Overlay{
		cfg:      cfg,
		messages: make([]UIMessage, 0),
		shutdown: make(chan struct{}),
	}
}

// AddMessage appends a UIMessage to the overlay's message buffer.
// It is safe for concurrent use.
func (o *Overlay) AddMessage(msg UIMessage) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.messages = append(o.messages, msg)
}

// GetMessages returns a copy of all messages currently in the overlay buffer.
// It is safe for concurrent use.
func (o *Overlay) GetMessages() []UIMessage {
	o.mu.RLock()
	defer o.mu.RUnlock()
	result := make([]UIMessage, len(o.messages))
	copy(result, o.messages)
	return result
}

// Run starts the GioUI window and enters the event loop. It blocks until
// the provided context is cancelled or the window is destroyed.
//
// Run configures the window with the title and size from OverlayConfig,
// and calls app.TopMost(true) for always-on-top behavior. On Windows,
// it additionally calls setTopmost after window creation for WS_EX_LAYERED
// and WS_EX_TRANSPARENT extended styles (transparency + click-through).
//
// Run returns nil on normal shutdown; if the context is cancelled it
// returns ctx.Err(). If the window is destroyed externally it returns
// the error from the destroy event.
func (o *Overlay) Run(ctx context.Context) error {
	var w app.Window

	w.Option(
		app.Title(o.cfg.Title),
		app.Size(unit.Dp(o.cfg.Width), unit.Dp(o.cfg.Height)),
		app.TopMost(true),
	)

	th := material.NewTheme()
	var ops op.Ops

	// Apply additional Windows extended styles after a short delay
	// to allow the window HWND to be created.
	go o.applyWindowStyles()

	defer close(o.shutdown)

	// Event loop.
	for {
		select {
		case <-ctx.Done():
			// Request window close and drain remaining events.
			w.Perform(system.ActionClose)
			// Drain one more event to process the close.
			w.Event()
			return ctx.Err()
		default:
		}

		evt := w.Event()
		switch e := evt.(type) {
		case app.DestroyEvent:
			return e.Err
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)

			// Render the two-zone overlay.
			o.render(gtx, th)

			e.Frame(gtx.Ops)
		}
	}
}

// WaitShutdown blocks until Run() has finished and the shutdown channel
// is closed. Safe to call from any goroutine.
func (o *Overlay) WaitShutdown() {
	<-o.shutdown
}

// applyWindowStyles finds the window HWND by title and applies Windows
// extended styles for transparency and click-through. It retries with
// backoff since the HWND may not be immediately available.
func (o *Overlay) applyWindowStyles() {
	// Wait briefly for the window to be created.
	time.Sleep(200 * time.Millisecond)

	for i := 0; i < 20; i++ {
		hwnd, err := findWindowHWND(o.cfg.Title)
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		if err := setTopmost(hwnd); err != nil {
			// Non-fatal: the overlay still works without extended styles.
			return
		}
		return
	}
}

// render draws the two-zone overlay layout into the provided graphics context.
// The top zone (TopZoneRatio% height) displays the latest Translation message
// against a semi-transparent dark background. The bottom zone displays
// numbered answer candidates with a light-green tint.
func (o *Overlay) render(gtx layout.Context, th *material.Theme) layout.Dimensions {
	msgs := o.GetMessages()

	// Background: semi-transparent dark.
	bgColor := color.NRGBA{R: 0, G: 0, B: 0, A: 180}

	// Fill the entire window with the background color.
	paintBackground(gtx, bgColor)

	// Render the two zones stacked vertically.
	layout.Flex{
		Axis: layout.Vertical,
	}.Layout(gtx,
		layout.Flexed(float32(o.cfg.TopZoneRatio), func(gtx layout.Context) layout.Dimensions {
			return layoutTopZoneContent(gtx, th, msgs, o.cfg.FontSize)
		}),
		layout.Flexed(float32(1.0-o.cfg.TopZoneRatio), func(gtx layout.Context) layout.Dimensions {
			return layoutBottomZoneContent(gtx, th, msgs, o.cfg.FontSize)
		}),
	)

	return layout.Dimensions{
		Size: gtx.Constraints.Max,
	}
}

// paintBackground fills the entire rendering area with the given color.
func paintBackground(gtx layout.Context, c color.NRGBA) {
	defer clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, c)
}

// layoutTopZoneContent renders the latest translation text centered
// with large white text.
func layoutTopZoneContent(gtx layout.Context, th *material.Theme, msgs []UIMessage, fontSize int) layout.Dimensions {
	// Find the latest translation message.
	var latestText string
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Type == Translation {
			latestText = msgs[i].Text
			break
		}
	}
	if latestText == "" {
		// Fallback: show the text of the latest AnswerCandidates message.
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].Type == AnswerCandidates && msgs[i].Text != "" {
				latestText = msgs[i].Text
				break
			}
		}
	}

	if latestText == "" {
		return layout.Dimensions{}
	}

	return layout.Flex{
		Axis:      layout.Vertical,
		Alignment: layout.Middle,
	}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			label := material.Label(th, unit.Sp(fontSize), latestText)
			label.Color = color.NRGBA{R: 255, G: 255, B: 255, A: 255} // white
			label.Alignment = text.Middle
			return label.Layout(gtx)
		}),
	)
}

// layoutBottomZoneContent renders the numbered answer candidates with
// a light-green tint.
func layoutBottomZoneContent(gtx layout.Context, th *material.Theme, msgs []UIMessage, fontSize int) layout.Dimensions {
	// Find the latest AnswerCandidates message.
	var answers []string
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Type == AnswerCandidates && len(msgs[i].Answers) > 0 {
			answers = msgs[i].Answers
			break
		}
	}

	if len(answers) == 0 {
		return layout.Dimensions{}
	}

	// Use a slightly smaller font for answers.
	answerFontSize := fontSize - 2
	if answerFontSize < 10 {
		answerFontSize = 10
	}

	// Build flex children for each answer.
	children := make([]layout.FlexChild, 0, len(answers))
	for i, ans := range answers {
		idx := i
		ansText := ans
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			textContent := formatAnswer(idx+1, ansText)
			label := material.Label(th, unit.Sp(answerFontSize), textContent)
			// Light green tint for answer candidates.
			label.Color = color.NRGBA{R: 144, G: 238, B: 144, A: 255}
			label.Alignment = text.Start
			label.MaxLines = 2
			return label.Layout(gtx)
		}))
	}

	if len(children) == 0 {
		return layout.Dimensions{}
	}

	return layout.Flex{
		Axis:      layout.Vertical,
		Alignment: layout.Start,
	}.Layout(gtx, children...)
}

// formatAnswer returns a numbered answer string like "1. text" or "2. text".
func formatAnswer(num int, text string) string {
	digits := [...]string{
		"0", "1", "2", "3", "4", "5", "6", "7", "8", "9",
	}
	if num >= 0 && num < 10 {
		return digits[num] + ". " + text
	}
	return text
}
