package pipeline

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mastererik/translator/internal/common"
	"github.com/mastererik/translator/internal/logger"
	"github.com/mastererik/translator/internal/translator"
	"github.com/mastererik/translator/internal/ui"
)

// =========================================================================
// Моки
// =========================================================================

// mockSTT реализует stt.STTProvider для тестирования.
type mockSTT struct {
	mu        sync.Mutex
	started   bool
	stopped   bool
	stopOrder atomic.Int32 // отслеживание порядка шатдауна
	audioCh   chan []byte
	textCh    chan common.STTEvent
	startErr  error
	stopErr   error
	stopFn    func() // вызывается перед закрытием textCh в Stop

	// Управление: если не nil, Start/Stop блокируются для тестов порядка.
	startBlocker chan struct{}
	stopBlocker  chan struct{}
}

func newMockSTT() *mockSTT {
	return &mockSTT{
		audioCh: make(chan []byte, 256),
		textCh:  make(chan common.STTEvent, 256),
	}
}

func (m *mockSTT) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.startBlocker != nil {
		blocker := m.startBlocker
		m.mu.Unlock()
		select {
		case <-blocker:
		case <-ctx.Done():
			return ctx.Err()
		}
	} else {
		m.mu.Unlock()
	}

	m.mu.Lock()
	m.started = true
	err := m.startErr
	m.mu.Unlock()
	return err
}

func (m *mockSTT) Stop() error {
	m.mu.Lock()
	stopped := m.stopped
	m.stopped = true
	fn := m.stopFn
	m.mu.Unlock()

	if stopped {
		return nil // идемпотентный
	}

	if fn != nil {
		fn()
	}

	close(m.textCh)

	m.mu.Lock()
	if m.stopBlocker != nil {
		blocker := m.stopBlocker
		m.mu.Unlock()
		<-blocker
	} else {
		m.mu.Unlock()
	}

	return m.stopErr
}

func (m *mockSTT) AudioStream() chan<- []byte {
	return m.audioCh
}

func (m *mockSTT) TextStream() <-chan common.STTEvent {
	return m.textCh
}

func (m *mockSTT) isStarted() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.started
}

func (m *mockSTT) isStopped() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopped
}

// mockOverlay реализует Overlay для тестирования.
type mockOverlay struct {
	mu           sync.Mutex
	messages     []ui.UIMessage
	shutdownDone chan struct{}
	runErr       error
	addMsgDelay  time.Duration // искусственная задержка AddMessage
	runBlocker   chan struct{} // если задан, Run блокируется пока не закроют

	closeOrder     atomic.Int32 // отслеживание порядка шатдауна
	waitShutdownFn func()       // вызывается в WaitShutdown
}

func newMockOverlay() *mockOverlay {
	return &mockOverlay{
		messages:     make([]ui.UIMessage, 0),
		shutdownDone: make(chan struct{}),
	}
}

func (m *mockOverlay) AddMessage(msg ui.UIMessage) {
	if m.addMsgDelay > 0 {
		time.Sleep(m.addMsgDelay)
	}
	m.mu.Lock()
	m.messages = append(m.messages, msg)
	m.mu.Unlock()
}

func (m *mockOverlay) GetMessages() []ui.UIMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ui.UIMessage, len(m.messages))
	copy(out, m.messages)
	return out
}

func (m *mockOverlay) Run(ctx context.Context) error {
	defer close(m.shutdownDone)

	if m.runBlocker != nil {
		select {
		case <-m.runBlocker:
			return m.runErr
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	<-ctx.Done()
	return m.runErr
}

func (m *mockOverlay) WaitShutdown() {
	if m.waitShutdownFn != nil {
		m.waitShutdownFn()
	}
	<-m.shutdownDone
}

func (m *mockOverlay) messageCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.messages)
}

