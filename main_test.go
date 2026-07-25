package main

import (
	"sync"
	"testing"
	"time"

	"github.com/mastererik/translator/internal/common"
	"github.com/mastererik/translator/internal/ui"
)

// TestLoadConfig verifies that common.LoadConfig returns a non-nil
// configuration with sensible defaults.
func TestLoadConfig(t *testing.T) {
	cfg := common.LoadConfig()
	if cfg == nil {
		t.Fatal("LoadConfig returned nil")
	}
	if cfg.OpenAIModel == "" {
		t.Error("OpenAIModel should have a default value")
	}
	if cfg.DeepgramModel == "" {
		t.Error("DeepgramModel should have a default value")
	}
	if cfg.TargetLanguage == "" {
		t.Error("TargetLanguage should have a default value")
	}
	if cfg.AudioSampleRate == 0 {
		t.Error("AudioSampleRate should have a default value")
	}
	if cfg.WindowSize == 0 {
		t.Error("WindowSize should have a default value")
	}
	t.Logf("Config: model=%s, lang=%s, window=%d, sampleRate=%d",
		cfg.OpenAIModel, cfg.TargetLanguage, cfg.WindowSize, cfg.AudioSampleRate)
}

// TestOverlayStub verifies that the UI overlay can accept and retrieve
// messages, and handles concurrent access correctly.
func TestOverlayStub(t *testing.T) {
	overlay := ui.NewOverlay(ui.OverlayConfig{
		Width:    800,
		Height:   200,
		FontSize: 16,
	})

	// Add several messages.
	overlay.AddMessage(ui.UIMessage{
		Type:      ui.Translation,
		Text:      "Hello, world!",
		Timestamp: time.Now(),
	})
	overlay.AddMessage(ui.UIMessage{
		Type:      ui.AnswerCandidates,
		Text:      "What is Go?",
		Answers:   []string{"Компилируемый язык", "Со сборщиком мусора"},
		Timestamp: time.Now(),
	})

	msgs := overlay.GetMessages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Type != ui.Translation {
		t.Errorf("first message should be Translation, got %s", msgs[0].Type)
	}
	if msgs[1].Type != ui.AnswerCandidates {
		t.Errorf("second message should be AnswerCandidates, got %s", msgs[1].Type)
	}
	if len(msgs[1].Answers) != 2 {
		t.Errorf("expected 2 answers, got %d", len(msgs[1].Answers))
	}

	// Concurrent access test.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			overlay.AddMessage(ui.UIMessage{
				Type:      ui.Translation,
				Text:      "concurrent",
				Timestamp: time.Now(),
			})
			_ = overlay.GetMessages()
		}()
	}
	wg.Wait()

	if len(overlay.GetMessages()) != 2 {
		t.Errorf("expected 2 messages (Translation is replaced, not duplicated), got %d", len(overlay.GetMessages()))
	}
}
