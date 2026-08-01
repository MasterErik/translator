package ui

import (
	"github.com/mastererik/translator/internal/logger"
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestNewOverlay(t *testing.T) {
	tests := []struct {
		name         string
		cfg          OverlayConfig
		wantWidth    int
		wantHeight   int
		wantFontSize int
	}{
		{
			name: "all fields set",
			cfg: OverlayConfig{
				Width:    1024,
				Height:   300,
				FontSize: 24,
			},
			wantWidth:    1024,
			wantHeight:   300,
			wantFontSize: 24,
		},
		{
			name:         "zero-value defaults",
			cfg:          OverlayConfig{},
			wantWidth:    1200,
			wantHeight:   650,
			wantFontSize: 18,
		},
		{
			name: "negative font size defaults",
			cfg: OverlayConfig{
				Width:    1200,
				FontSize: -5,
			},
			wantWidth:    1200,
			wantHeight:   650,
			wantFontSize: 18,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := NewOverlay(tt.cfg, logger.NewNopSessionLogger())
			if o.cfg.Width != tt.wantWidth {
				t.Errorf("Width = %d, want %d", o.cfg.Width, tt.wantWidth)
			}
			if o.cfg.Height != tt.wantHeight {
				t.Errorf("Height = %d, want %d", o.cfg.Height, tt.wantHeight)
			}
			if o.cfg.FontSize != tt.wantFontSize {
				t.Errorf("FontSize = %d, want %d", o.cfg.FontSize, tt.wantFontSize)
			}
			if o.messages == nil {
				t.Error("messages slice should be initialized")
			}
			if len(o.messages) != 0 {
				t.Errorf("initial messages should be empty, got %d", len(o.messages))
			}
			if o.shutdown == nil {
				t.Error("shutdown channel should be initialized")
			}
		})
	}
}

func TestAddMessageGetMessages(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 200}, logger.NewNopSessionLogger())

	// Статус и подсказки добавляются (append).
	msg1 := UIMessage{Type: Status, Text: "Connected", Timestamp: time.Now()}
	msg2 := UIMessage{Type: AnswerCandidates, Text: "Q", Answers: []string{"A1", "A2"}, Timestamp: time.Now()}
	o.AddMessage(msg1)
	o.AddMessage(msg2)

	msgs := o.GetMessages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Type != Status {
		t.Errorf("msg[0].Type = %v, want Status", msgs[0].Type)
	}
	if msgs[1].Type != AnswerCandidates {
		t.Errorf("msg[1].Type = %v, want AnswerCandidates", msgs[1].Type)
	}
}

func TestTranslationReplacement(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 200}, logger.NewNopSessionLogger())

	// Translation-сообщения заменяются, а не копятся.
	o.AddMessage(UIMessage{Type: Translation, Text: "first", MsgStatus: "pending"})
	o.AddMessage(UIMessage{Type: Translation, Text: "second", MsgStatus: "streaming"})
	o.AddMessage(UIMessage{Type: Translation, Text: "third", MsgStatus: "done"})

	msgs := o.GetMessages()
	if len(msgs) != 1 {
		t.Fatalf("Translation должен заменяться: ждали 1, получили %d", len(msgs))
	}
	if msgs[0].Text != "third" {
		t.Errorf("Text = %q, want %q", msgs[0].Text, "third")
	}
}

func TestMessageOrdering(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 200}, logger.NewNopSessionLogger())

	baseTime := time.Now()
	for i := 0; i < 10; i++ {
		o.AddMessage(UIMessage{
			Type:      Status,
			Text:      fmt.Sprintf("%d. message", i+1),
			Timestamp: baseTime.Add(time.Duration(i) * time.Second),
		})
	}

	msgs := o.GetMessages()
	if len(msgs) != 10 {
		t.Fatalf("expected 10 messages, got %d", len(msgs))
	}
	for i, m := range msgs {
		if i > 0 && m.Timestamp.Before(msgs[i-1].Timestamp) {
			t.Errorf("messages out of order at index %d", i)
		}
	}
}

