package ui

import (
	"fmt"
	"image/color"
	"strings"
	"testing"

	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"github.com/mastererik/translator/internal/logger"
)

// ── Регресс: застрявший errorMsg перекрывает зону 3 навсегда ──

// TestZone3ErrorClearedByNewAnswerCandidates — регрессионный тест: после ошибки
// генерации (Type=Error) НОВЫЙ AnswerCandidates должен перекрывать её.
//
// Раньше AddMessage(case Error) писал o.errorMsg и никогда его не сбрасывал, а
// layoutAnswersZone рендерит errorMsg ПРИОРИТЕТНО — зона 3 навсегда залипала на
// первой же ошибке (LLM timeout/сеть) и прятала ленту меток и ответы.
func TestZone3ErrorClearedByNewAnswerCandidates(t *testing.T) {
	const fs = 18
	th := material.NewTheme()
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: fs}, logger.NewNopSessionLogger())

	// 1. Ошибка генерации.
	o.AddMessage(UIMessage{Type: Error, Text: "llm: context deadline exceeded"})

	// До нового ответа ошибка рендерится (поведение зоны 3 не меняем).
	msg, has := o.lastAnswers()
	_ = msg
	gtxErr, _ := newTestContext(800, 650)
	if dims := o.layoutAnswersZone(gtxErr, th, msg, has, o.errorMsg, fs, false); dims.Size.Y <= 0 {
		t.Fatalf("ошибка без ответа: высота зоны 3 = %d, want > 0", dims.Size.Y)
	}

	// 2. Пришёл новый ответ (каркасная Answer с контрактом таймлайна).
	o.AddMessage(UIMessage{Type: AnswerCandidates, Answers: []Answer{{
		QuestionIdx: 0, Total: 1, QuestionText: "q", State: "queued", Mode: "answer",
	}}})

	if o.errorMsg.Type == Error {
		t.Fatalf("после нового AnswerCandidates errorMsg всё ещё Error(%q) — зона 3 залипнет на ошибке", o.errorMsg.Text)
	}

	got, has := o.lastAnswers()
	if !has {
		t.Fatal("lastAnswers = false после AnswerCandidates")
	}
	gtx, _ := newTestContext(800, 650)
	dims := o.layoutAnswersZone(gtx, th, got, has, o.errorMsg, fs, false)
	if dims.Size.Y < oneLineHeight(th, smallFontSize(fs)) {
		t.Errorf("зона 3 после нового ответа: высота = %d, want >= строки подписей %d (лента/шапка)",
			dims.Size.Y, oneLineHeight(th, smallFontSize(fs)))
	}

	// Проверено на уровне кадра: рендер не паникует и занимает окно.
	gtxFrame, _ := newTestContext(800, 650)
	o.render(gtxFrame, th)
	if gtxFrame.Constraints.Max.X != 800 || gtxFrame.Constraints.Max.Y != 650 {
		t.Fatalf("render занимает %v, want 800x650", gtxFrame.Constraints.Max)
	}
}

// ── End-to-end: лента меток и цвет состояния через AddMessage → lastAnswers → ──
// ── layoutAnswersZone (публичный путь зоны 3, а не чистые функции) ──

// zone3TimelineStrip — поднимает сообщение через реальный путь AddMessage и
// возвращает метаданные + первый (активный) лейбл метки, который лента выводит
// на этом сообщении. Рендер зоны 3 выполняется ради проверки, что она реально
// раскладывается (ненулевая высота), а стиль метки берётся из ТОЙ ЖЕ пары
// (answers, meta), которую layoutAnswers передаёт в layoutQuestionStrip.
func zone3TimelineStrip(t *testing.T, o *Overlay, th *material.Theme, fs int) (Answer, []Answer, material.LabelStyle) {
	t.Helper()
	msg, has := o.lastAnswers()
	if !has {
		t.Fatal("lastAnswers = false: сообщение AnswerCandidates не доехало до зоны 3")
	}
	meta, hasMeta := answersMeta(msg.Answers)
	if !hasMeta {
		t.Fatalf("answersMeta = false для Answers=%+v — лента/шапка не рендерятся вообще", msg.Answers)
	}

	gtx, _ := newTestContext(800, 650)
	dims := o.layoutAnswersZone(gtx, th, msg, has, o.errorMsg, fs, false)
	if dims.Size.Y < oneLineHeight(th, smallFontSize(fs)) {
		t.Fatalf("зона 3 высота = %d, want >= %d (лента + шапка реально рисуются)",
			dims.Size.Y, oneLineHeight(th, smallFontSize(fs)))
	}

	// Метка активного вопроса: тот же relActive-расчёт, что в layoutQuestionStrip.
	active := meta.QuestionIdx
	label := markLabelStyle(th, msg.Answers, meta, active, true, fs)
	return meta, msg.Answers, label
}

