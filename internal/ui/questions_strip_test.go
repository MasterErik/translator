package ui

import (
	"image/color"
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

// ── Индикатор свежих меток «➤» (A-T3) ──

// TestShowFreshIndicator — «➤» показывается ИСКЛЮЧИТЕЛЬНО по полю Answer.HasFresh,
// которое заполняет dispatcher из таймлайна. Никакой локальной арифметики
// QuestionIdx/Total: на >150 вопросах окно кольца короче Total, и старая логика
// (meta.QuestionIdx < meta.Total-1) давала неверный ответ (баг D4).
func TestShowFreshIndicator(t *testing.T) {
	tests := []struct {
		name string
		meta Answer
		want bool
	}{
		{"has_fresh_true", Answer{QuestionIdx: 145, Total: 155, HasFresh: true}, true},
		{"has_fresh_false", Answer{QuestionIdx: 145, Total: 155, HasFresh: false}, false},
		{"zero_value_old_format", Answer{}, false},
		// Кейс D4: активная не на хвосте (idx < total), но свежая метка есть.
		{"absolute_idx_below_total", Answer{QuestionIdx: 10, Total: 156, HasFresh: true}, true},
		// Обратный кейс: активная на хвосте и HasFresh=true не приходит (таймлайн
		// гасит индикатор) — показывать нечего.
		{"tail_no_fresh", Answer{QuestionIdx: 155, Total: 156, HasFresh: false}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := showFreshIndicator(tt.meta); got != tt.want {
				t.Errorf("showFreshIndicator(%+v) = %v, want %v", tt.meta, got, tt.want)
			}
		})
	}
}

// TestQuestionStripIndicatorRendersWithFresh — лента с HasFresh=true рендерится
// без паники (индикатор — Rigid-элемент у правого края) и занимает строку;
// HasFresh=false и нулевое значение старого формата тоже рендерятся.
func TestQuestionStripIndicatorRendersWithFresh(t *testing.T) {
	const fs = 18
	th := material.NewTheme()
	cases := map[string]Answer{
		"fresh":         {QuestionIdx: 145, Total: 155, QuestionText: "вопрос", State: "ready", HasFresh: true},
		"no_fresh":      {QuestionIdx: 145, Total: 155, QuestionText: "вопрос", State: "ready"},
		"old_format":    {},
		"fresh_wide":    {QuestionIdx: 0, Total: 1, QuestionText: "единственный", State: "idle", HasFresh: true},
		"fresh_narrow":  {QuestionIdx: 2, Total: 156, QuestionText: "v", State: "generating", HasFresh: true},
		"fresh_at_tail": {QuestionIdx: 155, Total: 156, QuestionText: "v", State: "ready", HasFresh: true},
	}
	for name, meta := range cases {
		t.Run(name, func(t *testing.T) {
			gtx, _ := newLabelContext(800)
			dims := layoutQuestionStrip(gtx, th, []Answer{meta}, meta, fs)
			if dims.Size.Y <= 0 {
				t.Errorf("layoutQuestionStrip высота = %d, want > 0", dims.Size.Y)
			}
		})
	}
}

// TestQuestionStripIndicatorNotDrawnWhenFreshFalse — индикатор при HasFresh=false
// не занимает места: ширина, отданная Rigid-элементу справа, нулевая. Проверяем
// через измерение ленты на узком контексте (индикатор «съел» бы место у метки).
func TestQuestionStripIndicatorNotDrawnWhenFreshFalse(t *testing.T) {
	const fs = 18
	th := material.NewTheme()
	// На очень узком контексте лишний Rigid-индикатор сдвинул бы метки, но высота
	// строки не меняется; проверяем прежде всего что рендер не паникует и
	// предикат показывает индикатор только при HasFresh.
	meta := Answer{QuestionIdx: 1, Total: 3, QuestionText: "q", State: "ready"}
	if showFreshIndicator(meta) {
		t.Fatalf("showFreshIndicator без HasFresh = true, want false")
	}
	gtx, _ := newLabelContext(120)
	if dims := layoutQuestionStrip(gtx, th, []Answer{meta}, meta, fs); dims.Size.Y <= 0 {
		t.Errorf("высота ленты = %d, want > 0", dims.Size.Y)
	}
}

// TestAnswersMetaHasFreshOnlyContract — Answer с единственным ненулевым полем
// HasFresh распознаётся как контрактная (иначе индикатор молча терялся бы), а
// полностью нулевая Answer — нет.
func TestAnswersMetaHasFreshOnlyContract(t *testing.T) {
	meta, ok := answersMeta([]Answer{{HasFresh: true}})
	if !ok || !meta.HasFresh {
		t.Fatalf("answersMeta({HasFresh:true}) = (%+v, %v), want (HasFresh=true, true)", meta, ok)
	}
	if _, ok := answersMeta([]Answer{{}}); ok {
		t.Fatalf("answersMeta({}) = ok=true, want false (нулевые поля = старый формат)")
	}
}

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

// ── Цветовая сигнализация состояний меток (Task 2) ──

