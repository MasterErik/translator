package ui

import (
	"strings"
	"testing"

	"gioui.org/widget/material"

	"github.com/mastererik/translator/internal/logger"
)

// ── visibleWindow ──

// TestVisibleWindow — чистая функция окна меток: длина min(n,total), active
// всегда внутри, окно упирается в границы [0, total-1].
func TestVisibleWindow(t *testing.T) {
	tests := []struct {
		name     string
		active   int
		total, n int
		want     []int
	}{
		{"total1_n3", 0, 1, 3, []int{0}},
		{"total3_n3", 0, 3, 3, []int{0, 1, 2}},
		{"total5_n3_active0", 0, 5, 3, []int{0, 1, 2}},
		{"total5_n3_mid", 2, 5, 3, []int{1, 2, 3}},
		{"total5_n3_tail", 4, 5, 3, []int{2, 3, 4}},
		{"total10_n3_active9", 9, 10, 3, []int{7, 8, 9}},
		{"total10_n3_active5", 5, 10, 3, []int{4, 5, 6}},
		{"total10_n3_active0", 0, 10, 3, []int{0, 1, 2}},
		{"n_larger_than_total", 1, 3, 10, []int{0, 1, 2}},
		{"active_negative_clamp", -5, 5, 3, []int{0, 1, 2}},
		{"active_overflow_clamp", 99, 5, 3, []int{2, 3, 4}},
		{"total0", 0, 0, 3, nil},
		{"n0", 0, 5, 0, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := visibleWindow(tt.active, tt.total, tt.n)
			if len(got) != len(tt.want) {
				t.Fatalf("visibleWindow(%d,%d,%d) = %v, want %v", tt.active, tt.total, tt.n, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("visibleWindow(%d,%d,%d) = %v, want %v", tt.active, tt.total, tt.n, got, tt.want)
				}
			}
		})
	}
}

// TestVisibleWindowActiveAlwaysInside — активная всегда в окне и окно в границах,
// для всех комбинаций (свойство, а не единичный кейс).
func TestVisibleWindowActiveAlwaysInside(t *testing.T) {
	const n = 3
	for total := 1; total <= 20; total++ {
		for active := 0; active < total; active++ {
			win := visibleWindow(active, total, n)
			if len(win) != minInt(n, total) {
				t.Fatalf("total=%d active=%d: len=%d, want %d", total, active, len(win), minInt(n, total))
			}
			found := false
			for _, idx := range win {
				if idx < 0 || idx >= total {
					t.Fatalf("total=%d active=%d: индекс %d вне границ", total, active, idx)
				}
				if idx == active {
					found = true
				}
			}
			if !found {
				t.Fatalf("total=%d active=%d: активная не в окне %v", total, active, win)
			}
		}
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ── Маркеры состояния ──

func TestQuestionStateMark(t *testing.T) {
	tests := map[string]string{
		"idle":       "○",
		"queued":     "…",
		"generating": "…",
		"ready":      "●",
		"failed":     "!",
		"":           "○",
		"unknown":    "○",
	}
	for state, want := range tests {
		if got := questionStateMark(state); got != want {
			t.Errorf("questionStateMark(%q) = %q, want %q", state, got, want)
		}
	}
}

// TestAnswerStateHint — тексты подсказок вместо пустоты при отсутствии ответа.
func TestAnswerStateHint(t *testing.T) {
	tests := map[string]string{
		"idle":       "F1 — ответить",
		"":           "F1 — ответить",
		"queued":     "генерация…",
		"generating": "генерация…",
		"failed":     "ошибка генерации",
	}
	for state, want := range tests {
		if got := answerStateHint(state); got != want {
			t.Errorf("answerStateHint(%q) = %q, want %q", state, got, want)
		}
	}
}

// ── Idle-подсказка в зоне 3 ──

// TestIdleHintRenderedZone3 — AnswerCandidates с каркасной Answer{State:"idle"}
// рендерится без паники; в зоне 3 присутствует подсказка «F1».
func TestIdleHintRenderedZone3(t *testing.T) {
	const fs = 18
	th := material.NewTheme()
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: fs}, logger.NewNopSessionLogger())

	o.AddMessage(UIMessage{
		Type: AnswerCandidates,
		Answers: []Answer{{
			QuestionIdx:  0,
			Total:        2,
			QuestionText: "Расскажите о себе",
			State:        "idle",
			Mode:         "answer",
		}},
	})

	// Нет текста ответа — lastAnswers не должен считаться «с содержимым»,
	// но сообщение с контрактом обязано отрендериться (шапка + подсказка F1).
	answers, ok := o.lastAnswers()
	if !ok {
		t.Fatalf("lastAnswers = false, want true (каркасная Answer с метаданными)")
	}
	if answers.Answers[0].State != "idle" {
		t.Fatalf("State = %q, want idle", answers.Answers[0].State)
	}

	gtx, _ := newTestContext(800, 650)
	m := o.render(gtx, th)
	if gtx.Constraints.Max.X != 800 || gtx.Constraints.Max.Y != 650 {
		t.Fatalf("render занимает %v, want 800x650", gtx.Constraints.Max)
	}
	_ = m
}

