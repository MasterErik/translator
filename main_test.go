package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mastererik/translator/internal/common"
	"github.com/mastererik/translator/internal/logger"
	"github.com/mastererik/translator/internal/ui"
)

// TestLoadConfig verifies that LoadConfigFromYAML returns a non-nil
// configuration with sensible defaults.
func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := common.LoadConfigFromYAML(path)
	if err != nil {
		t.Fatalf("LoadConfigFromYAML: %v", err)
	}
	if cfg == nil {
		t.Fatal("LoadConfigFromYAML returned nil")
	}
	if cfg.TargetLang == "" {
		t.Error("TargetLang should have a default value")
	}
}

// TestOverlayStub verifies that the UI overlay can accept and retrieve
// messages, and handles concurrent access correctly.
func TestOverlayStub(t *testing.T) {
	overlay := ui.NewOverlay(ui.OverlayConfig{
		Width:    800,
		Height:   200,
		FontSize: 16,
	}, logger.NewNopSessionLogger())

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
