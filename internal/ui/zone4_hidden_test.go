package ui

import (
	"fmt"
	"testing"

	"gioui.org/widget/material"

	"github.com/mastererik/translator/internal/logger"
)

// TestHistoryDataDoesNotShowZone4 — дефект «при получении данных 4 зона
// появляется сама». Гейт в render(): зона 4 и её separator добавляются только
// при historyVisible (F4). Тест подтверждает: при historyVisible=false и
// ПОЛНОМ наборе данных (History + Interim + Translation done +
// AnswerCandidates) в кадре ровно 2 separator-заливки — зона 4 и её separator
// отсутствуют, данные в буфере истории НЕ влияют на рендер зон 1–3.
//
// Если тест зелёный при наличии History-данных, дефект визуальный: пользователь
// принимает растущую зону 2 (переводы) / зону 3 (ответы) за появляющуюся
// зону 4 — данные самой истории в зоны 1–3 не утекают.
func TestHistoryDataDoesNotShowZone4(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: 18}, logger.NewNopSessionLogger())

	// Полный набор данных, включая несколько History-сообщений.
	for i := 1; i <= 5; i++ {
		o.AddMessage(UIMessage{Type: History, Text: fmt.Sprintf("history line %d", i), Translation: fmt.Sprintf("перевод %d", i)})
	}
	o.AddMessage(UIMessage{Type: Interim, Text: "interim text"})
	o.AddMessage(UIMessage{Type: Translation, Text: "готовый перевод", MsgStatus: "done"})
	o.AddMessage(UIMessage{Type: AnswerCandidates, Answers: answersFrom("EN: yes | RU: да")})

	if o.HistoryVisible() {
		t.Fatal("historyVisible должен быть false по умолчанию")
	}

	gtx, _ := newTestContext(800, 650)
	th := material.NewTheme()

	m := o.render(gtx, th)
	if gtx.Constraints.Max.X != 800 || gtx.Constraints.Max.Y != 650 {
		t.Fatalf("render занимает %v, want 800x650", gtx.Constraints.Max)
	}

	if got := m.SeparatorCount; got != 2 {
		t.Errorf("separator count = %d, want 2 (зона 4 и её separator отсутствуют при historyVisible=false)", got)
	}

	// Контраст: при F4 (historyVisible=true) появляется третий separator.
	o.ToggleTranscriptionHistory()
	gtx2, _ := newTestContext(800, 650)
	m2 := o.render(gtx2, th)
	if got := m2.SeparatorCount; got != 3 {
		t.Errorf("separator count (visible) = %d, want 3 (зона 4 добавлена)", got)
	}
}

// TestHistoryDataDoesNotLeakIntoZones — History-сообщения не должны попадать
// в зоны 1–3 (translationMessages и lastAnswers видят только свои типы).
func TestHistoryDataDoesNotLeakIntoZones(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: 18}, logger.NewNopSessionLogger())

	for i := 1; i <= 5; i++ {
		o.AddMessage(UIMessage{Type: History, Text: fmt.Sprintf("h%d", i), Translation: fmt.Sprintf("t%d", i)})
	}
	o.AddMessage(UIMessage{Type: Translation, Text: "реальный перевод", MsgStatus: "done"})
	o.AddMessage(UIMessage{Type: AnswerCandidates, Answers: answersFrom("EN: a | RU: б")})

	// Зона 2 (переводы) видит только Translation done, не History.
	if tr := o.translationMessages(); len(tr) != 1 || tr[0].Text != "реальный перевод" {
		t.Errorf("translationMessages = %v, want только [реальный перевод]", tr)
	}
	// Зона 4 (история) видит только History.
	if h := o.historyMessages(); len(h) != 5 {
		t.Errorf("historyMessages = %d, want 5", len(h))
	}
	// Зона 3 (ответы) видит только AnswerCandidates.
	answers, ok := o.lastAnswers()
	if !ok || len(answers.Answers) != 1 || answers.Answers[0].Source != "EN: a | RU: б" {
		t.Errorf("lastAnswers = %v ok=%v, want только [EN: a | RU: б]", answers.Answers, ok)
	}
}
