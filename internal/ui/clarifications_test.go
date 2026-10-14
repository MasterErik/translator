package ui

import (
	"image"
	"image/color"
	"testing"

	"gioui.org/widget/material"

	"github.com/mastererik/translator/internal/logger"
)

// ── Б-T4: строки уточнений в зоне 3 под шапкой активной метки ──

// TestAnswersMetaRecognizesClarifications — Answer с единственным ненулевым
// полем Clarifications распознаётся как контрактная (иначе уточнения молча не
// рисовались бы), а полностью нулевая Answer — нет.
func TestAnswersMetaRecognizesClarifications(t *testing.T) {
	meta, ok := answersMeta([]Answer{{Clarifications: []string{"а это про бюджет?"}}})
	if !ok {
		t.Fatalf("answersMeta({Clarifications:[1]}) = ok=false, want true")
	}
	if len(meta.Clarifications) != 1 || meta.Clarifications[0] != "а это про бюджет?" {
		t.Fatalf("answersMeta вернул Clarifications=%v, want [\"а это про бюджет?\"]", meta.Clarifications)
	}
	if _, ok := answersMeta([]Answer{{}}); ok {
		t.Fatalf("answersMeta({}) = ok=true, want false (нулевые поля = старый формат)")
	}
}

// TestRecentClarifications — последние 3 уточнения в ХРОНОЛОГИЧЕСКОМ порядке
// (старое → новое): 0 → nil, 1 → то же, 3 → все 3, 5 → ровно последние 3.
func TestRecentClarifications(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"zero", nil, nil},
		{"zero_empty_slice", []string{}, nil},
		{"one", []string{"c1"}, []string{"c1"}},
		{"three", []string{"c1", "c2", "c3"}, []string{"c1", "c2", "c3"}},
		{"five_keeps_last_three", []string{"c1", "c2", "c3", "c4", "c5"}, []string{"c3", "c4", "c5"}},
		{"four_keeps_last_three", []string{"c1", "c2", "c3", "c4"}, []string{"c2", "c3", "c4"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := recentClarifications(Answer{Clarifications: tt.in})
			if len(got) != len(tt.want) {
				t.Fatalf("recentClarifications(%v) = %v, want %v", tt.in, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("recentClarifications(%v) = %v, want %v (порядок хронологический)", tt.in, got, tt.want)
				}
			}
		})
	}
}

// TestRecentClarificationsNoAlias — результат не алиасит исходный стек: правка
// среза-источника не меняет результат (нужна копия, как в dispatcher).
func TestRecentClarificationsNoAlias(t *testing.T) {
	src := []string{"c1", "c2", "c3", "c4", "c5"}
	got := recentClarifications(Answer{Clarifications: src})
	src[3] = "ИЗМЕНЕНО"
	if got[1] != "c4" {
		t.Fatalf("результат алиасит исходный стек: got=%v", got)
	}
}

// TestLayoutClarificationsEmptyTakesNoSpace — пустой список: нулевые размеры,
// ни одного пикселя высоты (прежний рендер байт-в-байт).
func TestLayoutClarificationsEmptyTakesNoSpace(t *testing.T) {
	const fs = 18
	th := material.NewTheme()
	gtx, _ := newLabelContext(800)
	dims := layoutClarifications(gtx, th, nil, fs)
	if dims.Size != (image.Point{}) {
		t.Fatalf("layoutClarifications(nil) = %v, want 0x0", dims.Size)
	}
}

// TestLayoutClarificationsRowsHeight — блок уточнений занимает ровно N строк
// шрифта smallFontSize(fs) для N непустых уточнений (каждая на своей строке).
func TestLayoutClarificationsRowsHeight(t *testing.T) {
	const fs = 18
	th := material.NewTheme()
	oneLine := oneLineHeight(th, smallFontSize(fs))
	for _, n := range []int{1, 2, 3} {
		in := make([]string, n)
		for i := range in {
			in[i] = "уточнение"
		}
		gtx, _ := newLabelContext(800)
		dims := layoutClarifications(gtx, th, in, fs)
		want := n * oneLine
		if dims.Size.Y != want {
			t.Errorf("layoutClarifications(%d строк) высота = %d, want %d (по строке на уточнение)", n, dims.Size.Y, want)
		}
	}
}

// TestClarificationsFontNotSmallerThan10 — шрифт строк уточнений — smallFontSize
// (fs-2 с полом 10) и НИКОГДА не меньше 10, включая малые fs.
func TestClarificationsFontNotSmallerThan10(t *testing.T) {
	for fs := 8; fs <= 24; fs++ {
		got := smallFontSize(fs)
		if got < 10 {
			t.Errorf("smallFontSize(%d) = %d, want >= 10 (пол для строк уточнений)", fs, got)
		}
		want := fs - 2
		if want < 10 {
			want = 10
		}
		if got != want {
			t.Errorf("smallFontSize(%d) = %d, want %d (fs-2 с полом 10)", fs, got, want)
		}
	}
	// Малый fs=10: floor удерживает 10, а не 8.
	gtx, _ := newLabelContext(800)
	th := material.NewTheme()
	dims := layoutClarifications(gtx, th, []string{"c"}, 10)
	if dims.Size.Y <= 0 {
		t.Errorf("layoutClarifications(fs=10) высота = %d, want > 0", dims.Size.Y)
	}
}