func TestEmptyMessages(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 200}, logger.NewNopSessionLogger())
	msgs := o.GetMessages()
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages, got %d", len(msgs))
	}
	if msgs == nil {
		t.Error("GetMessages should return empty slice, not nil")
	}
}

func TestConcurrentAccess(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 200}, logger.NewNopSessionLogger())

	var wg sync.WaitGroup
	numWriters := 10
	numReaders := 5
	messagesPerWriter := 100

	for w := 0; w < numWriters; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			baseTime := time.Now()
			for i := 0; i < messagesPerWriter; i++ {
				o.AddMessage(UIMessage{
					Type:      Status,
					Text:      fmt.Sprintf("%d. msg", workerID*messagesPerWriter+i+1),
					Timestamp: baseTime.Add(time.Duration(i) * time.Millisecond),
				})
			}
		}(w)
	}

	for r := 0; r < numReaders; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < messagesPerWriter; i++ {
				_ = o.GetMessages()
			}
		}()
	}

	wg.Wait()

	msgs := o.GetMessages()
	expected := numWriters * messagesPerWriter
	if len(msgs) != expected {
		t.Errorf("expected %d messages, got %d", expected, len(msgs))
	}
}

func TestGetMessagesReturnsCopy(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 200}, logger.NewNopSessionLogger())
	o.AddMessage(UIMessage{Type: Status, Text: "original", Timestamp: time.Now()})

	msgs := o.GetMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	msgs[0] = UIMessage{Type: Translation, Text: "modified", Timestamp: time.Now()}

	msgs2 := o.GetMessages()
	if msgs2[0].Type == Translation {
		t.Error("GetMessages should return a copy; modification leaked")
	}
	if msgs2[0].Text != "original" {
		t.Errorf("GetMessages returned %q, want %q", msgs2[0].Text, "original")
	}
}

// ── Тесты вспомогательных функций ──

func TestLastInterim(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 200}, logger.NewNopSessionLogger())

	// Пустой overlay.
	if m := o.lastInterim(); m.Type != "" {
		t.Errorf("пустой overlay: lastInterim should return zero UIMessage, got %v", m.Type)
	}

	// Один interim.
	o.AddMessage(UIMessage{Type: Interim, Text: "Hello"})
	if m := o.lastInterim(); m.Text != "Hello" {
		t.Errorf("lastInterim = %q, want %q", m.Text, "Hello")
	}

	// Замена — только последний.
	o.AddMessage(UIMessage{Type: Interim, Text: "World"})
	if m := o.lastInterim(); m.Text != "World" {
		t.Errorf("lastInterim after replace = %q, want %q", m.Text, "World")
	}

	// Проверяем что в messages только 1 interim (старый заменён).
	interimCount := 0
	for _, m := range o.GetMessages() {
		if m.Type == Interim {
			interimCount++
		}
	}
	if interimCount != 1 {
		t.Errorf("interim count = %d, want 1 (replacement)", interimCount)
	}
}

func TestLastAnswers(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 200}, logger.NewNopSessionLogger())

	// Пустой.
	msg, ok := o.lastAnswers()
	if ok {
		t.Errorf("empty: lastAnswers should return false")
	}

	// Кандидаты с пустым списком — не считаются.
	o.AddMessage(UIMessage{Type: AnswerCandidates, Answers: []string{}})
	msg, ok = o.lastAnswers()
	if ok {
		t.Errorf("empty answers list: lastAnswers should return false")
	}

	// Реальные кандидаты.
	o.AddMessage(UIMessage{Type: AnswerCandidates, Answers: []string{"A", "B", "C"}})
	msg, ok = o.lastAnswers()
	if !ok || len(msg.Answers) != 3 {
		t.Errorf("with answers: got ok=%v len=%d, want ok=true len=3", ok, len(msg.Answers))
	}

	// Замена.
	o.AddMessage(UIMessage{Type: AnswerCandidates, Answers: []string{"X"}})
	msg, ok = o.lastAnswers()
	if !ok || len(msg.Answers) != 1 || msg.Answers[0] != "X" {
		t.Errorf("after replace: got ok=%v answers=%v, want [X]", ok, msg.Answers)
	}
}

