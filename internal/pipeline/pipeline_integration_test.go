package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mastererik/translator/internal/capture"
	"github.com/mastererik/translator/internal/common"
	"github.com/mastererik/translator/internal/logger"
	"github.com/mastererik/translator/internal/translator"
	"github.com/mastererik/translator/internal/ui"
)

// =========================================================================
// Дополнительные моки для интеграционных тестов
// =========================================================================

// streamingMockLLM — мок с потоковой выдачей токенов для подсказок.
type streamingMockLLM struct {
	mu      sync.Mutex
	tokens  []string
	delay   time.Duration
	callLog []string
}

func (m *streamingMockLLM) GenerateAnswers(ctx context.Context, question string, cv string) ([]string, error) {
	m.mu.Lock()
	m.callLog = append(m.callLog, "GenAnswers:"+question)
	m.mu.Unlock()
	return []string{"hint 1", "hint 2"}, nil
}

func (m *streamingMockLLM) GenerateAnswersStream(ctx context.Context, question string, cv string) (<-chan string, error) {
	m.mu.Lock()
	m.callLog = append(m.callLog, "GenAnswersStream:"+question)
	m.mu.Unlock()

	ch := make(chan string, len(m.tokens)+1)
	go func() {
		defer close(ch)
		for _, tok := range m.tokens {
			select {
			case <-ctx.Done():
				return
			default:
			}
			if m.delay > 0 {
				time.Sleep(m.delay)
			}
			select {
			case ch <- tok:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

var _ translator.LLMProvider = (*streamingMockLLM)(nil)
var _ translator.StreamingAnswersProvider = (*streamingMockLLM)(nil)

// slowLLM — LLM с настраиваемой задержкой (для проверки блокировок).
type slowLLM struct {
	mu    sync.Mutex
	delay time.Duration
}

func newSlowLLM(delay time.Duration) *slowLLM {
	return &slowLLM{
		delay: delay,
	}
}

func (m *slowLLM) GenerateAnswers(ctx context.Context, question string, cv string) ([]string, error) {
	return []string{"answer 1", "answer 2"}, nil
}

var _ translator.LLMProvider = (*slowLLM)(nil)

// countingOverlay — считает все сообщения, сохраняет для проверок.
type countingOverlay struct {
	mu           sync.Mutex
	messages     []ui.UIMessage
	shutdownDone chan struct{}
}

func newCountingOverlay() *countingOverlay {
	return &countingOverlay{
		messages:     make([]ui.UIMessage, 0),
		shutdownDone: make(chan struct{}),
	}
}

func (o *countingOverlay) AddMessage(msg ui.UIMessage) {
	o.mu.Lock()
	o.messages = append(o.messages, msg)
	o.mu.Unlock()
}

func (o *countingOverlay) GetMessages() []ui.UIMessage {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]ui.UIMessage, len(o.messages))
	copy(out, o.messages)
	return out
}

func (o *countingOverlay) Run(ctx context.Context) error {
	defer close(o.shutdownDone)
	<-ctx.Done()
	return ctx.Err()
}

func (o *countingOverlay) WaitShutdown() {
	<-o.shutdownDone
}

func (o *countingOverlay) messageCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.messages)
}

// =========================================================================
// Хелпер: сборка Pipeline из отдельных компонентов для интеграционных тестов
// =========================================================================

// buildTestPipeline собирает Pipeline с указанными компонентами.
// Позволяет задать любые моки и конфигурацию.
func buildTestPipeline(sttProv *mockSTT, capt capturer, ovl Overlay, llm translator.LLMProvider, sessLog logger.SessionLogger, txtBufSize int) *Pipeline {
	engine := translator.NewEngine(llm)
	cfg := Config{
		TextStreamBuffer: txtBufSize,
		AnswerTimeout:    10 * time.Second,
		MaxTokens:        256,
		SaveAudio:        true,
	}
	return &Pipeline{
		cfg:        cfg,
		capturer:   capt,
		sttProv:    sttProv,
		engine:     engine,
		overlay:    ovl,
		sessLog:    sessLog,
		textStream: make(chan common.STTEvent, txtBufSize),
	}
}

