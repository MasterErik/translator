package ui

import (
	"fmt"
	"testing"

	"gioui.org/widget/material"

	"github.com/mastererik/translator/internal/logger"
)

// TestAutoScrollZone2AlwaysToEnd — RED→GREEN (Task 3.1): автоскролл «всегда в
// конец» для зоны 2. После ручного сдвига Position в середину следующий рендер
// снова ставит список у конца (в старом коде с prevTransLen/needScroll сдвиг
// сохранялся, пока не приходило новое сообщение).
func TestAutoScrollZone2AlwaysToEnd(t *testing.T) {
	const fs = 18
	th := material.NewTheme()
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: fs}, logger.NewNopSessionLogger())

	// Достаточно переводов, чтобы контент заведомо превышал высоту зоны 2.
	for i := 1; i <= 20; i++ {
		o.AddMessage(UIMessage{Type: Translation, Text: fmt.Sprintf("перевод номер %d", i), MsgStatus: "done"})
	}

	gtx, _ := newTestContext(800, 650)
	m := o.render(gtx, th)
	if !m.TranslationsAtEnd {
		t.Fatalf("после 1-го рендера TranslationsAtEnd = false, want true (список не у конца)")
	}

	// Прокрутка не должна быть пустышкой: контент выше видимой высоты.
	if o.translationList.Position.Length <= o.translationList.Position.Offset+1 {
		t.Fatalf("контент не превышает видимую высоту (Length=%d) — тест некорректен",
			o.translationList.Position.Length)
	}

	// Вручную сдвигаем Position в середину (как будто пользователь отскроллил вверх).
	o.translationList.ScrollTo(0)
	gtx2, _ := newTestContext(800, 650)

	// Следующий кадр — снова у конца (автоскролл «всегда в конец», без счётчиков).
	m2 := o.render(gtx2, th)
	if !m2.TranslationsAtEnd {
		t.Errorf("после ручного сдвига TranslationsAtEnd = false, want true — автоскролл не сработал")
	}
	if o.translationList.Position.BeforeEnd {
		t.Errorf("translationList.Position.BeforeEnd = true, want false (список у конца)")
	}
}

// TestAutoScrollZones3And4AlwaysToEnd — тот же контракт для зон 3 (подсказки,
// список answersList) и 4 (история, transcriptionList).
func TestAutoScrollZones3And4AlwaysToEnd(t *testing.T) {
	const fs = 18
	th := material.NewTheme()
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: fs}, logger.NewNopSessionLogger())

	// Зона 3: пары Source+Target, чтобы список заведомо был длинным.
	for i := 1; i <= 20; i++ {
		o.AddMessage(UIMessage{
			Type:    AnswerCandidates,
			Answers: []Answer{{Source: fmt.Sprintf("EN line %d", i), Target: fmt.Sprintf("RU строка %d", i)}},
		})
	}
	// Зона 4: включена (F4) и заполнена.
	o.ToggleTranscriptionHistory()
	for i := 1; i <= 20; i++ {
		o.AddMessage(UIMessage{Type: History, Text: fmt.Sprintf("history line %d", i)})
	}

	gtx, _ := newTestContext(800, 650)
	m := o.render(gtx, th)
	if !m.AnswersAtEnd {
		t.Errorf("после 1-го рендера AnswersAtEnd = false, want true")
	}
	if !m.TranscriptionAtEnd {
		t.Errorf("после 1-го рендера TranscriptionAtEnd = false, want true")
	}

	// Ручной сдвиг обоих списков в начало → следующий кадр снова у конца.
	o.answersList.ScrollTo(0)
	o.transcriptionList.ScrollTo(0)
	gtx2, _ := newTestContext(800, 650)
	m2 := o.render(gtx2, th)
	if !m2.AnswersAtEnd {
		t.Errorf("после ручного сдвига AnswersAtEnd = false, want true")
	}
	if !m2.TranscriptionAtEnd {
		t.Errorf("после ручного сдвига TranscriptionAtEnd = false, want true")
	}
}