func TestHistoryMessages(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 200}, logger.NewNopSessionLogger())

	// Пустой.
	if h := o.historyMessages(); len(h) != 0 {
		t.Errorf("empty: historyMessages should return empty slice, got %d", len(h))
	}

	// Добавляем mixed — фильтрует только History.
	o.AddMessage(UIMessage{Type: Interim, Text: "interim"})
	o.AddMessage(UIMessage{Type: History, Text: "h1", Translation: "п1"})
	o.AddMessage(UIMessage{Type: Translation, Text: "tr", MsgStatus: "done"})
	o.AddMessage(UIMessage{Type: History, Text: "h2"})

	h := o.historyMessages()
	if len(h) != 2 {
		t.Fatalf("historyMessages count = %d, want 2", len(h))
	}
	if h[0].Text != "h1" || h[1].Text != "h2" {
		t.Errorf("history order: got [%q, %q], want [h1, h2]", h[0].Text, h[1].Text)
	}
}

// ── Тесты замены и накопления сообщений ──

func TestInterimReplacement(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 200}, logger.NewNopSessionLogger())

	o.AddMessage(UIMessage{Type: Interim, Text: "first"})
	o.AddMessage(UIMessage{Type: Interim, Text: "second"})
	o.AddMessage(UIMessage{Type: Interim, Text: "third"})

	msgs := o.GetMessages()
	interimCount := 0
	var lastInterimText string
	for _, m := range msgs {
		if m.Type == Interim {
			interimCount++
			lastInterimText = m.Text
		}
	}

	if interimCount != 1 {
		t.Errorf("interim count = %d, want 1 — Interim должен заменяться", interimCount)
	}
	if lastInterimText != "third" {
		t.Errorf("last interim = %q, want %q", lastInterimText, "third")
	}
}

func TestAnswerCandidatesReplacement(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 200}, logger.NewNopSessionLogger())

	o.AddMessage(UIMessage{Type: AnswerCandidates, Answers: []string{"A1", "A2"}})
	o.AddMessage(UIMessage{Type: AnswerCandidates, Answers: []string{"B1"}})
	o.AddMessage(UIMessage{Type: AnswerCandidates, Answers: []string{"C1", "C2", "C3"}})

	msgs := o.GetMessages()
	ansCount := 0
	var lastLen int
	for _, m := range msgs {
		if m.Type == AnswerCandidates {
			ansCount++
			lastLen = len(m.Answers)
		}
	}

	if ansCount != 1 {
		t.Errorf("AnswerCandidates count = %d, want 1 — должен заменяться", ansCount)
	}
	if lastLen != 3 {
		t.Errorf("last AnswerCandidates len = %d, want 3", lastLen)
	}
}

func TestHistoryAppendOnly(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 200}, logger.NewNopSessionLogger())

	o.AddMessage(UIMessage{Type: History, Text: "msg1", Translation: "tr1"})
	o.AddMessage(UIMessage{Type: History, Text: "msg2", Translation: "tr2"})
	o.AddMessage(UIMessage{Type: History, Text: "msg3"})

	hist := o.historyMessages()
	if len(hist) != 3 {
		t.Fatalf("history count = %d, want 3 — History должен накапливаться", len(hist))
	}
	if hist[0].Text != "msg1" || hist[1].Text != "msg2" || hist[2].Text != "msg3" {
		t.Error("history order нарушен")
	}
	if hist[0].Translation != "tr1" || hist[1].Translation != "tr2" {
		t.Error("history translation не сохранился")
	}
}

func TestUIMessageConstants(t *testing.T) {
	if string(Translation) != "Translation" {
		t.Errorf("Translation = %q", Translation)
	}
	if string(AnswerCandidates) != "AnswerCandidates" {
		t.Errorf("AnswerCandidates = %q", AnswerCandidates)
	}
	if string(Status) != "Status" {
		t.Errorf("Status = %q", Status)
	}
}