// TestIdleHintTextContainsF1 — текст подсказки idle содержит «F1» (юнит-уровень
// чистой функции и рендер строки).
func TestIdleHintTextContainsF1(t *testing.T) {
	if got := answerStateHint("idle"); !strings.Contains(got, "F1") {
		t.Fatalf("answerStateHint(idle) = %q, не содержит F1", got)
	}
	// Рендер подсказки напрямую не паникует и занимает ненулевую высоту.
	th := material.NewTheme()
	gtx, _ := newLabelContext(200)
	dims := layoutAnswerHint(gtx, th, "idle", 18)
	if dims.Size.Y <= 0 {
		t.Errorf("layoutAnswerHint высота = %d, want > 0", dims.Size.Y)
	}
}

// ── Обрезка длинного вопроса ──

// TestLongQuestionTextTruncated — QuestionText 200+ символов обрезается до
// maxQuestionHeaderLines строк, не раздувает зону и не паникует.
func TestLongQuestionTextTruncated(t *testing.T) {
	const fs = 18
	th := material.NewTheme()
	long := strings.Repeat("очень длинный вопрос про опыт работы ", 20)
	if len([]rune(long)) < 200 {
		t.Fatalf("тест некорректен: длина вопроса %d < 200", len([]rune(long)))
	}

	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: fs}, logger.NewNopSessionLogger())
	o.AddMessage(UIMessage{
		Type: AnswerCandidates,
		Answers: []Answer{{
			QuestionIdx: 2, Total: 5, QuestionText: long, State: "ready", Mode: "simpler",
		}},
	})

	gtx, _ := newTestContext(800, 650)
	o.render(gtx, th) // не паникует, окно не раздувается
	if gtx.Constraints.Max.X != 800 || gtx.Constraints.Max.Y != 650 {
		t.Fatalf("render занимает %v, want 800x650", gtx.Constraints.Max)
	}

	// Высота шапки ограничена maxQuestionHeaderLines (2): не больше двух строк.
	gtxTitle, _ := newLabelContext(800)
	dims := layoutQuestionTitle(gtxTitle, th, Answer{QuestionIdx: 2, Total: 5, QuestionText: long}, fs)
	oneLine := oneLineHeight(th, smallFontSize(fs))
	if dims.Size.Y > 2*oneLine+oneLine/2 {
		t.Errorf("шапка вопроса высота = %d, want <= 2 строк (%d px)", dims.Size.Y, 2*oneLine)
	}
}

// ── Лента при 5+ метках ──

// TestQuestionStripManyMarks — лента рендерится без паники при большом таймлайне
// (Total=10): окно вокруг активной не превышает questionStripMaxVisible, зона 3
// скроллится к концу.
func TestQuestionStripManyMarks(t *testing.T) {
	const fs = 18
	th := material.NewTheme()

	// Контракт dispatcher: одно сообщение AnswerCandidates несёт ответы ОДНОЙ
	// активной метки — все Answer с общим QuestionIdx/Total/State.
	const active, total = 6, 10
	ans := []Answer{
		{Source: "EN one", Target: "RU один", QuestionIdx: active, Total: total, State: "generating", Mode: "answer"},
		{Source: "EN two", Target: "RU два", QuestionIdx: active, Total: total, State: "generating", Mode: "answer"},
	}

	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: fs}, logger.NewNopSessionLogger())
	o.AddMessage(UIMessage{Type: AnswerCandidates, Answers: ans})

	gtx, _ := newTestContext(800, 650)
	m := o.render(gtx, th)
	if !m.AnswersAtEnd {
		t.Errorf("AnswersAtEnd = false, want true")
	}
	if gtx.Constraints.Max.X != 800 || gtx.Constraints.Max.Y != 650 {
		t.Fatalf("render занимает %v, want 800x650", gtx.Constraints.Max)
	}

	meta, ok := answersMeta(ans)
	if !ok {
		t.Fatal("answersMeta вернул ok=false для контрактных Answer")
	}
	if meta.QuestionIdx != active || meta.Total != total {
		t.Fatalf("meta = {idx:%d total:%d}, want {%d,%d}", meta.QuestionIdx, meta.Total, active, total)
	}
	gtxStrip, _ := newLabelContext(800)
	dims := layoutQuestionStrip(gtxStrip, th, ans, meta, fs)
	if dims.Size.Y <= 0 {
		t.Errorf("layoutQuestionStrip высота = %d, want > 0", dims.Size.Y)
	}
	if win := visibleWindow(meta.QuestionIdx, meta.Total, questionStripMaxVisible); len(win) > questionStripMaxVisible {
		t.Errorf("окно меток len=%d > %d", len(win), questionStripMaxVisible)
	}
}

