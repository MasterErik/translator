package ui

import (
	"sync"
	"testing"
	"time"
)

// TestNewOverlay verifies that NewOverlay creates an Overlay with the
// correct configuration values, including defaults for zero-valued fields.
func TestNewOverlay(t *testing.T) {
	tests := []struct {
		name             string
		cfg              OverlayConfig
		wantTitle        string
		wantFontSize     int
		wantTopZoneRatio float64
	}{
		{
			name: "all fields set",
			cfg: OverlayConfig{
				Title:        "Test Overlay",
				Width:        800,
				Height:       200,
				FontSize:     24,
				TopZoneRatio: 0.7,
				RefreshRate:  60,
			},
			wantTitle:        "Test Overlay",
			wantFontSize:     24,
			wantTopZoneRatio: 0.7,
		},
		{
			name:             "zero-value defaults",
			cfg:              OverlayConfig{},
			wantTitle:        "Translator Overlay",
			wantFontSize:     18,
			wantTopZoneRatio: 0.6,
		},
		{
			name: "negative font size defaults",
			cfg: OverlayConfig{
				Title:    "Neg",
				FontSize: -5,
			},
			wantTitle:        "Neg",
			wantFontSize:     18,
			wantTopZoneRatio: 0.6,
		},
		{
			name: "zero top ratio defaults",
			cfg: OverlayConfig{
				Title:        "Ratio",
				FontSize:     20,
				TopZoneRatio: 0,
			},
			wantTitle:        "Ratio",
			wantFontSize:     20,
			wantTopZoneRatio: 0.6,
		},
		{
			name: "top ratio > 1 defaults",
			cfg: OverlayConfig{
				Title:        "Ratio",
				FontSize:     20,
				TopZoneRatio: 1.5,
			},
			wantTitle:        "Ratio",
			wantFontSize:     20,
			wantTopZoneRatio: 0.6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := NewOverlay(tt.cfg)

			if o.cfg.Title != tt.wantTitle {
				t.Errorf("Title = %q, want %q", o.cfg.Title, tt.wantTitle)
			}
			if o.cfg.FontSize != tt.wantFontSize {
				t.Errorf("FontSize = %d, want %d", o.cfg.FontSize, tt.wantFontSize)
			}
			if o.cfg.TopZoneRatio != tt.wantTopZoneRatio {
				t.Errorf("TopZoneRatio = %f, want %f", o.cfg.TopZoneRatio, tt.wantTopZoneRatio)
			}

			// Verify initial state.
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

// TestAddMessageGetMessages verifies that AddMessage and GetMessages
// work correctly and are thread-safe under concurrent access.
func TestAddMessageGetMessages(t *testing.T) {
	o := NewOverlay(OverlayConfig{Title: "Concurrency Test"})

	// Add some messages sequentially first.
	msg1 := UIMessage{Type: Translation, Text: "Hello", Timestamp: time.Now()}
	msg2 := UIMessage{Type: Status, Text: "Connected", Timestamp: time.Now().Add(time.Second)}
	msg3 := UIMessage{Type: AnswerCandidates, Text: "What is Go?", Answers: []string{"A language", "A game", "A verb"}, Timestamp: time.Now().Add(2 * time.Second)}

	o.AddMessage(msg1)
	o.AddMessage(msg2)
	o.AddMessage(msg3)

	msgs := o.GetMessages()
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	if msgs[0].Type != Translation {
		t.Errorf("msg[0].Type = %v, want Translation", msgs[0].Type)
	}
	if msgs[1].Type != Status {
		t.Errorf("msg[1].Type = %v, want Status", msgs[1].Type)
	}
	if msgs[2].Type != AnswerCandidates {
		t.Errorf("msg[2].Type = %v, want AnswerCandidates", msgs[2].Type)
	}
	if len(msgs[2].Answers) != 3 {
		t.Errorf("msg[2].Answers count = %d, want 3", len(msgs[2].Answers))
	}
}

// TestMessageOrdering verifies that messages are returned in the order
// they were added.
func TestMessageOrdering(t *testing.T) {
	o := NewOverlay(OverlayConfig{Title: "Order Test"})

	baseTime := time.Now()
	for i := 0; i < 10; i++ {
		o.AddMessage(UIMessage{
			Type:      Translation,
			Text:      formatAnswer(i+1, "message"), // reuse format helper
			Timestamp: baseTime.Add(time.Duration(i) * time.Second),
		})
	}

	msgs := o.GetMessages()
	if len(msgs) != 10 {
		t.Fatalf("expected 10 messages, got %d", len(msgs))
	}

	for i, m := range msgs {
		if m.Timestamp.Before(baseTime) {
			t.Errorf("message %d timestamp before base time", i)
		}
		if i > 0 && m.Timestamp.Before(msgs[i-1].Timestamp) {
			t.Errorf("messages out of order at index %d", i)
		}
	}
}

// TestEmptyMessages verifies behavior when no messages have been added.
func TestEmptyMessages(t *testing.T) {
	o := NewOverlay(OverlayConfig{Title: "Empty Test"})

	msgs := o.GetMessages()
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages, got %d", len(msgs))
	}
	if msgs == nil {
		t.Error("GetMessages should return empty slice, not nil")
	}
}

// TestConcurrentAccess verifies thread safety of AddMessage and GetMessages
// under concurrent goroutine access.
func TestConcurrentAccess(t *testing.T) {
	o := NewOverlay(OverlayConfig{Title: "Race Test"})

	var wg sync.WaitGroup
	numWriters := 10
	numReaders := 5
	messagesPerWriter := 100

	// Start writers.
	for w := 0; w < numWriters; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			baseTime := time.Now()
			for i := 0; i < messagesPerWriter; i++ {
				o.AddMessage(UIMessage{
					Type:      Translation,
					Text:      formatAnswer(workerID*messagesPerWriter+i+1, "msg"),
					Timestamp: baseTime.Add(time.Duration(i) * time.Millisecond),
				})
			}
		}(w)
	}

	// Start readers concurrently with writers.
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

	// Verify final count.
	msgs := o.GetMessages()
	expected := numWriters * messagesPerWriter
	if len(msgs) != expected {
		t.Errorf("expected %d messages, got %d", expected, len(msgs))
	}
}

