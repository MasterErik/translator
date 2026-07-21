package main

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mastererik/translator/internal/capture"
	"github.com/mastererik/translator/internal/common"
	"github.com/mastererik/translator/internal/logger"
	"github.com/mastererik/translator/internal/stt"
	"github.com/mastererik/translator/internal/translator"
)

// --------------------------------------------------------------------------
// Mock STT Provider — emits controlled STTEvent values for testing
// --------------------------------------------------------------------------

// mockSTTProvider implements stt.STTProvider with in-memory channels.
// It emits a preconfigured sequence of STTEvent values and supports
// graceful shutdown via context cancellation.
type mockSTTProvider struct {
	audioCh chan []byte
	textCh  chan common.STTEvent
	events  []common.STTEvent
	mu      sync.Mutex
}

func newMockSTTProvider(events []common.STTEvent) *mockSTTProvider {
	return &mockSTTProvider{
		audioCh: make(chan []byte, 64),
		textCh:  make(chan common.STTEvent, 32),
		events:  events,
	}
}

func (m *mockSTTProvider) Start(ctx context.Context) error {
	go m.emitEvents(ctx)
	return nil
}

func (m *mockSTTProvider) emitEvents(ctx context.Context) {
	for _, event := range m.events {
		select {
		case m.textCh <- event:
		case <-ctx.Done():
			return
		}
	}
	// Keep the channel open but stop emitting; the reader
	// will see the context cancellation.
	<-ctx.Done()
}

func (m *mockSTTProvider) Stop() error {
	return nil
}

func (m *mockSTTProvider) AudioStream() chan<- []byte {
	return m.audioCh
}

func (m *mockSTTProvider) TextStream() <-chan common.STTEvent {
	return m.textCh
}

// Compile-time interface check.
var _ stt.STTProvider = (*mockSTTProvider)(nil)

// --------------------------------------------------------------------------
// Mock LLM Provider — returns canned translations and answers
// --------------------------------------------------------------------------

type mockLLMProvider struct {
	translations map[string]string
	answers      map[string][]string
}

func newMockLLMProvider() *mockLLMProvider {
	return &mockLLMProvider{
		translations: make(map[string]string),
		answers:      make(map[string][]string),
	}
}

func (m *mockLLMProvider) Translate(ctx context.Context, text string, history []string) (string, error) {
	if t, ok := m.translations[text]; ok {
		return t, nil
	}
	return "translated: " + text, nil
}

func (m *mockLLMProvider) GenerateAnswers(ctx context.Context, question string, cvContext string) ([]string, error) {
	if a, ok := m.answers[question]; ok {
		return a, nil
	}
	return []string{"answer 1", "answer 2"}, nil
}

// Compile-time interface check.
var _ translator.LLMProvider = (*mockLLMProvider)(nil)

// --------------------------------------------------------------------------
// Tests
// --------------------------------------------------------------------------

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

// TestOverlayStub verifies the stub overlay's AddMessage and GetMessages
// behave correctly under concurrent access.
func TestOverlayStub(t *testing.T) {
	overlay := NewOverlay(OverlayConfig{
		Title:        "Test Overlay",
		Width:        800,
		Height:       200,
		FontSize:     16,
		TopZoneRatio: 0.6,
	})

	// Add a few messages.
	overlay.AddMessage(OverlayMsg{
		Type:      MsgTranslation,
		Text:      "Hello, world!",
		Timestamp: time.Now(),
	})
	overlay.AddMessage(OverlayMsg{
		Type:      MsgAnswerCandidates,
		Text:      "What is Go?",
		Answers:   []string{"A compiled language", "Garbage collected"},
		Timestamp: time.Now(),
	})

	msgs := overlay.GetMessages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Type != MsgTranslation {
		t.Errorf("first message should be Translation, got %s", msgs[0].Type)
	}
	if msgs[1].Type != MsgAnswerCandidates {
		t.Errorf("second message should be AnswerCandidates, got %s", msgs[1].Type)
	}
	if len(msgs[1].Answers) != 2 {
		t.Errorf("expected 2 answers, got %d", len(msgs[1].Answers))
	}

	// Test concurrent access.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			overlay.AddMessage(OverlayMsg{
				Type:      MsgTranslation,
				Text:      "concurrent",
				Timestamp: time.Now(),
			})
			_ = overlay.GetMessages()
		}()
	}
	wg.Wait()

	// Verify no data races or panics (checked by -race flag).
	if len(overlay.GetMessages()) != 12 {
		t.Errorf("expected 12 messages, got %d", len(overlay.GetMessages()))
	}
}