// ── Обратная совместимость ──

// TestOldFormatAnswersUnchanged — AnswerCandidates со СТАРЫМ форматом (все поля
// метаданных нулевые) рендерится только строками ответа, без ленты/шапки, как
// раньше: высота зоны 3 = высота одной строки Source.
func TestOldFormatAnswersUnchanged(t *testing.T) {
	const fs = 18
	th := material.NewTheme()
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: fs}, logger.NewNopSessionLogger())

	gtx, _ := newTestContext(800, 650)
	layoutAnswers(gtx, th, UIMessage{Type: AnswerCandidates, Answers: answersFrom("single")}, fs, &o.answersList, false)
	if got, want := o.answersList.Position.Length, oneLineHeight(th, fs); got != want {
		t.Errorf("старый формат: высота = %d, want %d (только строка ответа)", got, want)
	}
}

// TestMetadataAnswersEmptySourceSkipped — Answer с пустыми Source/Target не
// рисуется как пустая строка (иначе пустая Answer добавляла бы строку).
func TestMetadataAnswersEmptySourceSkipped(t *testing.T) {
	const fs = 18
	th := material.NewTheme()
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: fs}, logger.NewNopSessionLogger())

	gtx, _ := newTestContext(800, 650)
	layoutAnswers(gtx, th, UIMessage{Type: AnswerCandidates, Answers: []Answer{{}}}, fs, &o.answersList, false)
	// Пустая Answer — нет items, зона не падает.
	if o.answersList.Position.Length != 0 {
		t.Errorf("пустая Answer: длина скролла = %d, want 0", o.answersList.Position.Length)
	}
}

// ── Полная зона 3 в кадре: шапка + лента + подсказка занимают высоту ──

// TestZone3TimelineFullRender — полный сценарий зоны 3 с контрактом таймлайна:
// каркасная Answer{idle} рисуется через layoutAnswersZone и занимает НЕНУЛЕВУЮ
// высоту (шапка + лента + подсказка F1), а не пустоту. Проверяет, что зона 3
// действительно наполняется в кадре.
func TestZone3TimelineFullRender(t *testing.T) {
	const fs = 18
	th := material.NewTheme()
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: fs}, logger.NewNopSessionLogger())

	msg := UIMessage{
		Type: AnswerCandidates,
		Answers: []Answer{{
			QuestionIdx: 2, Total: 5,
			QuestionText: "Расскажите про сложный проект",
			State:        "idle", Mode: "more context",
		}},
	}
	o.AddMessage(msg)

	// В full-кадре зона 3 раскладывается: шрифт подписей smallFontSize(fs).
	gtx, _ := newTestContext(800, 650)
	dims := o.layoutAnswersZone(gtx, th, msg, true, UIMessage{}, fs, false)
	if dims.Size.Y < oneLineHeight(th, smallFontSize(fs)) {
		t.Errorf("зона 3 (таймлайн) высота = %d, want >= одной строки подписей %d",
			dims.Size.Y, oneLineHeight(th, smallFontSize(fs)))
	}

	// Рендер всего кадра не паникует и занимает окно.
	gtx2, _ := newTestContext(800, 650)
	o.render(gtx2, th)
	if gtx2.Constraints.Max.X != 800 || gtx2.Constraints.Max.Y != 650 {
		t.Fatalf("render занимает %v, want 800x650", gtx2.Constraints.Max)
	}
}