// TestClarificationsRenderedUnderHeaderZone3 — полный рендер зоны 3 с 5
// уточнениями: рендерятся ровно последние 3 (recentClarifications), шапка не
// раздувается, окно сохраняет размер, зона 3 занимает ненулевую высоту.
func TestClarificationsRenderedUnderHeaderZone3(t *testing.T) {
	const fs = 18
	th := material.NewTheme()

	clar := []string{"c1", "c2", "c3", "c4", "c5"}
	msg := UIMessage{
		Type: AnswerCandidates,
		Answers: []Answer{{
			QuestionIdx: 2, Total: 5,
			QuestionText:   "Why did you leave your last job?",
			State:          "ready",
			Mode:           "answer",
			Clarifications: clar,
		}},
	}
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: fs}, logger.NewNopSessionLogger())
	o.AddMessage(msg)

	// Ровно последние 3 уточнения уходят в рендер (и в промпт) — UI честный.
	meta, ok := answersMeta(msg.Answers)
	if !ok {
		t.Fatal("answersMeta вернул ok=false для контрактной Answer с уточнениями")
	}
	shown := recentClarifications(meta)
	if len(shown) != 3 {
		t.Fatalf("к рендеру %d уточнений, want ровно 3 (последние)", len(shown))
	}
	if shown[0] != "c3" || shown[1] != "c4" || shown[2] != "c5" {
		t.Fatalf("показаны %v, want [c3 c4 c5] (хронологически)", shown)
	}

	// Зона 3 в кадре: высота не меньше трёх строк уточнений.
	gtx, _ := newTestContext(800, 650)
	dims := o.layoutAnswersZone(gtx, th, msg, true, UIMessage{}, fs, false)
	wantMin := 3 * oneLineHeight(th, smallFontSize(fs))
	if dims.Size.Y < wantMin {
		t.Errorf("зона 3 высота = %d, want >= %d (три строки уточнений)", dims.Size.Y, wantMin)
	}

	// Полный кадр не паникует и не раздувает окно.
	gtx2, _ := newTestContext(800, 650)
	o.render(gtx2, th)
	if gtx2.Constraints.Max.X != 800 || gtx2.Constraints.Max.Y != 650 {
		t.Fatalf("render занимает %v, want 800x650", gtx2.Constraints.Max)
	}
}

// TestClarificationsZeroKeepsOldRender — 0 уточнений: блок пустой (нулевые
// размеры), зона 3 рендерится как раньше (обратная совместимость).
func TestClarificationsZeroKeepsOldRender(t *testing.T) {
	const fs = 18
	th := material.NewTheme()
	gtx, _ := newLabelContext(800)
	if dims := layoutClarifications(gtx, th, recentClarifications(Answer{}), fs); dims.Size.Y != 0 {
		t.Fatalf("0 уточнений: высота блока = %d, want 0 (ничего не рисуем)", dims.Size.Y)
	}

	// Контрактная Answer без уточнений в зоне 3 рендерится без паники.
	msg := UIMessage{Type: AnswerCandidates, Answers: []Answer{{
		QuestionIdx: 0, Total: 1, QuestionText: "q", State: "idle", Mode: "answer",
	}}}
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: fs}, logger.NewNopSessionLogger())
	gtxZone, _ := newTestContext(800, 650)
	if d := o.layoutAnswersZone(gtxZone, th, msg, true, UIMessage{}, fs, false); d.Size.Y < oneLineHeight(th, smallFontSize(fs)) {
		t.Errorf("зона 3 без уточнений высота = %d, want >= строки подписей", d.Size.Y)
	}
}

// TestClarificationMarkAndColor — префикс строки уточнения — «↳ » (маркер из
// плана), а цвет отличается от цвета строк ответа (белый) и от шапки вопроса.
func TestClarificationMarkAndColor(t *testing.T) {
	if clarificationMark != "↳ " {
		t.Fatalf("clarificationMark = %q, want %q", clarificationMark, "↳ ")
	}
	white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}  // строки ответа Source
	header := color.NRGBA{R: 220, G: 220, B: 230, A: 255} // шапка вопроса
	if clarificationColor == white {
		t.Fatal("цвет уточнений совпал с цветом строк ответа (белый) — неразличимы")
	}
	if clarificationColor == header {
		t.Fatal("цвет уточнений совпал с цветом шапки вопроса — неразличимы")
	}
}