// TestGracefulShutdown exercises the full pipeline with stub components,
// verifies that a log file is created, and that all goroutines exit cleanly
// on context cancellation.
func TestGracefulShutdown(t *testing.T) {
	// Create a temporary log directory.
	tmpDir, err := os.MkdirTemp("", "translator-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// ---- 1. Set up stub components ----

	// Mock STT emits one interim and one final event, then waits for ctx.
	mockSTT := newMockSTTProvider([]common.STTEvent{
		{
			Text:      "hello world",
			IsFinal:   false,
			ChannelID: "speaker",
			Timestamp: time.Now(),
		},
		{
			Text:      "Hello, how are you?",
			IsFinal:   true,
			ChannelID: "speaker",
			Timestamp: time.Now(),
		},
	})

	// Mock LLM returns canned translations.
	mockLLM := newMockLLMProvider()
	mockLLM.translations["Hello, how are you?"] = "Привет, как дела?"

	// Translation engine with window size 3.
	engine := translator.NewEngine(mockLLM, 3)

	// Session logger writing to temp dir.
	sessLog, err := logger.NewFileSessionLogger(tmpDir)
	if err != nil {
		t.Fatalf("failed to create session logger: %v", err)
	}

	// Stub overlay.
	overlay := NewOverlay(OverlayConfig{
		Title:        "Test Overlay",
		Width:        800,
		Height:       200,
		FontSize:     16,
		TopZoneRatio: 0.6,
	})

	// Stub capture — emits silent 20ms frames at 16kHz mono (320 samples × 2 bytes).
	silentFrame := make([]byte, 640) // 320 samples * 2 bytes/sample = 640 bytes
	stubCap := capture.NewStubCapture(
		capture.CaptureConfig{BufferSizeMs: 20},
		silentFrame,
		silentFrame,
		20*time.Millisecond,
	)

	// ---- 2. Start STT provider ----
	ctx, cancel := context.WithCancel(context.Background())

	if err := mockSTT.Start(ctx); err != nil {
		t.Fatalf("failed to start mock STT: %v", err)
	}

	// ---- 3. Launch pipeline goroutines ----
	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		runCapture(ctx, stubCap, mockSTT, sessLog)
	}()

	go func() {
		defer wg.Done()
		runSTT(ctx, mockSTT, engine, overlay, sessLog)
	}()

	go func() {
		defer wg.Done()
		runUI(ctx, overlay)
	}()

	// ---- 4. Wait for events to propagate, then signal shutdown ----
	time.Sleep(1500 * time.Millisecond)
	cancel()

	// ---- 5. Graceful shutdown in order ----
	_ = mockSTT.Stop()
	overlay.WaitShutdown()

	if err := sessLog.Close(); err != nil {
		t.Errorf("failed to close session logger: %v", err)
	}

	// Wait for pipeline goroutines to exit.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Log("all goroutines exited cleanly")
	case <-time.After(5 * time.Second):
		t.Fatal("goroutines did not exit within timeout — possible leak")
	}

	// ---- 6. Verify log file was created ----
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("failed to read log dir: %v", err)
	}

	var foundLog, foundAudioDir bool
	for _, e := range entries {
		if e.IsDir() && e.Name() == "audio" {
			foundAudioDir = true
		}
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			foundLog = true
			t.Logf("session log file: %s", e.Name())
		}
	}

	if !foundLog {
		t.Error("session log file was not created")
	}
	if !foundAudioDir {
		t.Error("audio directory was not created")
	}

	// ---- 7. Verify overlay received messages ----
	msgs := overlay.GetMessages()
	t.Logf("overlay received %d messages", len(msgs))
	if len(msgs) == 0 {
		t.Error("overlay should have received at least one message")
	}

	// Check that the translation message was received.
	var foundTranslation bool
	for _, msg := range msgs {
		if msg.Type == MsgTranslation && msg.Text == "Привет, как дела?" {
			foundTranslation = true
			break
		}
	}
	if !foundTranslation {
		t.Error("overlay did not receive expected translation message")
	}

	// Check that answer candidates were received (question detected).
	var foundAnswers bool
	for _, msg := range msgs {
		if msg.Type == MsgAnswerCandidates {
			foundAnswers = true
			t.Logf("answer candidates: %v", msg.Answers)
			break
		}
	}
	if !foundAnswers {
		t.Error("overlay did not receive answer candidates for question")
	}
}

// TestGracefulShutdownNoEvents verifies clean shutdown even when no
// STT events arrive before cancellation.
func TestGracefulShutdownNoEvents(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "translator-test-noevents-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Mock STT that never emits events.
	mockSTT := newMockSTTProvider(nil)
	mockLLM := newMockLLMProvider()
	engine := translator.NewEngine(mockLLM, 5)
	sessLog, err := logger.NewFileSessionLogger(tmpDir)
	if err != nil {
		t.Fatalf("failed to create session logger: %v", err)
	}
	overlay := NewOverlay(OverlayConfig{})

	silentFrame := make([]byte, 640)
	stubCap := capture.NewStubCapture(
		capture.CaptureConfig{BufferSizeMs: 20},
		silentFrame,
		silentFrame,
		20*time.Millisecond,
	)

	ctx, cancel := context.WithCancel(context.Background())

	if err := mockSTT.Start(ctx); err != nil {
		t.Fatalf("failed to start mock STT: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); runCapture(ctx, stubCap, mockSTT, sessLog) }()
	go func() { defer wg.Done(); runSTT(ctx, mockSTT, engine, overlay, sessLog) }()
	go func() { defer wg.Done(); runUI(ctx, overlay) }()

	// Cancel almost immediately.
	time.Sleep(100 * time.Millisecond)
	cancel()

	_ = mockSTT.Stop()
	overlay.WaitShutdown()
	_ = sessLog.Close()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
		t.Log("all goroutines exited cleanly with no events")
	case <-time.After(5 * time.Second):
		t.Fatal("goroutines did not exit within timeout")
	}
}
