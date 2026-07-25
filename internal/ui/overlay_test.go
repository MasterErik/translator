package ui

import (
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
		wantMaxLines int
	}{
		{
			name: "all fields set",
			cfg: OverlayConfig{
				Width:    1024,
				Height:   300,
				FontSize: 24,
				MaxLines: 10,
			},
			wantWidth:    1024,
			wantHeight:   300,
			wantFontSize: 24,
			wantMaxLines: 10,
		},
		{
			name:         "zero-value defaults",
			cfg:          OverlayConfig{},
			wantWidth:    800,
			wantHeight:   400,
			wantFontSize: 18,
			wantMaxLines: 10,
		},
		{
			name: "negative font size defaults",
			cfg: OverlayConfig{
				Width:    800,
				FontSize: -5,
			},
			wantWidth:    800,
			wantHeight:   400,
			wantFontSize: 18,
			wantMaxLines: 10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := NewOverlay(tt.cfg)
			if o.cfg.Width != tt.wantWidth {
				t.Errorf("Width = %d, want %d", o.cfg.Width, tt.wantWidth)
			}
			if o.cfg.Height != tt.wantHeight {
				t.Errorf("Height = %d, want %d", o.cfg.Height, tt.wantHeight)
			}
			if o.cfg.FontSize != tt.wantFontSize {
				t.Errorf("FontSize = %d, want %d", o.cfg.FontSize, tt.wantFontSize)
			}
			if o.cfg.MaxLines != tt.wantMaxLines {
				t.Errorf("MaxLines = %d, want %d", o.cfg.MaxLines, tt.wantMaxLines)
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
	o := NewOverlay(OverlayConfig{Width: 800, Height: 200})

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
	o := NewOverlay(OverlayConfig{Width: 800, Height: 200})

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
	o := NewOverlay(OverlayConfig{Width: 800, Height: 200})

	baseTime := time.Now()
	for i := 0; i < 10; i++ {
		o.AddMessage(UIMessage{
			Type:      Status,
			Text:      formatAnswer(i+1, "message"),
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
	o := NewOverlay(OverlayConfig{Width: 800, Height: 200})
	msgs := o.GetMessages()
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages, got %d", len(msgs))
	}
	if msgs == nil {
		t.Error("GetMessages should return empty slice, not nil")
	}
}

func TestConcurrentAccess(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 200})

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
					Text:      formatAnswer(workerID*messagesPerWriter+i+1, "msg"),
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
	o := NewOverlay(OverlayConfig{Width: 800, Height: 200})
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