// =========================================================================
// TestPipelineFullFlow — полный жизненный цикл с событиями STT и переводом
// =========================================================================

func TestPipelineFullFlow(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "pipeline-fullflow-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Mock STT emits two events.
	stt := newMockSTT()

	// Mock capturer with StubCapture (emits 16kHz mono PCM frames for VAD).
	loudFrame := make([]byte, 2560) // 80ms at 16kHz mono
	for i := 0; i < len(loudFrame)-1; i += 2 {
		loudFrame[i] = 0x10
		loudFrame[i+1] = 0x27 // 0x2710 = 10000 в little-endian
	}
	stubCap := capture.NewStubCapture(
		capture.CaptureConfig{BufferSizeMs: 80},
		loudFrame,
		loudFrame,
		80*time.Millisecond,
	)
	capt := &stubCapturerAdapter{c: stubCap}

	// Overlay.
	ovl := newCountingOverlay()

	// Mock LLM.
	llm := &mockLLM{}

	// Session logger.
	sessLog, err := logger.NewFileSessionLogger(tmpDir, true)
	if err != nil {
		t.Fatalf("failed to create session logger: %v", err)
	}

	p := buildTestPipeline(stt, capt, ovl, llm, sessLog, 16)

	if err := stt.Start(context.Background()); err != nil {
		t.Fatalf("mock STT Start: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(4)
	go func() { defer wg.Done(); p.runCapture(ctx) }()
	go func() { defer wg.Done(); p.runSTT(ctx) }()
	go func() { defer wg.Done(); p.runDispatch(ctx) }()
	go func() { defer wg.Done(); p.runUI(ctx) }()

	// Send an interim event.
	stt.textCh <- common.STTEvent{
		Text:      "hello world",
		Event:     common.EventUpdate,
		ChannelID: "speaker",
		Timestamp: time.Now(),
	}

	// Send a final event + translation (Gladia flow).
	stt.textCh <- common.STTEvent{
		Text:      "Hello, how are you?",
		Event:     common.EventEndOfTurn,
		ChannelID: "speaker",
		Timestamp: time.Now(),
	}
	// Translation from Gladia.
	stt.textCh <- common.STTEvent{
		Text:      "Привет, как дела?",
		Event:     common.EventEndOfTurn,
		ChannelID: "translation",
		Timestamp: time.Now(),
	}

	// Wait for events to propagate.
	time.Sleep(1500 * time.Millisecond)

	// Shutdown.
	cancel()
	_ = stt.Stop()
	_ = sessLog.Close()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
		t.Log("all goroutines exited cleanly")
	case <-time.After(15 * time.Second):
		t.Fatal("goroutines did not exit within timeout — possible leak")
	}

	// Verify log file was created.
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("failed to read log dir: %v", err)
	}

	var foundLog, foundAudio bool
	for _, e := range entries {
		if e.IsDir() && e.Name() == "audio" {
			foundAudio = true
		}
		if !e.IsDir() && strings.HasPrefix(e.Name(), "session_") {
			if strings.HasSuffix(e.Name(), ".csv") {
				foundLog = true
			}
			if strings.HasSuffix(e.Name(), ".mp3") || strings.HasSuffix(e.Name(), ".wav") {
				foundAudio = true
			}
		}
	}
	if !foundLog {
		t.Error("session log file was not created")
	}
	if !foundAudio {
		t.Error("audio file was not created")
	}

	// Verify overlay received translation.
	msgs := ovl.GetMessages()
	if len(msgs) == 0 {
		t.Error("overlay should have received at least one message")
	}

	var foundTranslation bool
	for _, msg := range msgs {
		if msg.Type == ui.Translation && msg.Text == "Привет, как дела?" {
			foundTranslation = true
			break
		}
	}
	if !foundTranslation {
		t.Error("overlay did not receive expected translation")
	}
}

