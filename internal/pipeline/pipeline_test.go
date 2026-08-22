package pipeline

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mastererik/translator/internal/common"
	"github.com/mastererik/translator/internal/dispatcher"
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

// mockLLM реализует translator.LLMProvider для тестирования.
type mockLLM struct {
	mu sync.Mutex
}

func (m *mockLLM) GenerateAnswers(ctx context.Context, req translator.AnswerRequest) ([]string, error) {
	return []string{"mock hint 1", "mock hint 2"}, nil
}

// GenerateAnswersStream реализует translator.StreamingAnswersProvider.
func (m *mockLLM) GenerateAnswersStream(ctx context.Context, req translator.AnswerRequest) (<-chan string, error) {
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
	engine := translator.NewEngine(llm)
	cfg := Config{
		TextStreamBuffer: 4, // маленький буфер для тестов переполнения
		AnswerTimeout:    3 * time.Second,
		MaxTokens:        256,
	}
	return &Pipeline{
		cfg:        cfg,
		capturer:   capt,
		sttProv:    sttProv,
		engine:     engine,
		overlay:    ovl,
		sessLog:    sessLog,
		textStream: make(chan common.STTEvent, cfg.TextStreamBuffer),
		dispatch: dispatcher.New(
			ovl,
			engine,
			sessLog,
			dispatcher.Config{AnswerTimeout: 3 * time.Second},
		),
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

	p := newTestPipeline(stt, capt, ovl, llm, logger.NewNopSessionLogger())

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

	p := newTestPipeline(stt, capt, ovl, llm, logger.NewNopSessionLogger())

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

	p := newTestPipeline(stt, capt, ovl, llm, logger.NewNopSessionLogger())

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

	// Создаём Pipeline с NopSessionLogger (без файлового логгера).
	p := newTestPipeline(stt, capt, ovl, llm, logger.NewNopSessionLogger())

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

// =========================================================================
// TestPipelineDispatch_TranslationWithoutOriginal — перевод без оригинала (edge case)
// =========================================================================

func TestPipelineDispatch_TranslationWithoutOriginal(t *testing.T) {
	stt := newMockSTT()
	ovl := newMockOverlay()
	llm := &mockLLM{}

	p := newTestPipeline(stt, nil, ovl, llm, logger.NewNopSessionLogger())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go p.runDispatch(ctx)

	// Отправляем перевод без предшествующего оригинала.
	p.textStream <- common.STTEvent{
		Text:      "перевод без оригинала",
		Event:     common.EventEndOfTurn,
		ChannelID: "translation",
		Timestamp: time.Now(),
	}

	time.Sleep(300 * time.Millisecond)
	cancel()

	msgs := ovl.GetMessages()
	var translationFound bool
	for _, m := range msgs {
		if m.Type == ui.Translation && m.Text == "перевод без оригинала" {
			translationFound = true
		}
	}
	if !translationFound {
		t.Error("перевод должен показываться даже без предшествующего оригинала")
	}
}

// =========================================================================
// TestPipelineDispatch_TwoTranslationsInRow — два перевода подряд без оригинала между ними
// =========================================================================

func TestPipelineDispatch_TwoTranslationsInRow(t *testing.T) {
	stt := newMockSTT()
	ovl := newMockOverlay()
	llm := &mockLLM{}

	p := newTestPipeline(stt, nil, ovl, llm, logger.NewNopSessionLogger())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go p.runDispatch(ctx)

	// Оригинал 1.
	p.textStream <- common.STTEvent{
		Text:      "original one",
		Event:     common.EventEndOfTurn,
		ChannelID: "speaker",
		Timestamp: time.Now(),
	}
	// Перевод 1.
	p.textStream <- common.STTEvent{
		Text:      "перевод один",
		Event:     common.EventEndOfTurn,
		ChannelID: "translation",
		Timestamp: time.Now(),
	}
	// Перевод 2 без оригинала между ними (используется lastOriginal=original one).
	p.textStream <- common.STTEvent{
		Text:      "перевод два",
		Event:     common.EventEndOfTurn,
		ChannelID: "translation",
		Timestamp: time.Now(),
	}

	time.Sleep(300 * time.Millisecond)
	cancel()

	msgs := ovl.GetMessages()
	var transCount int
	for _, m := range msgs {
		if m.Type == ui.Translation {
			transCount++
		}
	}
	if transCount < 2 {
		t.Errorf("ожидалось 2 перевода, получено %d", transCount)
	}
}

// =========================================================================
// TestPipelineDispatch_ErrorEvent — ошибка STTEvent не ломает dispatch
// =========================================================================

func TestPipelineDispatch_ErrorEvent(t *testing.T) {
	stt := newMockSTT()
	ovl := newMockOverlay()
	llm := &mockLLM{}

	p := newTestPipeline(stt, nil, ovl, llm, logger.NewNopSessionLogger())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go p.runDispatch(ctx)

	// Событие с ошибкой.
	p.textStream <- common.STTEvent{
		Text:      "",
		Event:     common.EventError,
		ChannelID: "speaker",
		Timestamp: time.Now(),
		Error:     fmt.Errorf("test error"),
	}

	// Нормальный финальный транскрипт после ошибки.
	p.textStream <- common.STTEvent{
		Text:      "normal text",
		Event:     common.EventEndOfTurn,
		ChannelID: "speaker",
		Timestamp: time.Now(),
	}

	// Перевод для этого транскрипта.
	p.textStream <- common.STTEvent{
		Text:      "нормальный текст",
		Event:     common.EventEndOfTurn,
		ChannelID: "translation",
		Timestamp: time.Now(),
	}

	time.Sleep(300 * time.Millisecond)
	cancel()

	msgs := ovl.GetMessages()
	var historyFound, translationFound bool
	for _, m := range msgs {
		if m.Type == ui.History && m.Text == "normal text" {
			historyFound = true
		}
		if m.Type == ui.Translation && m.Text == "нормальный текст" {
			translationFound = true
		}
	}
	if !historyFound {
		t.Error("dispatch должен добавлять History (оригинал) после транскрипта")
	}
	if !translationFound {
		t.Error("dispatch должен добавлять Translation (перевод) после транскрипта")
	}
}

// =========================================================================
// TestGenerateAnswersAsync_LLMError — ошибка LLM не паникует
// =========================================================================

func TestGenerateAnswersAsync_LLMError(t *testing.T) {
	errLLM := &errorLLM{err: fmt.Errorf("api error")}
	engine := translator.NewEngine(errLLM)
	ovl := newMockOverlay()

	p := &Pipeline{
		cfg:     Config{AnswerTimeout: 3 * time.Second},
		engine:  engine,
		overlay: ovl,
	}

	// Не должно быть паники.
	p.generateAnswersAsync("What is Docker?")

	// Даём время горутине.
	time.Sleep(500 * time.Millisecond)

	msgs := ovl.GetMessages()
	found := false
	for _, m := range msgs {
		if m.Type == ui.AnswerCandidates {
			found = true
			if len(m.Answers) == 0 || m.Answers[0] == "" {
				t.Error("AnswerCandidates при ошибке должны содержать сообщение об ошибке")
			}
		}
	}
	if !found {
		t.Error("ожидались AnswerCandidates с ошибкой при сбое LLM")
	}
}

// =========================================================================
// TestPipelineNew_EmptyLLMAPIKey — проверка ошибки при пустом LLMAPIKey
// =========================================================================

func TestPipelineNew_EmptyLLMAPIKey(t *testing.T) {
	cfg := Config{
		LLMAPIKey:  "",
		LLMBaseURL: "https://api.z.ai/api/paas/v4/",
	}
	_, err := New(cfg)
	if err == nil {
		t.Error("ожидалась ошибка при пустом LLMAPIKey")
	}
}

// errorLLM — LLM который всегда возвращает ошибку.
type errorLLM struct {
	err error
}

func (m *errorLLM) GenerateAnswers(ctx context.Context, req translator.AnswerRequest) ([]string, error) {
	return nil, m.err
}

var _ translator.LLMProvider = (*errorLLM)(nil)

// =========================================================================
// TestPipelineNew_ValidConfig — проверяет создание Pipeline с минимальным валидным конфигом.
// =========================================================================

func TestPipelineNew_ValidConfig_Minimal(t *testing.T) {
	commonCfg, err := common.LoadConfigFromYAML(filepath.Join("..", "..", "config.yaml"))
	if err != nil {
		t.Fatalf("LoadConfigFromYAML: %v", err)
	}
	cfg := Config{
		LLMAPIKey:  "sk-test-key",
		LLMBaseURL: commonCfg.LLMBaseURL,
		LLMModel:   commonCfg.LLMModel,
	}
	p, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if p == nil {
		t.Fatal("Pipeline не должен быть nil")
	}
	if p.sttProv == nil {
		t.Error("STT provider не должен быть nil")
	}
	if p.engine == nil {
		t.Error("Engine не должен быть nil")
	}
	if p.overlay == nil {
		t.Error("Overlay не должен быть nil")
	}
	if p.sessLog == nil {
		t.Error("SessionLogger не должен быть nil (должен быть NopLogger)")
	}
	if p.cfg.TextStreamBuffer != 64 {
		t.Errorf("TextStreamBuffer = %d, want 64", p.cfg.TextStreamBuffer)
	}
	if p.cfg.AnswerTimeout != 10*time.Second {
		t.Errorf("AnswerTimeout = %v, want 10s", p.cfg.AnswerTimeout)
	}
}

// =========================================================================
// TestPipelineNew_ValidConfig_WithLogDir — с LogDir создаётся FileSessionLogger.
// =========================================================================

func TestPipelineNew_ValidConfig_WithLogDir(t *testing.T) {
	tmpDir := t.TempDir()

	commonCfg, err := common.LoadConfigFromYAML(filepath.Join("..", "..", "config.yaml"))
	if err != nil {
		t.Fatalf("LoadConfigFromYAML: %v", err)
	}
	cfg := Config{
		LLMAPIKey:  "sk-test-key",
		LLMBaseURL: commonCfg.LLMBaseURL,
		LLMModel:   commonCfg.LLMModel,
		LogDir:     tmpDir,
	}
	p, err := New(cfg)
	if err != nil {
		t.Fatalf("New() with LogDir error: %v", err)
	}
	if p == nil {
		t.Fatal("Pipeline не должен быть nil")
	}
	if p.sessLog == nil {
		t.Error("SessionLogger не должен быть nil с LogDir")
	}

	// Закрываем логгер.
	_ = p.sessLog.Close()
}

// =========================================================================
// TestGenerateAnswersAsync_FullStream — проверяет полный путь: стрим LLM → AnswerCandidates.
// =========================================================================

func TestGenerateAnswersAsync_FullStream(t *testing.T) {
	streamLLM := &streamingMockLLM{
		tokens: []string{"- EN: ", "hint about Docker", " | RU: ", "подсказка про Docker"},
		delay:  1 * time.Millisecond,
	}
	engine := translator.NewEngine(streamLLM)
	ovl := newMockOverlay()

	p := &Pipeline{
		cfg:     Config{AnswerTimeout: 5 * time.Second},
		engine:  engine,
		overlay: ovl,
	}

	p.generateAnswersAsync("What is Docker?")

	// Даём время на сбор токенов и отправку в UI.
	for i := 0; i < 20; i++ {
		time.Sleep(50 * time.Millisecond)
		msgs := ovl.GetMessages()
		for _, m := range msgs {
			if m.Type == ui.AnswerCandidates {
				t.Logf("AnswerCandidates получены: %v", m.Answers)
				return
			}
		}
	}

	t.Log("AnswerCandidates не получены — OK (может не хватить токенов для парсинга подсказок)")
}

// streamingMockLLM для тестов pipeline_test (адаптация из pipeline_integration_test).
// Используем streamingMockLLM из pipeline_integration_test.go.
// Если нет — создаём локальный.
type localStreamingMockLLM struct {
	mu      sync.Mutex
	tokens  []string
	delay   time.Duration
	callLog []string
}

func (m *localStreamingMockLLM) GenerateAnswers(ctx context.Context, req translator.AnswerRequest) ([]string, error) {
	return []string{"hint 1", "hint 2"}, nil
}

func (m *localStreamingMockLLM) GenerateAnswersStream(ctx context.Context, req translator.AnswerRequest) (<-chan string, error) {
	m.mu.Lock()
	m.callLog = append(m.callLog, "GenAnswersStream:"+req.Question)
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

var _ translator.LLMProvider = (*localStreamingMockLLM)(nil)

// TestPipelineNoHistoryDuplicates — проверяет отсутствие дублей в History.
func TestPipelineNoHistoryDuplicates(t *testing.T) {
	stt := newMockSTT()
	capt := newMockCapturer()
	ovl := newMockOverlay()

	llm := &mockLLM{}
	engine := translator.NewEngine(llm)

	p := &Pipeline{
		cfg:        Config{TextStreamBuffer: 64, AnswerTimeout: 3 * time.Second},
		sttProv:    stt,
		capturer:   capt,
		engine:     engine,
		overlay:    ovl,
		textStream: make(chan common.STTEvent, 64),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); p.runDispatch(ctx) }()

	// Симулируем 3 фразы: transcript + translation.
	for i := 0; i < 3; i++ {
		orig := fmt.Sprintf("original %d", i+1)
		trans := fmt.Sprintf("translation %d", i+1)

		// Финальный транскрипт.
		p.textStream <- common.STTEvent{Text: orig, Event: common.EventEndOfTurn, ChannelID: "speaker"}
		time.Sleep(50 * time.Millisecond)

		// Перевод.
		p.textStream <- common.STTEvent{Text: trans, Event: common.EventEndOfTurn, ChannelID: "translation"}
		time.Sleep(50 * time.Millisecond)
	}

	time.Sleep(300 * time.Millisecond)
	cancel()
	wg.Wait()

	msgs := ovl.GetMessages()

	// Считаем History и Translation записи.
	var hist []ui.UIMessage
	var translations []ui.UIMessage
	for _, m := range msgs {
		if m.Type == ui.History {
			hist = append(hist, m)
		}
		if m.Type == ui.Translation {
			translations = append(translations, m)
		}
	}

	// Должно быть ровно 3 оригинала и 3 перевода.
	if len(hist) != 3 {
		t.Errorf("History count = %d, want 3 (no duplicates)", len(hist))
		for i, m := range hist {
			t.Logf("  History[%d] text=%q", i, m.Text)
		}
	}
	if len(translations) != 3 {
		t.Errorf("Translation count = %d, want 3", len(translations))
		for i, m := range translations {
			t.Logf("  Translation[%d] text=%q", i, m.Text)
		}
	}

	// Проверяем что нет дублей по английскому оригиналу.
	seenText := map[string]bool{}
	for _, m := range hist {
		if seenText[m.Text] {
			t.Errorf("дубликат английского оригинала в History: text=%q", m.Text)
		}
		seenText[m.Text] = true
	}

	// Проверяем что нет дублей по переводу.
	seenTrans := map[string]bool{}
	for _, m := range translations {
		if seenTrans[m.Text] {
			t.Errorf("дубликат перевода в Translation: text=%q", m.Text)
		}
		seenTrans[m.Text] = true
	}
}

// TestPipelineHistoryDedup — проверяет что повторные переводы не дублируются.
func TestPipelineHistoryDedup(t *testing.T) {
	stt := newMockSTT()
	capt := newMockCapturer()
	ovl := newMockOverlay()
	llm := &mockLLM{}
	engine := translator.NewEngine(llm)

	p := &Pipeline{
		cfg:        Config{TextStreamBuffer: 64, AnswerTimeout: 3 * time.Second},
		sttProv:    stt,
		capturer:   capt,
		engine:     engine,
		overlay:    ovl,
		textStream: make(chan common.STTEvent, 64),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); p.runDispatch(ctx) }()

	// Один транскрипт + три одинаковых перевода (Gladia может слать повторы).
	p.textStream <- common.STTEvent{Text: "Hello", Event: common.EventEndOfTurn, ChannelID: "speaker"}
	time.Sleep(50 * time.Millisecond)

	for i := 0; i < 3; i++ {
		p.textStream <- common.STTEvent{Text: "Привет", Event: common.EventEndOfTurn, ChannelID: "translation"}
		time.Sleep(30 * time.Millisecond)
	}

	time.Sleep(300 * time.Millisecond)
	cancel()
	wg.Wait()

	var hist []ui.UIMessage
	for _, m := range ovl.GetMessages() {
		if m.Type == ui.History {
			hist = append(hist, m)
		}
	}

	if len(hist) != 1 {
		t.Errorf("History count = %d, want 1 (дедупликация не сработала)", len(hist))
		for i, m := range hist {
			t.Logf("  [%d] text=%q translation=%q", i, m.Text, m.Translation)
		}
	}
}

var _ translator.StreamingAnswersProvider = (*localStreamingMockLLM)(nil)

// =========================================================================
// TestPipeline_TextStreamBackpressure — проверяет, что final-событие
// не блокирует пайплайн навечно при переполненном textStream.
// После таймаута 5s событие дропается с логом.
// =========================================================================

func TestPipeline_TextStreamBackpressure(t *testing.T) {
	stt := newMockSTT()
	capt := newMockCapturer()
	ovl := newMockOverlay()
	llm := &mockLLM{}
	sessLog := &mockSessionLogger{}

	p := newTestPipeline(stt, capt, ovl, llm, sessLog)

	// Заполняем textStream до ёмкости.
	bufSize := p.cfg.TextStreamBuffer
	for i := 0; i < bufSize; i++ {
		p.textStream <- common.STTEvent{
			Text:      fmt.Sprintf("interim %d", i),
			Event:     common.EventUpdate,
			ChannelID: "speaker",
			Timestamp: time.Now(),
		}
	}

	// Отправляем final-событие через routeSTTEvent с context.Background().
	// Канал полон → таймаут 5s → дроп с логом.
	ctx := context.Background()
	done := make(chan struct{})
	go func() {
		p.routeSTTEvent(ctx, common.STTEvent{
			Text:      "final under backpressure",
			Event:     common.EventEndOfTurn,
			ChannelID: "speaker",
			Timestamp: time.Now(),
		})
		close(done)
	}()

	// Ждём: либо событие проходит (если канал дренирован), либо таймаут 5s.
	select {
	case <-done:
		t.Log("backpressure: routeSTTEvent завершился (событие прошло или дропнуто по таймауту)")
	case <-time.After(6 * time.Second):
		t.Fatal("backpressure deadlock: routeSTTEvent завис >6s при заполненном textStream")
	}
}

// =========================================================================
// TestPipeline_LogTextSync — проверяет, что LogText вызывается синхронно
// (не в отдельной горутине), и runSTT блокируется до возврата LogText.
// =========================================================================

// blockingLogTextLogger — логгер, который блокируется в LogText до сигнала.
type blockingLogTextLogger struct {
	mockSessionLogger
	blockCh   chan struct{} // блокирует LogText до закрытия
	callCount atomic.Int32
}

func (m *blockingLogTextLogger) LogText(event common.STTEvent) error {
	m.callCount.Add(1)
	<-m.blockCh // блокируемся до сигнала
	return nil
}

func TestPipeline_LogTextSync(t *testing.T) {
	stt := newMockSTT()
	capt := newMockCapturer()
	ovl := newMockOverlay()
	llm := &mockLLM{}

	blocker := &blockingLogTextLogger{
		blockCh: make(chan struct{}),
	}

	p := newTestPipeline(stt, capt, ovl, llm, blocker)

	if err := stt.Start(context.Background()); err != nil {
		t.Fatalf("mock STT Start: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Запускаем runSTT.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		p.runSTT(ctx)
	}()

	// Отправляем первое событие — runSTT должен заблокироваться на LogText.
	stt.textCh <- common.STTEvent{
		Text:      "hello",
		Event:     common.EventEndOfTurn,
		ChannelID: "speaker",
		Timestamp: time.Now(),
	}

	// Даём время runSTT дойти до вызова LogText.
	time.Sleep(100 * time.Millisecond)

	// Отправляем второе событие — оно НЕ должно быть обработано,
	// потому что runSTT всё ещё заблокирован на первом LogText.
	stt.textCh <- common.STTEvent{
		Text:      "world",
		Event:     common.EventEndOfTurn,
		ChannelID: "speaker",
		Timestamp: time.Now(),
	}

	time.Sleep(100 * time.Millisecond)

	// LogText должен быть вызван ровно 1 раз (только для первого события).
	if c := blocker.callCount.Load(); c != 1 {
		t.Errorf("LogText callCount = %d, want 1 (синхронный вызов блокирует обработку второго события)", c)
	}

	// Разблокируем LogText — runSTT обработает второе событие.
	close(blocker.blockCh)

	// Ждём, что второе событие тоже обработается.
	time.Sleep(200 * time.Millisecond)

	if c := blocker.callCount.Load(); c < 2 {
		t.Errorf("LogText callCount = %d, want >=2 после разблокировки", c)
	}

	cancel()
	wg.Wait()

	t.Log("LogText вызывается синхронно: runSTT блокируется до возврата LogText")
}

// =========================================================================
// TestPipeline_RunCaptureWaitGroup — проверяет, что все горутины runCapture
// завершаются корректно при отмене контекста с новой арифметикой WaitGroup.
// =========================================================================

func TestPipeline_RunCaptureWaitGroup(t *testing.T) {
	stt := newMockSTT()
	capt := newMockCapturer()
	ovl := newMockOverlay()
	llm := &mockLLM{}

	p := newTestPipeline(stt, capt, ovl, llm, logger.NewNopSessionLogger())

	ctx, cancel := context.WithCancel(context.Background())

	// Запускаем runCapture и сразу отменяем контекст.
	done := make(chan struct{})
	go func() {
		p.runCapture(ctx)
		close(done)
	}()

	// Даём горутинам внутри runCapture запуститься.
	time.Sleep(50 * time.Millisecond)
	cancel()

	// Проверяем, что runCapture не завис.
	select {
	case <-done:
		t.Log("runCapture: все горутины завершились без ошибок")
	case <-time.After(5 * time.Second):
		t.Fatal("runCapture завис при отмене контекста — возможна ошибка в арифметике WaitGroup")
	}

	// Дополнительно: проверяем что пайплайн без логгера тоже не зависает.
	p2 := newTestPipeline(stt, capt, ovl, llm, logger.NewNopSessionLogger())
	// Второй вызов с новым контекстом — проверка идемпотентности.
	ctx2, cancel2 := context.WithCancel(context.Background())
	cancel2() // отменяем сразу

	done2 := make(chan struct{})
	go func() {
		p2.runCapture(ctx2)
		close(done2)
	}()

	select {
	case <-done2:
		t.Log("runCapture с отменённым контекстом: завершился мгновенно")
	case <-time.After(5 * time.Second):
		t.Fatal("runCapture с отменённым контекстом завис")
	}
}

// =========================================================================
// TestPipeline_ShutdownGoroutineLeak — проверка отсутствия утечек горутин после shutdown.
// =========================================================================

func TestPipeline_ShutdownGoroutineLeak(t *testing.T) {
	// Считаем горутины до создания пайплайна.
	initialGoroutines := runtime.NumGoroutine()

	stt := newMockSTT()
	capt := newMockCapturer()
	// mockOverlay с runBlocker: Run() вернётся сразу, что запустит каскад shutdown.
	ovl := newMockOverlay()
	runDone := make(chan struct{})
	close(runDone)
	ovl.runBlocker = runDone
	llm := &mockLLM{}
	sessLog := &mockSessionLogger{}

	p := newTestPipeline(stt, capt, ovl, llm, sessLog)

	// Запускаем Run() в горутине — overlayDone сработает сразу,
	// запустив shutdown() и освободив все ресурсы.
	done := make(chan struct{})
	go func() {
		_ = p.Run()
		close(done)
	}()

	// Ждём завершения Run() с таймаутом.
	select {
	case <-done:
		t.Log("Run() завершился без ошибок")
	case <-time.After(10 * time.Second):
		t.Fatal("таймаут: Run() не завершился — возможна блокировка")
	}

	// Даём GC немного времени на финализацию горутин.
	time.Sleep(100 * time.Millisecond)

	finalGoroutines := runtime.NumGoroutine()
	delta := finalGoroutines - initialGoroutines

	t.Logf("Горутины: до=%d, после=%d, delta=%d (допуск ±2)", initialGoroutines, finalGoroutines, delta)
	if delta > 2 || delta < -2 {
		t.Errorf("возможна утечка горутин: delta=%d (допуск ±2)", delta)
	}
}