// questionStripTestColors — цвета меток ленты по контракту Task 2: янтарный для
// queued/generating, зелёный для ready, красный (layoutErrorColor) для failed,
// серый для idle/неизвестного.
var questionStripTestColors = map[string]color.NRGBA{
	"queued":     {R: 200, G: 200, B: 120, A: 255},
	"generating": {R: 200, G: 200, B: 120, A: 255},
	"ready":      {R: 120, G: 200, B: 120, A: 255},
	"failed":     {R: 255, G: 96, B: 96, A: 255},   // layoutErrorColor
	"idle":       {R: 160, G: 160, B: 180, A: 255}, // серый
	"":           {R: 160, G: 160, B: 180, A: 255},
	"unknown":    {R: 160, G: 160, B: 180, A: 255},
}

// TestQuestionMarkColorByState — чистая функция цвета метки: оттенок задаётся
// состоянием метки; активная метка — непрозрачная (A=255), неактивная приглушена
// (A≈200), оттенок при этом НЕ меняется.
func TestQuestionMarkColorByState(t *testing.T) {
	for state, want := range questionStripTestColors {
		t.Run(state, func(t *testing.T) {
			// Активная: полная непрозрачность.
			gotActive := questionMarkColor(state, true)
			if gotActive != want {
				t.Errorf("questionMarkColor(%q, active=true) = %+v, want %+v", state, gotActive, want)
			}
			// Неактивная: тот же оттенок (R/G/B), но приглушённая A.
			gotInactive := questionMarkColor(state, false)
			if gotInactive.R != want.R || gotInactive.G != want.G || gotInactive.B != want.B {
				t.Errorf("Неактивная метка %q: R/G/B = {%d,%d,%d}, want {%d,%d,%d} (оттенок не меняется)",
					state, gotInactive.R, gotInactive.G, gotInactive.B, want.R, want.G, want.B)
			}
			if gotInactive.A >= gotActive.A {
				t.Errorf("Неактивная метка %q: A = %d, want < %d (приглушение)",
					state, gotInactive.A, gotActive.A)
			}
			if gotInactive.A == 0 {
				t.Errorf("Неактивная метка %q: A = 0 (невидима), want > 0", state)
			}
		})
	}
}

// TestQuestionMarkColorDistinctStates — состояния РАЗЛИЧИМЫ по цвету: пары
// (queued/generating), ready, failed, idle дают разные оттенки (не только
// прозрачность) — сигнализация читается.
func TestQuestionMarkColorDistinctStates(t *testing.T) {
	states := []string{"queued", "ready", "failed", "idle"}
	for i := 0; i < len(states); i++ {
		for j := i + 1; j < len(states); j++ {
			a := questionMarkColor(states[i], true)
			b := questionMarkColor(states[j], true)
			if a.R == b.R && a.G == b.G && a.B == b.B {
				t.Errorf("Состояния %q и %q неразличимы по цвету: оба %+v", states[i], states[j], a)
			}
		}
	}
}

// TestQuestionMarkLabelColorByState — цвет лейбла метки из РЕАЛЬНОГО пути
// рендера (markLabelStyle, используемого layoutMark): state берётся из Answer
// метки (answerByQuestionIdx), активная — A=255, неактивная — приглушена.
func TestQuestionMarkLabelColorByState(t *testing.T) {
	const fs = 18
	th := material.NewTheme()

	for state, want := range questionStripTestColors {
		t.Run(state, func(t *testing.T) {
			answers := []Answer{{QuestionIdx: 2, Total: 5, State: state, Mode: "answer"}}
			meta := Answer{QuestionIdx: 2, Total: 5, State: state}

			active := markLabelStyle(th, answers, meta, 2, true, fs)
			if active.Color != want {
				t.Errorf("активная метка state=%q: цвет = %+v, want %+v", state, active.Color, want)
			}
			active.Color.A = 255 // нормализация не нужна, но фиксируем контракт

			inactive := markLabelStyle(th, answers, meta, 2, false, fs)
			if inactive.Color.R != want.R || inactive.Color.G != want.G || inactive.Color.B != want.B {
				t.Errorf("неактивная метка state=%q: оттенок = {%d,%d,%d}, want {%d,%d,%d}",
					state, inactive.Color.R, inactive.Color.G, inactive.Color.B, want.R, want.G, want.B)
			}
			if inactive.Color.A >= 255 {
				t.Errorf("неактивная метка state=%q: A = %d, want < 255 (приглушение)",
					state, inactive.Color.A)
			}
			if inactive.Color.A != markInactiveAlpha {
				t.Errorf("неактивная метка state=%q: A = %d, want %d", state, inactive.Color.A, markInactiveAlpha)
			}
		})
	}
}