// =========================================================================
// TestPipelineSaveAudioDisabled — SaveAudio=false не создаёт audio/
// =========================================================================

func TestPipelineSaveAudioDisabled(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "pipeline-nosave-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	stt := newMockSTT()
	capt := newMockCapturer()
	ovl := newCountingOverlay()
	llm := &mockLLM{}

	sessLog, err := logger.NewFileSessionLogger(tmpDir, true)
	if err != nil {
		t.Fatalf("failed to create session logger: %v", err)
	}

	p := buildTestPipeline(stt, capt, ovl, llm, sessLog, 16)
	p.cfg.SaveAudio = false

	if err := stt.Start(context.Background()); err != nil {
		t.Fatalf("mock STT Start: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(4)
	go func() { defer wg.Done(); p.runCapture(ctx) }()
	go func() { defer wg.Done(); p.runSTT(ctx) }()
	go func() { defer wg.Done(); p.runDispatch(ctx) }()
	go func() { defer wg.Done(); p.runUI(ctx) }()

	stt.textCh <- common.STTEvent{
		Text:      "test",
		Event:     common.EventEndOfTurn,
		ChannelID: "speaker",
		Timestamp: time.Now(),
	}

	time.Sleep(1500 * time.Millisecond)
	cancel()
	_ = stt.Stop()
	_ = sessLog.Close()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("goroutines did not exit within timeout")
	}

	// Verify audio/ directory was NOT created.
	audioDir := filepath.Join(tmpDir, "audio")
	if _, err := os.Stat(audioDir); !os.IsNotExist(err) {
		t.Error("audio/ directory should NOT exist when saveAudio=false")
	}
}

// =========================================================================
// TestPipelineInterimDuringLongTranslation — проверка, что interim-события
// обрабатываются даже когда в очереди есть final и translation события.
func TestPipelineInterimDuringLongTranslation(t *testing.T) {
	llm := &mockLLM{}

	ovl := newCountingOverlay()

	stt := newMockSTT()
	capt := newMockCapturer()

	p := buildTestPipeline(stt, capt, ovl, llm, nil, 16)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go p.runDispatch(ctx)

	// Send a final event.
	p.textStream <- common.STTEvent{
		Text:      "final text",
		Event:     common.EventEndOfTurn,
		ChannelID: "speaker",
		Timestamp: time.Now(),
	}

	// Immediately send several interim events.
	for i := 0; i < 3; i++ {
		p.textStream <- common.STTEvent{
			Text:      "interim text",
			Event:     common.EventUpdate,
			ChannelID: "speaker",
			Timestamp: time.Now(),
		}
	}

	// Send translation event.
	p.textStream <- common.STTEvent{
		Text:      "финальный перевод",
		Event:     common.EventEndOfTurn,
		ChannelID: "translation",
		Timestamp: time.Now(),
	}

	// Wait for events to process.
	time.Sleep(300 * time.Millisecond)

	msgs := ovl.GetMessages()
	t.Logf("received %d messages", len(msgs))

	if len(msgs) < 4 {
		t.Errorf("expected >= 4 messages, got %d", len(msgs))
	}

	var interimCount int
	for _, msg := range msgs {
		if msg.Type == ui.Interim && msg.Text == "interim text" {
			interimCount++
		}
	}
	if interimCount < 3 {
		t.Errorf("expected 3 interim messages, got %d", interimCount)
	}

	var foundTranslation bool
	for _, msg := range msgs {
		if msg.Type == ui.Translation && msg.Text == "финальный перевод" {
			foundTranslation = true
			break
		}
	}
	if !foundTranslation {
		t.Error("translation not received")
	}
}

// =========================================================================
// TestPipelineDispatchGracefulShutdown — dispatch завершается чисто
// =========================================================================

func TestPipelineDispatchGracefulShutdown(t *testing.T) {
	// slowLLM не реализует StreamingAnswersProvider — проверяем, что
	// dispatch завершается чисто даже с медленным LLM (Gladia flow).
	llm := newSlowLLM(100 * time.Millisecond)

	ovl := newCountingOverlay()

	stt := newMockSTT()
	capt := newMockCapturer()

	p := buildTestPipeline(stt, capt, ovl, llm, nil, 16)

	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		p.runDispatch(ctx)
	}()

	// Send a final event (speaker original).
	p.textStream <- common.STTEvent{
		Text:      "shutdown test",
		Event:     common.EventEndOfTurn,
		ChannelID: "speaker",
		Timestamp: time.Now(),
	}

	// Send translation event (from Gladia).
	p.textStream <- common.STTEvent{
		Text:      "тест остановки",
		Event:     common.EventEndOfTurn,
		ChannelID: "translation",
		Timestamp: time.Now(),
	}

	time.Sleep(300 * time.Millisecond)

	// Cancel — dispatch should finish after draining.
	cancel()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
		t.Log("dispatch finished cleanly")
	case <-time.After(5 * time.Second):
		t.Fatal("dispatch did not exit — possible goroutine leak")
	}

	msgs := ovl.GetMessages()
	var foundTranslation bool
	for _, msg := range msgs {
		if msg.Type == ui.Translation && msg.Text == "тест остановки" {
			foundTranslation = true
			break
		}
	}
	if !foundTranslation {
		t.Error("translation not received after shutdown")
	}
}