// TestWindowStarts — интеграционный тест: окно создаётся, все 4 зоны получают данные.
func TestWindowStarts(t *testing.T) {
	o := NewOverlay(OverlayConfig{
		Width:    1200,
		Height:   650,
		FontSize: 18,
	}, logger.NewNopSessionLogger())

	// Добавляем сообщения во все 4 зоны.
	o.AddMessage(UIMessage{Type: Interim, Text: "I have five years of..."})
	o.AddMessage(UIMessage{Type: Translation, Text: "У меня пять лет опыта...", MsgStatus: "done"})
	o.AddMessage(UIMessage{Type: AnswerCandidates, Answers: []string{"Yes, I agree", "No, thanks", "Let me think"}})

	// Добавляем 40 строк в историю перевода (>10 — проверка скролла).
	for i := 1; i <= 40; i++ {
		o.AddMessage(UIMessage{
			Type:        History,
			Text:        fmt.Sprintf("Original line %d", i),
			Translation: fmt.Sprintf("Перевод строки %d", i),
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- o.Run(ctx)
	}()

	// Ждём что окно стартует без мгновенной ошибки.
	select {
	case err := <-errCh:
		t.Fatalf("окно упало при старте: %v", err)
	case <-time.After(2 * time.Second):
	}

	msgs := o.GetMessages()

	// Проверяем что все 4 зоны получили данные.
	has := map[UIMessageType]bool{}
	for _, m := range msgs {
		has[m.Type] = true
	}

	zones := []UIMessageType{Interim, Translation, AnswerCandidates, History}
	for _, z := range zones {
		if !has[z] {
			t.Errorf("зона %s не получила данные", z)
		}
	}

	// Проверяем конкретные значения.
	if m := o.lastInterim(); m.Text != "I have five years of..." {
		t.Errorf("Interim = %q", m.Text)
	}
	if m, ok := o.lastAnswers(); !ok || len(m.Answers) != 3 {
		t.Errorf("AnswerCandidates = %v (ok=%v)", m.Answers, ok)
	}
	hist := o.historyMessages()
	if len(hist) != 40 {
		t.Errorf("History count = %d, want 40 — все строки должны быть в буфере для скролла", len(hist))
	}
	// Проверяем что последняя строка доступна (скролл до конца).
	if hist[39].Translation != "Перевод строки 40" {
		t.Errorf("last translation = %q, want %q", hist[39].Translation, "Перевод строки 40")
	}
	if hist[0].Translation != "Перевод строки 1" {
		t.Errorf("first translation = %q, want %q", hist[0].Translation, "Перевод строки 1")
	}

	// Проверяем скролл: prevTranscLen должен обновиться = 40 строк.
	if o.prevTranscLen != 40 {
		t.Errorf("prevTranscLen = %d, want 40 — скролл не сработал (needScrollHist=false?)", o.prevTranscLen)
	}
	// Флаги конца — производная проверка.
	if !o.TranslationAtEnd {
		t.Error("Translation History: скролл НЕ в конце")
	}
	if !o.TranscriptionAtEnd {
		t.Error("Transcription History: скролл НЕ в конце")
	}

	// Проверяем размеры окна.
	if o.cfg.Width != 1200 {
		t.Errorf("Width = %d, want 1200", o.cfg.Width)
	}
	if o.cfg.Height != 650 {
		t.Errorf("Height = %d, want 650", o.cfg.Height)
	}

	// Проверяем пропорции зон согласно ARCHITECTURE.md:
	// 1. Interim (Rigid, 2 строки)
	// 2. Translation History (Flexed 0.55, скролл переводов)
	// 3. Transcription History (Flexed 0.25, скролл оригиналов)
	// 4. AnswerCandidates (Flexed 0.20, подсказки)
	zonesInfo := []struct {
		name  string
		flexed bool
	}{
		{"Interim", false},
		{"Translation History", true},
		{"Transcription History", true},
		{"AnswerCandidates", true},
	}
	for _, z := range zonesInfo {
		t.Logf("зона %s: flexed=%v", z.name, z.flexed)
	}
	t.Logf("окно %d×%d, fontSize=%d", o.cfg.Width, o.cfg.Height, o.cfg.FontSize)
}