// mockCapturer реализует интерфейс capturer для тестирования.
type mockCapturer struct {
	mu         sync.Mutex
	started    bool
	loopbackCh chan []byte
	micCh      chan []byte
	startErr   error

	startBlocker chan struct{} // если задан, Start блокируется
}

func newMockCapturer() *mockCapturer {
	return &mockCapturer{
		loopbackCh: make(chan []byte, 16),
		micCh:      make(chan []byte, 16),
	}
}

func (m *mockCapturer) Start(ctx context.Context) (<-chan []byte, <-chan []byte, error) {
	if m.startBlocker != nil {
		select {
		case <-m.startBlocker:
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}

	m.mu.Lock()
	m.started = true
	err := m.startErr
	m.mu.Unlock()

	if err != nil {
		return nil, nil, err
	}

	return m.loopbackCh, m.micCh, nil
}

// mockLLM реализует translator.LLMProvider и translator.StreamingTranslator.
type mockLLM struct {
	mu             sync.Mutex
	translateFn    func(ctx context.Context, text string, history []string) (string, error)
	translateDelay time.Duration
}

func (m *mockLLM) Translate(ctx context.Context, text string, history []string) (string, error) {
	if m.translateDelay > 0 {
		select {
		case <-time.After(m.translateDelay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	m.mu.Lock()
	fn := m.translateFn
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, text, history)
	}
	return "[translated] " + text, nil
}

func (m *mockLLM) GenerateAnswers(ctx context.Context, question string, cvContext string) ([]string, error) {
	return []string{"mock hint 1", "mock hint 2"}, nil
}

// TranslateStream реализует StreamingTranslator.
func (m *mockLLM) TranslateStream(ctx context.Context, text string, history []string) (<-chan string, error) {
	ch := make(chan string, 1)
	translation, err := m.Translate(ctx, text, history)
	if err != nil {
		close(ch)
		return nil, fmt.Errorf("translate stream: %w", err)
	}
	ch <- translation
	close(ch)
	return ch, nil
}

// GenerateAnswersStream реализует StreamingTranslator.
func (m *mockLLM) GenerateAnswersStream(ctx context.Context, question string, cvContext string) (<-chan string, error) {
	ch := make(chan string, 2)
	ch <- "mock hint 1"
	ch <- "mock hint 2"
	close(ch)
	return ch, nil
}

// mockSessionLogger реализует logger.SessionLogger для тестирования.
type mockSessionLogger struct {
	mu         sync.Mutex
	closed     bool
	closeErr   error
	closeOrder atomic.Int32
	closeFn    func() // вызывается в Close
}

func (m *mockSessionLogger) LogText(event common.STTEvent) error { return nil }
func (m *mockSessionLogger) LogTranslation(event common.STTEvent, translation string, answers []string) error {
	return nil
}
func (m *mockSessionLogger) SaveAudioChunk(channelID string, pcm []byte) error { return nil }
func (m *mockSessionLogger) LogDebug(msg string) error                         { return nil }
func (m *mockSessionLogger) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closeFn != nil {
		m.closeFn()
	}
	m.closed = true
	return m.closeErr
}
func (m *mockSessionLogger) isClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

// =========================================================================
// Хелперы
// =========================================================================

// newTestPipeline создаёт Pipeline для тестов с заданными моками.
// Все обязательные поля инициализируются, чтобы избежать nil-паник.
func newTestPipeline(sttProv *mockSTT, capt *mockCapturer, ovl *mockOverlay, llm *mockLLM, sessLog logger.SessionLogger) *Pipeline {
	engine := translator.NewEngine(llm, 3)
	cfg := Config{
		TextStreamBuffer: 4, // маленький буфер для тестов переполнения
		StreamDoneBuffer: 4,
		StreamTimeout:    5 * time.Second,
		AnswerTimeout:    3 * time.Second,
		MaxTokens:        256,
		WindowSize:       3,
	}
	return &Pipeline{
		cfg:        cfg,
		capturer:   capt,
		sttProv:    sttProv,
		engine:     engine,
		overlay:    ovl,
		sessLog:    sessLog,
		textStream: make(chan common.STTEvent, cfg.TextStreamBuffer),
		streamDone: make(chan struct{}, cfg.StreamDoneBuffer),
	}
}

// =========================================================================
// TestPipelineGracefulShutdown — все горутины завершаются без утечек
// =========================================================================

func TestPipelineGracefulShutdown(t *testing.T) {
	stt := newMockSTT()
	capt := newMockCapturer()
	ovl := newMockOverlay()
	llm := &mockLLM{}

	p := newTestPipeline(stt, capt, ovl, llm, nil)

	if err := stt.Start(context.Background()); err != nil {
		t.Fatalf("mock STT Start: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	// Запускаем все горутины пайплайна.
	wg.Add(4)
	go func() { defer wg.Done(); p.runCapture(ctx) }()
	go func() { defer wg.Done(); p.runSTT(ctx) }()
	go func() { defer wg.Done(); p.runDispatch(ctx) }()
	go func() { defer wg.Done(); p.runUI(ctx) }()

	// Даём горутинам время запуститься.
	time.Sleep(50 * time.Millisecond)

	// Отменяем контекст — все горутины должны завершиться.
	cancel()

	// Ждём с таймаутом, чтобы обнаружить зависания.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Log("все горутины завершились без утечек")
	case <-time.After(10 * time.Second):
		t.Fatal("таймаут: горутины не завершились — возможна утечка")
	}

	// Проверяем, что STT был остановлен (shutdown() не вызывался, но Stop() ожидается из runSTT).
	// runSTT завершается при ctx.Done() и закрывает textStream, но Stop() вызывается только из shutdown().
	// Здесь мы просто проверяем, что runDispatch не заблокировался навсегда.
	if !stt.isStarted() {
		t.Error("mockSTT должен быть запущен")
	}
}

// =========================================================================
// TestPipelineFinalEventNotLost — final-событие не теряется при медленном dispatch
// =========================================================================

func TestPipelineFinalEventNotLost(t *testing.T) {
	stt := newMockSTT()
	capt := newMockCapturer()
	ovl := newMockOverlay()
	llm := &mockLLM{}

	p := newTestPipeline(stt, capt, ovl, llm, nil)

	// Заполняем textStream до ёмкости (буфер = 4).
	bufSize := p.cfg.TextStreamBuffer
	for i := 0; i < bufSize; i++ {
		p.textStream <- common.STTEvent{
			Text:      fmt.Sprintf("interim %d", i),
			Event:     common.EventUpdate,
			ChannelID: "speaker",
			Timestamp: time.Now(),
		}
	}

	// Канал полон. Пытаемся отправить final-событие — должно заблокироваться
	// (блокирующая отправка для EventEndOfTurn).
	ctx := context.Background()
	finalEvent := common.STTEvent{
		Text:      "final message",
		Event:     common.EventEndOfTurn,
		ChannelID: "speaker",
		Timestamp: time.Now(),
	}

	// Запускаем отправку final в горутине — она заблокируется на заполненном канале.
	sendDone := make(chan bool, 1)
	go func() {
		p.routeSTTEvent(ctx, finalEvent)
		sendDone <- true
	}()

	// Даём время горутине заблокироваться.
	time.Sleep(50 * time.Millisecond)

	// Сливаем 1 элемент из канала — final должен пройти.
	select {
	case ev := <-p.textStream:
		if ev.Event != common.EventUpdate {
			t.Errorf("ожидался interim, получили %s", ev.Event)
		}
	case <-time.After(time.Second):
		t.Fatal("не удалось прочитать interim из канала")
	}

	// Теперь final должен отправиться.
	select {
	case <-sendDone:
		t.Log("final-событие успешно отправлено (не потеряно)")
	case <-time.After(2 * time.Second):
		t.Fatal("final-событие потеряно — отправка не завершилась")
	}

	// Дренируем оставшиеся interim-события.
	remaining := len(p.textStream)
	for i := 0; i < remaining-1; i++ {
		<-p.textStream
	}

	// Теперь в канале должен быть один элемент — final.
	select {
	case ev := <-p.textStream:
		if ev.Event != common.EventEndOfTurn || ev.Text != "final message" {
			t.Errorf("некорректное final-событие: event=%s text=%s", ev.Event, ev.Text)
		} else {
			t.Log("final-событие обнаружено в канале")
		}
	default:
		t.Error("final-событие не обнаружено в канале после отправки")
	}
}

// =========================================================================
// TestPipelineInterimEventDropped — interim может быть пропущен при переполнении
// =========================================================================

func TestPipelineInterimEventDropped(t *testing.T) {
	stt := newMockSTT()
	capt := newMockCapturer()
	ovl := newMockOverlay()
	llm := &mockLLM{}

	p := newTestPipeline(stt, capt, ovl, llm, nil)

	// Заполняем textStream до ёмкости (буфер = 4).
	bufSize := p.cfg.TextStreamBuffer
	for i := 0; i < bufSize; i++ {
		p.textStream <- common.STTEvent{
			Text:      fmt.Sprintf("interim %d", i),
			Event:     common.EventUpdate,
			ChannelID: "speaker",
			Timestamp: time.Now(),
		}
	}

	// Пытаемся отправить ещё один interim — должно быть отброшено (default branch).
	ctx := context.Background()
	p.routeSTTEvent(ctx, common.STTEvent{
		Text:      "dropped interim",
		Event:     common.EventUpdate,
		ChannelID: "speaker",
		Timestamp: time.Now(),
	})

	// Проверяем: канал всё ещё полон ровно bufSize элементами.
	count := len(p.textStream)
	if count != bufSize {
		t.Errorf("после попытки отправки при переполнении: len(textStream) = %d, want %d", count, bufSize)
	}

	// Дренируем — проверяем, что "dropped interim" отсутствует.
	found := false
	for i := 0; i < count; i++ {
		ev := <-p.textStream
		if ev.Text == "dropped interim" {
			found = true
		}
	}
	if found {
		t.Error("interim-событие НЕ должно было попасть в канал при переполнении")
	}

	t.Log("interim-событие корректно отброшено при переполнении канала")
}

// =========================================================================
// TestPipelineShutdownOrder — правильный порядок шатдауна
// =========================================================================

func TestPipelineShutdownOrder(t *testing.T) {
	// Используем общий атомарный счётчик для отслеживания порядка вызовов.
	var order atomic.Int32

	// mockSTT с отслеживанием порядка.
	stt := newMockSTT()
	stt.stopFn = func() {
		stt.stopOrder.Store(order.Add(1))
	}

	capt := newMockCapturer()

	// mockOverlay с отслеживанием WaitShutdown.
	ovl := newMockOverlay()
	close(ovl.shutdownDone) // WaitShutdown завершится сразу
	ovl.shutdownDone = make(chan struct{})
	close(ovl.shutdownDone)
	ovl.waitShutdownFn = func() {
		ovl.closeOrder.Store(order.Add(1))
	}

	llm := &mockLLM{}
	sessLog := &mockSessionLogger{}
	sessLog.closeFn = func() {
		sessLog.closeOrder.Store(order.Add(1))
	}

	p := newTestPipeline(stt, capt, ovl, llm, sessLog)

	// Вызываем shutdown напрямую.
	p.shutdown()

	sttOrd := stt.stopOrder.Load()
	ovlOrd := ovl.closeOrder.Load()
	sessOrd := sessLog.closeOrder.Load()

	t.Logf("Порядок шатдауна: STT.Stop=%d, Overlay.WaitShutdown=%d, SessLog.Close=%d",
		sttOrd, ovlOrd, sessOrd)

	if sttOrd == 0 {
		t.Error("STT.Stop не вызван")
	}
	if !stt.isStopped() {
		t.Error("mockSTT должен быть остановлен после shutdown")
	}
	if !sessLog.isClosed() {
		t.Error("SessionLogger.Close не был вызван")
	}

	// Порядок: Stop → WaitShutdown → Close
	if !(sttOrd < ovlOrd && ovlOrd < sessOrd) {
		t.Errorf("Неверный порядок: Stop=%d, WaitShutdown=%d, Close=%d (ожидается 1 < 2 < 3)",
			sttOrd, ovlOrd, sessOrd)
	} else {
		t.Log("Порядок шатдауна корректен: STT.Stop → Overlay.WaitShutdown → SessLog.Close")
	}
}

// =========================================================================
// TestPipelineNoSessLog — Pipeline работает без логгера
// =========================================================================

func TestPipelineNoSessLog(t *testing.T) {
	stt := newMockSTT()
	capt := newMockCapturer()
	ovl := newMockOverlay()
	llm := &mockLLM{}

	// Создаём Pipeline с sessLog = nil.
	p := newTestPipeline(stt, capt, ovl, llm, nil)

	if p.sessLog != nil {
		t.Fatal("sessLog должен быть nil")
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

	time.Sleep(30 * time.Millisecond)
	cancel()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Log("Pipeline без логгера: все горутины завершились без паники")
	case <-time.After(5 * time.Second):
		t.Fatal("таймаут: горутины зависли")
	}

	// Проверяем, что shutdown() без sessLog не паникует.
	p.shutdown()
	t.Log("shutdown() без sessLog отработал без паники")
}

// =========================================================================
// TestPipelineShutdownOrder_ExplicitTracking — явное отслеживание порядка
// =========================================================================

func TestPipelineShutdownOrder_ExplicitTracking(t *testing.T) {
	// Создаём моки, которые записывают порядок вызова в общий счётчик.
	var order atomic.Int32

	stt := newMockSTT()
	stt.stopFn = func() {
		stt.stopOrder.Store(order.Add(1))
	}

	capt := newMockCapturer()

	ovl := newMockOverlay()
	// WaitShutdown завершится сразу.
	close(ovl.shutdownDone)
	ovl.shutdownDone = make(chan struct{})
	close(ovl.shutdownDone)
	ovl.waitShutdownFn = func() {
		ovl.closeOrder.Store(order.Add(1))
	}

	llm := &mockLLM{}

	sessLog := &mockSessionLogger{}
	sessLog.closeFn = func() {
		sessLog.closeOrder.Store(order.Add(1))
	}

	p := newTestPipeline(stt, capt, ovl, llm, sessLog)

	// Вызываем shutdown.
	p.shutdown()

	sttOrd := stt.stopOrder.Load()
	ovlOrd := ovl.closeOrder.Load()
	sessOrd := sessLog.closeOrder.Load()

	t.Logf("Порядок шатдауна: STT.Stop=%d, Overlay.WaitShutdown=%d, SessLog.Close=%d",
		sttOrd, ovlOrd, sessOrd)

	if sttOrd == 0 {
		t.Error("STT.Stop не вызван")
	}
	if ovlOrd == 0 {
		t.Error("Overlay.WaitShutdown не вызван")
	}
	if sessOrd == 0 {
		t.Error("SessLog.Close не вызван")
	}

	// Порядок должен быть: Stop (1) → WaitShutdown (2) → Close (3)
	if !(sttOrd < ovlOrd && ovlOrd < sessOrd) {
		t.Errorf("Неверный порядок шатдауна: Stop=%d, WaitShutdown=%d, Close=%d (ожидается 1 < 2 < 3)",
			sttOrd, ovlOrd, sessOrd)
	} else {
		t.Log("Порядок шатдауна корректен: STT.Stop → Overlay.WaitShutdown → SessLog.Close")
	}
}