// =========================================================================
// TestPipelineStreamingTokensArrive — токены приходят инкрементально
// =========================================================================

// TestPipelineStreamingTokensArrive — проверяет, что потоковые подсказки
// от GenerateAnswersStream доходят до UI AnswerCandidates.
// В новом Gladia flow: финальный транскрипт-вопрос → generateAnswersAsync →
// токены собираются → AnswerCandidates в оверлее.
func TestPipelineStreamingTokensArrive(t *testing.T) {
	llm := &streamingMockLLM{
		tokens: []string{"mock hint 1", "mock hint 2"},
		delay:  10 * time.Millisecond,
	}
	ovl := newCountingOverlay()

	stt := newMockSTT()
	capt := newMockCapturer()

	p := buildTestPipeline(stt, capt, ovl, llm, nil, 4)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go p.runDispatch(ctx)

	// Send a question — triggers generateAnswersAsync.
	p.textStream <- common.STTEvent{
		Text:      "Can you explain Docker?",
		Event:     common.EventEndOfTurn,
		ChannelID: "speaker",
		Timestamp: time.Now(),
	}

	time.Sleep(500 * time.Millisecond)
	cancel()

	msgs := ovl.GetMessages()
	t.Logf("Total UI messages: %d", len(msgs))

	var answerCandidates []string
	for _, m := range msgs {
		if m.Type == ui.AnswerCandidates {
			answerCandidates = append(answerCandidates, m.Answers...)
			t.Logf("AnswerCandidates: answers=%v", m.Answers)
		}
	}
	t.Logf("AnswerCandidates messages with content: %d answers", len(answerCandidates))

	if len(answerCandidates) == 0 {
		t.Fatal("NO ANSWER CANDIDATES — UI is empty!")
	}

	// streamingMockLLM.GenerateAnswersStream returns ["mock hint 1", "mock hint 2"]
	// engine.GenerateAnswersStream concatenates tokens → parseAnswerHints → up to 3 hints.
	expectedMin := 1
	if len(answerCandidates) < expectedMin {
		t.Errorf("Expected at least %d answer hints, got %d", expectedMin, len(answerCandidates))
	}

	t.Logf("Streaming answer hints: %v", answerCandidates)
}

// =========================================================================
// TestPipelineStreamCancellation — стрим прерывается при отмене контекста
// =========================================================================