// TestGetMessagesReturnsCopy verifies that GetMessages returns a copy,
// not a reference to the internal slice.
func TestGetMessagesReturnsCopy(t *testing.T) {
	o := NewOverlay(OverlayConfig{Title: "Copy Test"})

	o.AddMessage(UIMessage{Type: Translation, Text: "original", Timestamp: time.Now()})

	// Get a copy and modify it.
	msgs := o.GetMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	msgs[0] = UIMessage{Type: Status, Text: "modified", Timestamp: time.Now()}

	// Get again — should still be the original.
	msgs2 := o.GetMessages()
	if len(msgs2) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs2))
	}
	if msgs2[0].Type == Status {
		t.Error("GetMessages should return a copy; modification leaked to internal state")
	}
	if msgs2[0].Text != "original" {
		t.Errorf("GetMessages returned modified text %q, want %q", msgs2[0].Text, "original")
	}
}

// TestUIMessageConstants verifies the UIMessageType constants have the
// expected string values.
func TestUIMessageConstants(t *testing.T) {
	if string(Translation) != "Translation" {
		t.Errorf("Translation = %q, want %q", Translation, "Translation")
	}
	if string(AnswerCandidates) != "AnswerCandidates" {
		t.Errorf("AnswerCandidates = %q, want %q", AnswerCandidates, "AnswerCandidates")
	}
	if string(Status) != "Status" {
		t.Errorf("Status = %q, want %q", Status, "Status")
	}
}