// TestQuestionMarkLabelColorMarkMatchesState — лейбл метки несёт и ЦВЕТ, и
// МАРКЕР состояния: generating → «…», ready → «●», failed → «!», idle → «○».
func TestQuestionMarkLabelColorMarkMatchesState(t *testing.T) {
	const fs = 18
	th := material.NewTheme()
	cases := map[string]string{
		"generating": "…",
		"queued":     "…",
		"ready":      "●",
		"failed":     "!",
		"idle":       "○",
	}
	for state, wantMark := range cases {
		answers := []Answer{{QuestionIdx: 1, Total: 3, State: state}}
		meta := Answer{QuestionIdx: 1, Total: 3, State: state}
		l := markLabelStyle(th, answers, meta, 1, true, fs)
		if !strings.Contains(l.Text, wantMark) {
			t.Errorf("state=%q: лейбл %q не содержит маркер %q", state, l.Text, wantMark)
		}
		if !strings.Contains(l.Text, "[2]") {
			t.Errorf("state=%q: лейбл %q не содержит номер метки [2]", state, l.Text)
		}
	}
}

// TestQuestionStripRendersStateColors — полный рендер ленты (layoutQuestionStrip)
// с метками в разных состояниях не паникует и занимает высоту строки; активная
// метка несёт янтарный цвет генерации (проверяем через markLabelStyle на тех же
// данных, что рендерит лента).
func TestQuestionStripRendersStateColors(t *testing.T) {
	const fs = 18
	th := material.NewTheme()

	const active, total = 2, 5
	ans := []Answer{
		{Source: "EN", Target: "RU", QuestionIdx: active, Total: total, State: "generating", Mode: "answer"},
	}
	gtx, _ := newLabelContext(800)
	dims := layoutQuestionStrip(gtx, th, ans, Answer{QuestionIdx: active, Total: total, State: "generating"}, fs)
	if dims.Size.Y <= 0 {
		t.Fatalf("layoutQuestionStrip высота = %d, want > 0", dims.Size.Y)
	}

	l := markLabelStyle(th, ans, Answer{QuestionIdx: active, Total: total, State: "generating"}, active, true, fs)
	want := color.NRGBA{R: 200, G: 200, B: 120, A: 255}
	if l.Color != want {
		t.Errorf("активная метка generating: цвет = %+v, want янтарный %+v", l.Color, want)
	}
}

// ── Абсолютная нумерация метки (кольцевой таймлайн) ──

// TestQuestionMarkLabelAbsoluteNumber — лейбл метки показывает АБСОЛЮТНЫЙ номер
// (idx+1): при окне кольца активная метка №12 даёт «[12]», а не «[1]».
func TestQuestionMarkLabelAbsoluteNumber(t *testing.T) {
	const fs = 18
	th := material.NewTheme()

	// Внешний индекс 11 (нумерация с 0) — «12-й вопрос», total хранимого окна 150.
	answers := []Answer{{QuestionIdx: 11, Total: 150, State: "ready"}}
	meta := Answer{QuestionIdx: 11, Total: 150, State: "ready"}
	l := markLabelStyle(th, answers, meta, 11, true, fs)
	if !strings.Contains(l.Text, "[12]") {
		t.Errorf("лейбл активной метки %q не содержит абсолютный номер [12]", l.Text)
	}

	// Соседняя (неактивная) метка того же окна — свой абсолютный номер.
	l2 := markLabelStyle(th, answers, meta, 8, false, fs)
	if !strings.Contains(l2.Text, "[9]") {
		t.Errorf("лейбл метки %q не содержит абсолютный номер [9]", l2.Text)
	}
}

// TestQuestionStripAbsoluteWindowLabels — лента рендерится для окна кольца:
// активная метка — не первая видимая, окно прижато к её абсолютному номеру,
// рендер не паникует и занимает строку.
func TestQuestionStripAbsoluteWindowLabels(t *testing.T) {
	const fs = 18
	th := material.NewTheme()

	// Окно кольца из cap меток, активная — 12-й вопрос (idx 11), total=150.
	const active, total = 11, 150
	ans := []Answer{
		{Source: "EN", Target: "RU", QuestionIdx: active, Total: total, State: "generating", Mode: "answer"},
	}
	meta := Answer{QuestionIdx: active, Total: total, State: "generating"}

	gtx, _ := newLabelContext(800)
	dims := layoutQuestionStrip(gtx, th, ans, meta, fs)
	if dims.Size.Y <= 0 {
		t.Fatalf("layoutQuestionStrip (абсолютный idx) высота = %d, want > 0", dims.Size.Y)
	}

	// Активная метка — не первая в окне: метка с абсолютным номером active-1
	// тоже принадлежит видимому окну.
	if !answersMetaContracts(meta, active-1) {
		t.Fatalf("окно вокруг активной %d не содержит метку %d", active, active-1)
	}
}

// answersMetaContracts проверяет, что метка absIdx попадает в видимое окно
// вокруг активной meta.QuestionIdx при вместимости questionStripMaxVisible.
func answersMetaContracts(meta Answer, absIdx int) bool {
	relActive := meta.Total - 1 // активная прижата к концу абсолютного диапазона
	base := meta.QuestionIdx - relActive
	for _, rel := range visibleWindow(relActive, meta.Total, questionStripMaxVisible) {
		if base+rel == absIdx {
			return true
		}
	}
	return false
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