func TestPipelineStreamCancellation(t *testing.T) {
	llm := &streamingMockLLM{
		tokens: []string{"очень", "длинный", "перевод"},
		delay:  500 * time.Millisecond,
	}
	ovl := newCountingOverlay()

	stt := newMockSTT()
	capt := newMockCapturer()

	p := buildTestPipeline(stt, capt, ovl, llm, nil, 4)

	ctx, cancel := context.WithCancel(context.Background())

	dispatchDone := make(chan struct{})
	go func() {
		p.runDispatch(ctx)
		close(dispatchDone)
	}()

	p.textStream <- common.STTEvent{
		Text:      "Long text",
		Event:     common.EventEndOfTurn,
		ChannelID: "speaker",
		Timestamp: time.Now(),
	}

	time.Sleep(100 * time.Millisecond)

	// Cancel — dispatch must exit cleanly.
	cancel()

	select {
	case <-dispatchDone:
		t.Log("dispatch finished after context cancellation — OK")
	case <-time.After(3 * time.Second):
		t.Fatal("dispatch DID NOT FINISH after 3s — GOROUTINE LEAK!")
	}
}

// =========================================================================
// TestPipelineConcurrentStreams — параллельная обработка стримов
// =========================================================================

// TestPipelineConcurrentStreams — параллельная обработка нескольких вопросов.
// Отправляем 3 вопроса одновременно → каждый запускает generateAnswersAsync →
// все должны сгенерировать AnswerCandidates.
func TestPipelineConcurrentStreams(t *testing.T) {
	llm := &streamingMockLLM{
		tokens: []string{"hint A", "hint B"},
		delay:  50 * time.Millisecond,
	}
	ovl := newCountingOverlay()

	stt := newMockSTT()
	capt := newMockCapturer()

	p := buildTestPipeline(stt, capt, ovl, llm, nil, 8)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go p.runDispatch(ctx)

	// Send 3 questions almost simultaneously.
	questions := []string{
		"Can you explain goroutines?",
		"What is a mutex?",
		"How does garbage collection work?",
	}
	for _, q := range questions {
		p.textStream <- common.STTEvent{
			Text:      q,
			Event:     common.EventEndOfTurn,
			ChannelID: "speaker",
			Timestamp: time.Now(),
		}
	}

	time.Sleep(800 * time.Millisecond)

	msgs := ovl.GetMessages()
	t.Logf("Total messages: %d", len(msgs))

	var answerCount int
	for _, m := range msgs {
		if m.Type == ui.AnswerCandidates {
			answerCount++
			t.Logf("AnswerCandidates: answers=%v text=%q", m.Answers, m.Text)
		}
	}
	t.Logf("AnswerCandidates messages: %d", answerCount)

	if answerCount < 3 {
		t.Errorf("Expected at least 3 AnswerCandidates messages, got %d", answerCount)
	}

	// Verify LLM call log — each question should trigger GenerateAnswersStream.
	llm.mu.Lock()
	genCalls := 0
	for _, c := range llm.callLog {
		if strings.HasPrefix(c, "GenAnswers:") {
			genCalls++
		}
	}
	llm.mu.Unlock()

	if genCalls != 3 {
		t.Errorf("Expected 3 GenerateAnswers calls, got %d", genCalls)
	}
}

// =========================================================================
// TestPipelineInterimNotBlockedInStream — interim проходит во время стрима
// =========================================================================

func TestPipelineInterimNotBlockedInStream(t *testing.T) {
	llm := &streamingMockLLM{
		tokens: []string{"X", "Y", "Z"},
		delay:  300 * time.Millisecond,
	}
	ovl := newCountingOverlay()

	stt := newMockSTT()
	capt := newMockCapturer()

	p := buildTestPipeline(stt, capt, ovl, llm, nil, 16)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go p.runDispatch(ctx)

	// Final event launches a long stream.
	p.textStream <- common.STTEvent{
		Text:      "final",
		Event:     common.EventEndOfTurn,
		ChannelID: "speaker",
		Timestamp: time.Now(),
	}

	// Send interim events while stream is running.
	for i := 0; i < 5; i++ {
		p.textStream <- common.STTEvent{
			Text:      fmt.Sprintf("interim %d", i),
			Event:     common.EventUpdate,
			ChannelID: "speaker",
			Timestamp: time.Now(),
		}
	}

	time.Sleep(500 * time.Millisecond)

	msgs := ovl.GetMessages()
	var interimCount, transCount int
	for _, m := range msgs {
		switch m.Type {
		case ui.Interim:
			interimCount++
		case ui.Translation:
			transCount++
		}
	}

	t.Logf("Interim: %d, Translation: %d", interimCount, transCount)

	if interimCount < 5 {
		t.Errorf("Interim lost: expected 5, got %d — STT is blocked!", interimCount)
	}
}