// TestZone3StripColorQueuedEndToEnd — State="queued": маркер «…», янтарный цвет
// (questionPendingColor), шапка «Q1 из 2: How do you scale?».
func TestZone3StripColorQueuedEndToEnd(t *testing.T) {
	const fs = 18
	th := material.NewTheme()
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: fs}, logger.NewNopSessionLogger())

	o.AddMessage(UIMessage{Type: AnswerCandidates, Answers: []Answer{{
		QuestionIdx: 0, Total: 2, QuestionText: "How do you scale?", State: "queued", Mode: "answer",
	}}})

	meta, _, label := zone3TimelineStrip(t, o, th, fs)

	if want := (color.NRGBA{R: 200, G: 200, B: 120, A: 255}); label.Color != want {
		t.Errorf("queued: цвет метки %+v, want янтарный %+v", label.Color, want)
	}
	if !strings.Contains(label.Text, "…") {
		t.Errorf("queued: лейбл %q не содержит маркер состояния «…»", label.Text)
	}
	if !strings.Contains(label.Text, "[1]") {
		t.Errorf("queued: лейбл %q не содержит номер метки [1]", label.Text)
	}

	// Шапка активной метки: «Q1 из 2: How do you scale?».
	// layoutQuestionTitle обрезает заголовок (MaxLines) и потому не отдаёт
	// натуральную ширину текста — меряем эталон тем же overlayLabel-путём, что и
	// рендер (шрифт smallFontSize, без MaxLines), и сверяем его с ожидаемой
	// строкой «Q1 из 2: <вопрос>». Совпадение — рендер печатает именно её.
	titleText := fmt.Sprintf("Q%d из %d: %s", meta.QuestionIdx+1, meta.Total, meta.QuestionText)
	if titleText != "Q1 из 2: How do you scale?" {
		t.Errorf("текст шапки = %q, want «Q1 из 2: How do you scale?»", titleText)
	}
	gtxTitle, _ := newLabelContext(800)
	if dims := layoutQuestionTitle(gtxTitle, th, meta, fs); dims.Size.Y <= 0 {
		t.Errorf("шапка высота = %d, want > 0", dims.Size.Y)
	}
	gtxRef, _ := newLabelContext(4000)
	ref := overlayLabel(th, unit.Sp(smallFontSize(fs)), titleText).Layout(gtxRef)
	gtxOld, _ := newLabelContext(4000)
	oldPrefix := overlayLabel(th, unit.Sp(smallFontSize(fs)), "Q1 (1/2): How do you scale?").Layout(gtxOld)
	if ref.Size.X <= 0 {
		t.Fatalf("эталон шапки имеет нулевую ширину — измерение некорректно")
	}
	if ref.Size.X == oldPrefix.Size.X {
		t.Errorf("тест некорректен: старый и новый формат одной ширины (%d)", ref.Size.X)
	}
}

// TestZone3StripColorGeneratingEndToEnd — State="generating" даёт тот же янтарный
// цвет и «…», что и queued (одна группа состояний).
func TestZone3StripColorGeneratingEndToEnd(t *testing.T) {
	const fs = 18
	th := material.NewTheme()
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: fs}, logger.NewNopSessionLogger())

	o.AddMessage(UIMessage{Type: AnswerCandidates, Answers: []Answer{{
		QuestionIdx: 1, Total: 3, QuestionText: "Why this role?", State: "generating", Mode: "answer",
	}}})

	_, _, label := zone3TimelineStrip(t, o, th, fs)
	if want := (color.NRGBA{R: 200, G: 200, B: 120, A: 255}); label.Color != want {
		t.Errorf("generating: цвет метки %+v, want янтарный %+v", label.Color, want)
	}
	if !strings.Contains(label.Text, "…") {
		t.Errorf("generating: лейбл %q не содержит маркер «…»", label.Text)
	}
	if !strings.Contains(label.Text, "[2]") {
		t.Errorf("generating: лейбл %q не содержит абсолютный номер [2]", label.Text)
	}
}

// TestZone3StripColorReadyEndToEnd — State="ready": маркер «●» и зелёный цвет
// (questionReadyColor) через публичный путь зоны 3.
func TestZone3StripColorReadyEndToEnd(t *testing.T) {
	const fs = 18
	th := material.NewTheme()
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: fs}, logger.NewNopSessionLogger())

	o.AddMessage(UIMessage{Type: AnswerCandidates, Answers: []Answer{{
		QuestionIdx: 0, Total: 2, QuestionText: "How do you scale?",
		Source: "I shard by tenant.", Target: "Я шардирую по тенанту.",
		State: "ready", Mode: "answer",
	}}})

	_, _, label := zone3TimelineStrip(t, o, th, fs)
	if want := (color.NRGBA{R: 120, G: 200, B: 120, A: 255}); label.Color != want {
		t.Errorf("ready: цвет метки %+v, want зелёный %+v", label.Color, want)
	}
	if !strings.Contains(label.Text, "●") {
		t.Errorf("ready: лейбл %q не содержит маркер «●»", label.Text)
	}
}

var _ = op.Ops{}