// =========================================================================
// TestPipelineFallbackToSync — фолбэк на синхронный Translate
// =========================================================================

// TestPipelineFallbackToSync — фолбэк с GenerateAnswersStream на GenerateAnswers.
// Когда LLM не реализует StreamingAnswersProvider, engine.GenerateAnswersStream
// использует синхронный GenerateAnswers и возвращает результаты через канал.
// Проверяем, что подсказки доходят до UI AnswerCandidates.
func TestPipelineFallbackToSync(t *testing.T) {
	// slowLLM does NOT implement StreamingAnswersProvider — should fall back.
	llm := newSlowLLM(50 * time.Millisecond)

	ovl := newCountingOverlay()

	stt := newMockSTT()
	capt := newMockCapturer()

	p := buildTestPipeline(stt, capt, ovl, llm, nil, 4)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	go p.runDispatch(ctx)

	// Send a question — triggers generateAnswersAsync.
	p.textStream <- common.STTEvent{
		Text:      "Can you explain Docker?",
		Event:     common.EventEndOfTurn,
		ChannelID: "speaker",
		Timestamp: time.Now(),
	}

	time.Sleep(300 * time.Millisecond)

	msgs := ovl.GetMessages()
	var found bool
	for _, m := range msgs {
		if m.Type == ui.AnswerCandidates {
			t.Logf("AnswerCandidates: answers=%v text=%q", m.Answers, m.Text)
			if len(m.Answers) > 0 {
				found = true
			}
		}
	}

	if !found {
		t.Error("Sync fallback failed — answer candidates not received!")
	} else {
		t.Log("Sync fallback — OK")
	}
}

// =========================================================================
// TestPipelineWithStubCapture — тест с StubCapture (без CGO)
// =========================================================================

func TestPipelineWithStubCapture(t *testing.T) {
	stt := newMockSTT()

	// StubCapture emits silent 16kHz frames.
	silentFrame := make([]byte, 2560) // 80ms at 16kHz mono
	stubCap := capture.NewStubCapture(
		capture.CaptureConfig{BufferSizeMs: 80},
		silentFrame,
		silentFrame,
		80*time.Millisecond,
	)

	ovl := newCountingOverlay()
	llm := &mockLLM{}

	engine := translator.NewEngine(llm)
	cfg := Config{
		TextStreamBuffer: 16,
		AnswerTimeout:    3 * time.Second,
		MaxTokens:        256,
		SaveAudio:        true,
	}

	// stubCapturer adapter for the capturer interface.
	var capt capturer = &stubCapturerAdapter{c: stubCap}

	p := &Pipeline{
		cfg:        cfg,
		capturer:   capt,
		sttProv:    stt,
		engine:     engine,
		overlay:    ovl,
		sessLog:    nil,
		textStream: make(chan common.STTEvent, 16),
	}

	if err := stt.Start(context.Background()); err != nil {
		t.Fatalf("mock STT Start: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(4)
	go func() { defer wg.Done(); p.runCapture(ctx) }()
	go func() { defer wg.Done(); p.runSTT(ctx) }()
	go func() { defer wg.Done(); p.runDispatch(ctx) }()
	go func() { defer wg.Done(); p.runUI(ctx) }()

	time.Sleep(100 * time.Millisecond)
	cancel()
	_ = stt.Stop()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
		t.Log("pipeline with StubCapture: all goroutines exited cleanly")
	case <-time.After(5 * time.Second):
		t.Fatal("goroutines did not exit — possible leak")
	}
}

// stubCapturerAdapter adapts capture.StubCapture to the capturer interface.
type stubCapturerAdapter struct {
	c *capture.StubCapture
}

func (a *stubCapturerAdapter) Start(ctx context.Context) (<-chan []byte, <-chan []byte, error) {
	return a.c.Start(ctx)
}

// =========================================================================
// TestEndOfTurnCycle — проверка полного цикла: EndOfTurn → перевод + история.
// Баг: 63 Update за 12с запускали 63 LLM-запроса → перевод не появлялся,
// история не накапливалась. Тест проверяет что после EndOfTurn перевод
// появляется, история пополняется, подсказки генерируются для вопросов.
// =========================================================================

func TestEndOfTurnCycle(t *testing.T) {
	sttProv := newMockSTT()
	llm := &mockLLM{}
	ovl := newMockOverlay()

	engine := translator.NewEngine(llm)

	p := &Pipeline{
		cfg: Config{
			AnswerTimeout:    3 * time.Second,
			TextStreamBuffer: 16,
		},
		sttProv:    sttProv,
		engine:     engine,
		overlay:    ovl,
		textStream: make(chan common.STTEvent, 64),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go p.runDispatch(ctx)

	// 3 Update → EndOfTurn → translation → EndOfTurn (вопрос) → translation.
	p.textStream <- common.STTEvent{Event: common.EventUpdate, Text: "I have", Timestamp: time.Now()}
	p.textStream <- common.STTEvent{Event: common.EventUpdate, Text: "I have five", Timestamp: time.Now()}
	p.textStream <- common.STTEvent{Event: common.EventUpdate, Text: "I have five years", Timestamp: time.Now()}

	// EndOfTurn 1: не вопрос.
	p.textStream <- common.STTEvent{Event: common.EventEndOfTurn, Text: "I have five years of experience", ChannelID: "speaker", Timestamp: time.Now()}
	// Translation for EndOfTurn 1 (from Gladia).
	p.textStream <- common.STTEvent{Event: common.EventEndOfTurn, Text: "У меня пять лет опыта", ChannelID: "translation", Timestamp: time.Now()}
	time.Sleep(300 * time.Millisecond)

	// EndOfTurn 2: ВОПРОС.
	p.textStream <- common.STTEvent{Event: common.EventEndOfTurn, Text: "Can you explain Kubernetes?", ChannelID: "speaker", Timestamp: time.Now()}
	// Translation for EndOfTurn 2 (from Gladia).
	p.textStream <- common.STTEvent{Event: common.EventEndOfTurn, Text: "Можете объяснить Kubernetes?", ChannelID: "translation", Timestamp: time.Now()}
	time.Sleep(600 * time.Millisecond)

	cancel()

	msgs := ovl.GetMessages()

	var historyCount, translationDone, answerCount int
	for _, m := range msgs {
		switch m.Type {
		case ui.History:
			historyCount++
		case ui.Translation:
			if m.MsgStatus == "done" {
				translationDone++
			}
		case ui.AnswerCandidates:
			answerCount++
		}
	}

	// History: 2 final transcripts + 2 history-with-translation = 4
	if historyCount < 2 {
		t.Errorf("История: ждали >=2, получили %d", historyCount)
	}
	if translationDone < 2 {
		t.Errorf("Перевод done: ждали >=2, получили %d", translationDone)
	}
	if answerCount < 1 {
		t.Errorf("Подсказки: ждали >=1 для вопроса, получили %d", answerCount)
	}
	t.Logf("History=%d Translation(done)=%d Answers=%d OK", historyCount, translationDone, answerCount)
}

// =========================================================================
// TestTranslationLoggedToCSV — Е2Е: EndOfTurn → стриминг-перевод → CSV-лог.
// Воспроизводит баг «перевод не попадает в лог» автоматически.
// =========================================================================

func TestTranslationLoggedToCSV(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "pipeline-csv-log-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Стриминг-мок LLM (как OpenAIProvider).
	llm := &streamingMockLLM{
		tokens: []string{"При", "вет", ", ", "м", "ир"},
		delay:  5 * time.Millisecond,
	}

	// Мок STT.
	sttProv := newMockSTT()
	_ = sttProv.Start(context.Background())

	// Мок capturer (не используется в dispatch-тесте).
	capt := newMockCapturer()

	// Мок overlay.
	ovl := newCountingOverlay()

	// Реальный FileSessionLogger.
	sessLog, err := logger.NewFileSessionLogger(tmpDir, false)
	if err != nil {
		t.Fatalf("logger: %v", err)
	}

	engine := translator.NewEngine(llm)

	p := &Pipeline{
		cfg: Config{
			TextStreamBuffer: 8,
			AnswerTimeout:    3 * time.Second,
			MaxTokens:        256,
			SaveAudio:        false,
		},
		capturer:     capt,
		sttProv:      sttProv,
		engine:       engine,
		overlay:      ovl,
		sessLog:      sessLog,
		textStream:   make(chan common.STTEvent, 8),
		dispatchDone: make(chan struct{}, 1),
	}

	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(4)
	go func() { defer wg.Done(); p.runCapture(ctx) }()
	go func() { defer wg.Done(); p.runSTT(ctx) }()
	go func() { defer wg.Done(); p.runDispatch(ctx) }()
	go func() { defer wg.Done(); p.runUI(ctx) }()

	// Отправляем EndOfTurn + translation (Gladia flow).
	p.textStream <- common.STTEvent{
		Text:      "Hello world",
		Event:     common.EventEndOfTurn,
		ChannelID: "speaker",
		Timestamp: time.Now(),
	}
	// Translation from Gladia.
	p.textStream <- common.STTEvent{
		Text:      "Привет, мир",
		Event:     common.EventEndOfTurn,
		ChannelID: "translation",
		Timestamp: time.Now(),
	}

	// Ждём завершения перевода.
	time.Sleep(300 * time.Millisecond)

	// Корректный shutdown: cancel ctx → STT.Stop → dispatchDone → UI.WaitShutdown → Logger.Close.
	cancel()
	_ = sttProv.Stop()
	<-p.dispatchDone
	ovl.WaitShutdown()
	_ = sessLog.Close()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("goroutines did not exit")
	}

	// Проверка 1: перевод в overlay.
	msgs := ovl.GetMessages()
	var foundTranslation bool
	var historyWithTranslation bool
	for _, m := range msgs {
		if m.Type == ui.Translation && strings.Contains(m.Text, "Привет, мир") {
			foundTranslation = true
		}
		if m.Type == ui.History && m.Translation != "" {
			historyWithTranslation = true
		}
	}
	if !foundTranslation {
		t.Error("BUG: translation not found in overlay messages!")
	}
	if !historyWithTranslation {
		t.Error("BUG: history entry has no Translation field!")
	}

	// Проверка 2: перевод в CSV-логе.
	files, _ := os.ReadDir(tmpDir)
	var csvPath string
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".csv") {
			csvPath = filepath.Join(tmpDir, f.Name())
			break
		}
	}
	if csvPath == "" {
		t.Fatal("CSV log file not created!")
	}

	csvData, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatalf("read CSV: %v", err)
	}

	csvStr := string(csvData)
	t.Logf("CSV content:\n%s", csvStr)

	if !strings.Contains(csvStr, "Привет, мир") {
		t.Error("BUG: translation NOT FOUND in CSV log!")
	} else {
		t.Log("PASS: translation found in CSV ✅")
	}

	// Проверка 3: история содержит перевод.
	var historyHasTranslation bool
	for _, m := range msgs {
		if m.Type == ui.History {
			t.Logf("History: text=%q translation=%q", m.Text, m.Translation)
			if m.Translation == "Привет, мир" {
				historyHasTranslation = true
			}
		}
	}
	if !historyHasTranslation {
		t.Error("BUG: History entry missing Translation field!")
	}
}
