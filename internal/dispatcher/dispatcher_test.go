package dispatcher

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mastererik/translator/internal/common"
	"github.com/mastererik/translator/internal/translator"
	"github.com/mastererik/translator/internal/ui"
)

// ── Моки ────────────────────────────────────────────────────────────────

type mockOverlay struct {
	mu       sync.Mutex
	messages []ui.UIMessage
}

func (m *mockOverlay) AddMessage(msg ui.UIMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, msg)
}

func (m *mockOverlay) GetMessages() []ui.UIMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ui.UIMessage, len(m.messages))
	copy(out, m.messages)
	return out
}

type mockEngine struct {
	mu      sync.Mutex
	answers []string
	err     error
	delay   time.Duration
	calls   []string
	reqs    []translator.AnswerRequest
}

func (m *mockEngine) GenerateAnswers(ctx context.Context, req translator.AnswerRequest) ([]string, error) {
	m.mu.Lock()
	m.calls = append(m.calls, req.Question)
	m.reqs = append(m.reqs, req)
	answers := make([]string, len(m.answers))
	copy(answers, m.answers)
	err := m.err
	m.mu.Unlock()

	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return answers, err
}

func (m *mockEngine) Calls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.calls))
	copy(out, m.calls)
	return out
}

func (m *mockEngine) Reqs() []translator.AnswerRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]translator.AnswerRequest, len(m.reqs))
	copy(out, m.reqs)
	return out
}

type mockLogger struct {
	mu        sync.Mutex
	transLogs []common.STTEvent
	debugLogs []string
}

func (m *mockLogger) LogTranslation(event common.STTEvent, translation string, answers []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.transLogs = append(m.transLogs, event)
	return nil
}

func (m *mockLogger) LogDebug(msg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.debugLogs = append(m.debugLogs, msg)
	return nil
}

func (m *mockLogger) Translations() []common.STTEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]common.STTEvent, len(m.transLogs))
	copy(out, m.transLogs)
	return out
}

func (m *mockLogger) DebugLogs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.debugLogs))
	copy(out, m.debugLogs)
	return out
}

func (m *mockLogger) HasDebug(contains string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, msg := range m.debugLogs {
		if containsStr(msg, contains) {
			return true
		}
	}
	return false
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && searchStr(s, sub)
}

func searchStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ── Тесты ───────────────────────────────────────────────────────────────

func TestDispatcherInterim(t *testing.T) {
	overlay := &mockOverlay{}
	engine := &mockEngine{}
	logger := &mockLogger{}
	cfg := DefaultConfig()
	cfg.AnswerQueueSize = -1 // отключаем очередь, тестируем старый путь
	d := New(overlay, engine, logger, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	textStream := make(chan common.STTEvent, 8)
	done := make(chan struct{}, 1)

	go d.Run(ctx, textStream, done)

	// Отправляем interim-событие.
	textStream <- common.STTEvent{
		Event:     common.EventUpdate,
		ChannelID: "speaker",
		Text:      "I have five years",
		Timestamp: time.Now(),
	}

	// Ждём обработки.
	time.Sleep(50 * time.Millisecond)

	msgs := overlay.GetMessages()
	if len(msgs) != 1 {
		t.Fatalf("ожидалось 1 сообщение, получено %d", len(msgs))
	}
	if msgs[0].Type != ui.Interim {
		t.Errorf("ожидался Interim, получен %v", msgs[0].Type)
	}
	if msgs[0].Text != "I have five years" {
		t.Errorf("текст: %q", msgs[0].Text)
	}

	cancel()
	<-done
}

func TestDispatcherFinalTranscript(t *testing.T) {
	overlay := &mockOverlay{}
	engine := &mockEngine{}
	logger := &mockLogger{}
	cfg := DefaultConfig()
	cfg.AnswerQueueSize = -1
	d := New(overlay, engine, logger, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	textStream := make(chan common.STTEvent, 8)
	done := make(chan struct{}, 1)

	go d.Run(ctx, textStream, done)

	// Финальный транскрипт (не вопрос).
	textStream <- common.STTEvent{
		Event:     common.EventEndOfTurn,
		ChannelID: "speaker",
		Text:      "I have five years of experience",
		Timestamp: time.Now(),
	}

	time.Sleep(50 * time.Millisecond)

	// После финального транскрипта сразу идёт History в overlay.
	msgs := overlay.GetMessages()
	if len(msgs) != 1 {
		t.Fatalf("ожидалось 1 сообщение (History), получено %d", len(msgs))
	}
	if msgs[0].Type != ui.History {
		t.Errorf("ожидался History, получен %v", msgs[0].Type)
	}
	if msgs[0].Text != "I have five years of experience" {
		t.Errorf("текст: %q", msgs[0].Text)
	}

	cancel()
	<-done
}

func TestDispatcherTranslationPaired(t *testing.T) {
	overlay := &mockOverlay{}
	engine := &mockEngine{}
	logger := &mockLogger{}
	cfg := DefaultConfig()
	cfg.AnswerQueueSize = -1
	d := New(overlay, engine, logger, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	textStream := make(chan common.STTEvent, 8)
	done := make(chan struct{}, 1)

	go d.Run(ctx, textStream, done)

	// 1. Финальный транскрипт.
	textStream <- common.STTEvent{
		Event:     common.EventEndOfTurn,
		ChannelID: "speaker",
		Text:      "I have five years of experience",
		Timestamp: time.Now(),
	}

	time.Sleep(20 * time.Millisecond)

	// 2. Перевод.
	textStream <- common.STTEvent{
		Event:     common.EventEndOfTurn,
		ChannelID: "translation",
		Text:      "У меня пять лет опыта",
		Timestamp: time.Now(),
	}

	time.Sleep(50 * time.Millisecond)

	msgs := overlay.GetMessages()
	if len(msgs) != 2 {
		t.Fatalf("ожидалось 2 сообщения (History + Translation), получено %d", len(msgs))
	}

	// Первое — History (оригинал без перевода, сразу после финального транскрипта).
	if msgs[0].Type != ui.History {
		t.Errorf("ожидался History, получен %v", msgs[0].Type)
	}
	if msgs[0].Text != "I have five years of experience" {
		t.Errorf("оригинал: %q", msgs[0].Text)
	}
	if msgs[0].Translation != "" {
		t.Errorf("не должно быть перевода в первом History: %q", msgs[0].Translation)
	}

	// Второе — Translation.
	if msgs[1].Type != ui.Translation {
		t.Errorf("ожидался Translation, получен %v", msgs[1].Type)
	}
	if msgs[1].Text != "У меня пять лет опыта" {
		t.Errorf("текст перевода: %q", msgs[1].Text)
	}
}

func TestDispatcherQuestionTriggersAnswers(t *testing.T) {
	overlay := &mockOverlay{}
	engine := &mockEngine{
		answers: []string{"EN: Redis is a cache | RU: Redis — это кэш"},
	}
	logger := &mockLogger{}
	cfg := DefaultConfig()
	cfg.AnswerQueueSize = -1
	d := New(overlay, engine, logger, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	textStream := make(chan common.STTEvent, 8)
	done := make(chan struct{}, 1)

	go d.Run(ctx, textStream, done)

	// Вопрос (заканчивается на ?).
	textStream <- common.STTEvent{
		Event:     common.EventEndOfTurn,
		ChannelID: "speaker",
		Text:      "What is Redis?",
		Timestamp: time.Now(),
	}

	// Ждём генерации подсказок.
	time.Sleep(200 * time.Millisecond)

	cancel()
	<-done

	calls := engine.Calls()
	if len(calls) != 1 {
		t.Fatalf("ожидался 1 вызов GenerateAnswers, получено %d", len(calls))
	}
	if calls[0] != "What is Redis?" {
		t.Errorf("вопрос: %q", calls[0])
	}

	msgs := overlay.GetMessages()
	// Должен быть AnswerCandidates.
	found := false
	for _, m := range msgs {
		if m.Type == ui.AnswerCandidates {
			found = true
			if len(m.Answers) != 1 {
				t.Errorf("ожидалась 1 подсказка, получено %d", len(m.Answers))
			}
		}
	}
	if !found {
		t.Error("AnswerCandidates не найдены в сообщениях UI")
	}
}

func TestDispatcherHistoryDedup(t *testing.T) {
	overlay := &mockOverlay{}
	engine := &mockEngine{}
	logger := &mockLogger{}
	cfg := DefaultConfig()
	cfg.AnswerQueueSize = -1
	d := New(overlay, engine, logger, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	textStream := make(chan common.STTEvent, 8)
	done := make(chan struct{}, 1)

	go d.Run(ctx, textStream, done)

	original := "I have five years of experience"

	// Финальный транскрипт.
	textStream <- common.STTEvent{
		Event: common.EventEndOfTurn, ChannelID: "speaker",
		Text: original, Timestamp: time.Now(),
	}
	time.Sleep(20 * time.Millisecond)

	// Первый перевод.
	textStream <- common.STTEvent{
		Event: common.EventEndOfTurn, ChannelID: "translation",
		Text: "У меня пять лет опыта", Timestamp: time.Now(),
	}
	time.Sleep(20 * time.Millisecond)

	// Второй перевод (дубль).
	textStream <- common.STTEvent{
		Event: common.EventEndOfTurn, ChannelID: "translation",
		Text: "У меня пять лет опыта v2", Timestamp: time.Now(),
	}
	time.Sleep(50 * time.Millisecond)

	msgs := overlay.GetMessages()
	// 3 сообщения: History (от default) + Translation1 + Translation2.
	// History приходит только из default-ветки, translation-ветка отправляет только Translation.
	historyCount := 0
	for _, m := range msgs {
		if m.Type == ui.History {
			historyCount++
		}
	}
	if historyCount != 1 {
		t.Errorf("ожидалась 1 History-запись, получено %d", historyCount)
	}
}

func TestDispatcherGracefulShutdown(t *testing.T) {
	overlay := &mockOverlay{}
	engine := &mockEngine{}
	logger := &mockLogger{}
	cfg := DefaultConfig()
	cfg.AnswerQueueSize = -1
	d := New(overlay, engine, logger, cfg)

	ctx, cancel := context.WithCancel(context.Background())

	textStream := make(chan common.STTEvent, 8)
	done := make(chan struct{}, 1)

	go d.Run(ctx, textStream, done)

	// Отмена контекста → Run должен завершиться.
	cancel()

	select {
	case <-done:
		// OK.
	case <-time.After(2 * time.Second):
		t.Fatal("dispatcher не завершился после отмены контекста")
	}
}

func TestDispatcherTextStreamClose(t *testing.T) {
	overlay := &mockOverlay{}
	engine := &mockEngine{}
	logger := &mockLogger{}
	cfg := DefaultConfig()
	cfg.AnswerQueueSize = -1
	d := New(overlay, engine, logger, cfg)

	ctx := context.Background()
	textStream := make(chan common.STTEvent, 8)
	done := make(chan struct{}, 1)

	go d.Run(ctx, textStream, done)

	// Закрываем textStream.
	close(textStream)

	select {
	case <-done:
		// OK.
	case <-time.After(2 * time.Second):
		t.Fatal("dispatcher не завершился после закрытия textStream")
	}
}

func TestDispatcherReportDrop(t *testing.T) {
	overlay := &mockOverlay{}
	engine := &mockEngine{}
	logger := &mockLogger{}
	cfg := DefaultConfig()
	cfg.AnswerQueueSize = -1
	d := New(overlay, engine, logger, cfg)

	// Первый дроп — логирует warn и сбрасывает счётчик.
	d.ReportDrop("audioCh")
	if d.DropCount() != 0 {
		t.Errorf("после warn счётчик должен быть 0, получено %d", d.DropCount())
	}

	// Ещё 9 дропов — накапливаются без логов (< 10s интервал).
	for i := 0; i < 9; i++ {
		d.ReportDrop("audioCh")
	}
	if d.DropCount() != 9 {
		t.Errorf("ожидалось 9 накопленных дропов, получено %d", d.DropCount())
	}

	// Ещё один — итого 10 накоплено.
	d.ReportDrop("textStream")
	if d.DropCount() != 10 {
		t.Errorf("ожидалось 10 накопленных дропов, получено %d", d.DropCount())
	}
}

// ── Новые тесты: очередь, дедупликация, пустой ответ ─────────────────

func TestDispatcherAnswerQueueSequential(t *testing.T) {
	// Проверяем, что с очередью вопросы обрабатываются последовательно
	// одним consumer'ом, а не параллельными горутинами.
	engine := &mockEngine{
		answers: []string{"ответ"},
		delay:   50 * time.Millisecond,
	}
	overlay := &mockOverlay{}
	logger := &mockLogger{}
	d := New(overlay, engine, logger, DefaultConfig())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	textStream := make(chan common.STTEvent, 8)
	done := make(chan struct{}, 1)

	go d.Run(ctx, textStream, done)

	// Отправляем 4 вопроса подряд.
	questions := []string{"What?", "What is your name?", "Where are you from?", "How old are you?"}
	for _, q := range questions {
		textStream <- common.STTEvent{
			Event:     common.EventEndOfTurn,
			ChannelID: "speaker",
			Text:      q,
			Timestamp: time.Now(),
		}
		time.Sleep(5 * time.Millisecond) // небольшая пауза между событиями
	}

	// Ждём обработки всех вопросов (4 × 50ms + запас).
	time.Sleep(500 * time.Millisecond)

	cancel()
	<-done

	calls := engine.Calls()
	if len(calls) != 4 {
		t.Fatalf("ожидалось 4 вызова GenerateAnswers, получено %d", len(calls))
	}

	// Порядок должен сохраниться.
	for i, q := range questions {
		if calls[i] != q {
			t.Errorf("вызов %d: ожидался %q, получен %q", i, q, calls[i])
		}
	}
}

func TestDispatcherAnswerQueueDedup(t *testing.T) {
	// Повторяющиеся подряд вопросы должны пропускаться.
	engine := &mockEngine{
		answers: []string{"ответ"},
		delay:   20 * time.Millisecond,
	}
	overlay := &mockOverlay{}
	logger := &mockLogger{}
	d := New(overlay, engine, logger, DefaultConfig())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	textStream := make(chan common.STTEvent, 8)
	done := make(chan struct{}, 1)

	go d.Run(ctx, textStream, done)

	// Три одинаковых вопроса подряд.
	for i := 0; i < 3; i++ {
		textStream <- common.STTEvent{
			Event:     common.EventEndOfTurn,
			ChannelID: "speaker",
			Text:      "What is Redis?",
			Timestamp: time.Now(),
		}
		time.Sleep(5 * time.Millisecond)
	}

	time.Sleep(300 * time.Millisecond)

	cancel()
	<-done

	calls := engine.Calls()
	if len(calls) != 1 {
		t.Fatalf("ожидался 1 вызов GenerateAnswers (дедупликация), получено %d: %v", len(calls), calls)
	}
	if calls[0] != "What is Redis?" {
		t.Errorf("вопрос: %q", calls[0])
	}

	// Проверяем, что дубликат залогирован.
	if !logger.HasDebug("дубликат вопроса пропущен") {
		t.Error("ожидался debug-лог о пропуске дубликата")
	}
}

func TestDispatcherAnswerQueueOverflow(t *testing.T) {
	// При переполнении очереди вопросы должны дропаться с логом.
	engine := &mockEngine{
		delay: 200 * time.Millisecond, // медленный consumer
	}
	overlay := &mockOverlay{}
	logger := &mockLogger{}

	// Маленькая очередь.
	cfg := Config{
		AnswerTimeout:   10 * time.Second,
		AnswerQueueSize: 2,
	}
	d := New(overlay, engine, logger, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	textStream := make(chan common.STTEvent, 8)
	done := make(chan struct{}, 1)

	go d.Run(ctx, textStream, done)

	// Отправляем 5 вопросов при размере очереди 2.
	for i := 0; i < 5; i++ {
		textStream <- common.STTEvent{
			Event:     common.EventEndOfTurn,
			ChannelID: "speaker",
			Text:      fmt.Sprintf("Question %d?", i),
			Timestamp: time.Now(),
		}
		time.Sleep(5 * time.Millisecond)
	}

	time.Sleep(800 * time.Millisecond)

	cancel()
	<-done

	// Должен быть лог о переполнении.
	if !logger.HasDebug("очередь подсказок переполнена") {
		t.Error("ожидался debug-лог о переполнении очереди")
	}
}

func TestDispatcherEmptyAnswerLogged(t *testing.T) {
	// Пустой ответ от LLM должен логироваться.
	engine := &mockEngine{
		answers: []string{}, // пустой ответ
	}
	overlay := &mockOverlay{}
	logger := &mockLogger{}
	cfg := DefaultConfig()
	cfg.AnswerQueueSize = -1
	d := New(overlay, engine, logger, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	textStream := make(chan common.STTEvent, 8)
	done := make(chan struct{}, 1)

	go d.Run(ctx, textStream, done)

	textStream <- common.STTEvent{
		Event:     common.EventEndOfTurn,
		ChannelID: "speaker",
		Text:      "What is Redis?",
		Timestamp: time.Now(),
	}

	time.Sleep(200 * time.Millisecond)

	cancel()
	<-done

	// Должен быть лог о пустых подсказках.
	if !logger.HasDebug("подсказки пусты") {
		t.Error("ожидался debug-лог о пустых подсказках")
	}
}

func TestDispatcherAnswerQueueShutdownDrain(t *testing.T) {
	// При shutdown worker дочитывает очередь.
	engine := &mockEngine{
		answers: []string{"ответ"},
		delay:   30 * time.Millisecond,
	}
	overlay := &mockOverlay{}
	logger := &mockLogger{}
	d := New(overlay, engine, logger, DefaultConfig())

	ctx, cancel := context.WithCancel(context.Background())

	textStream := make(chan common.STTEvent, 8)
	done := make(chan struct{}, 1)

	go d.Run(ctx, textStream, done)

	// Отправляем 3 вопроса и сразу отменяем контекст.
	questions := []string{"Q1?", "Q2?", "Q3?"}
	for _, q := range questions {
		textStream <- common.STTEvent{
			Event:     common.EventEndOfTurn,
			ChannelID: "speaker",
			Text:      q,
			Timestamp: time.Now(),
		}
	}
	time.Sleep(10 * time.Millisecond)

	// Отменяем контекст — worker должен дренировать очередь.
	cancel()

	select {
	case <-done:
		// OK.
	case <-time.After(2 * time.Second):
		t.Fatal("dispatcher не завершился после отмены контекста с непустой очередью")
	}

	// Все 3 вопроса должны быть обработаны (дренирование).
	calls := engine.Calls()
	if len(calls) != 3 {
		t.Fatalf("ожидалось 3 вызова после дренирования, получено %d: %v", len(calls), calls)
	}
}

// ── Тесты покрытия (Шаг 4) ───────────────────────────────────────────

// 4.1: engine возвращает ошибку → UI получает сообщение с ⚠️.
func TestDispatcherGenerateAnswersError(t *testing.T) {
	engine := &mockEngine{
		err: fmt.Errorf("LLM timeout"),
	}
	overlay := &mockOverlay{}
	logger := &mockLogger{}
	cfg := DefaultConfig()
	cfg.AnswerQueueSize = -1
	d := New(overlay, engine, logger, cfg)

	d.GenerateAnswers("What is Redis?")

	msgs := overlay.GetMessages()
	if len(msgs) != 1 {
		t.Fatalf("ожидалось 1 сообщение об ошибке, получено %d", len(msgs))
	}
	if msgs[0].Type != ui.AnswerCandidates {
		t.Errorf("ожидался AnswerCandidates, получен %v", msgs[0].Type)
	}
	if len(msgs[0].Answers) != 1 {
		t.Fatalf("ожидался 1 ответ с ошибкой, получено %d", len(msgs[0].Answers))
	}
	if !stringsContains(msgs[0].Answers[0], "⚠️") && !stringsContains(msgs[0].Answers[0], "Ошибка") {
		t.Errorf("сообщение об ошибке не содержит ⚠️ или 'Ошибка': %q", msgs[0].Answers[0])
	}
	if !logger.HasDebug("генерация подсказок не удалась") {
		t.Error("ожидался debug-лог об ошибке генерации")
	}
}

// 4.2: overlay=nil + ошибка engine → не падает.
func TestDispatcherGenerateAnswersNilOverlay(t *testing.T) {
	engine := &mockEngine{
		err: fmt.Errorf("LLM timeout"),
	}
	logger := &mockLogger{}
	cfg := DefaultConfig()
	cfg.AnswerQueueSize = -1
	d := New(nil, engine, logger, cfg)

	// Не должно паниковать.
	d.GenerateAnswers("What is Redis?")

	if !logger.HasDebug("генерация подсказок не удалась") {
		t.Error("ожидался debug-лог об ошибке генерации")
	}
}

// 4.3: sessLog=nil + пустой ответ → не падает.
func TestDispatcherEmptyAnswerNoLogger(t *testing.T) {
	engine := &mockEngine{
		answers: []string{}, // пустой
	}
	overlay := &mockOverlay{}
	cfg := DefaultConfig()
	cfg.AnswerQueueSize = -1
	d := New(overlay, engine, nil, cfg)

	// Не должно паниковать при sessLog=nil.
	d.GenerateAnswers("What is Redis?")

	// overlay не должен получить сообщений (пустой ответ).
	msgs := overlay.GetMessages()
	if len(msgs) != 0 {
		t.Errorf("ожидалось 0 сообщений при пустом ответе, получено %d", len(msgs))
	}
}

// 4.4: закрыть answerCh → answerWorker выходит без паники.
func TestDispatcherAnswerWorkerChannelClose(t *testing.T) {
	engine := &mockEngine{
		answers: []string{"ответ"},
	}
	overlay := &mockOverlay{}
	logger := &mockLogger{}
	d := New(overlay, engine, logger, DefaultConfig())

	ctx := context.Background()

	// Запускаем worker вручную.
	go d.answerWorker(ctx)

	// Ждём немного и закрываем канал.
	time.Sleep(20 * time.Millisecond)
	close(d.answerCh)

	// Ждём завершения worker'а.
	select {
	case <-d.answerDone:
		// OK — worker завершился без паники.
	case <-time.After(2 * time.Second):
		t.Fatal("answerWorker не завершился после закрытия канала")
	}
}

// 4.5: drainQueue с дубликатами → дубли пропускаются.
func TestDispatcherDrainQueueWithDupes(t *testing.T) {
	engine := &mockEngine{
		answers: []string{"ответ"},
	}
	overlay := &mockOverlay{}
	logger := &mockLogger{}
	d := New(overlay, engine, logger, DefaultConfig())

	// Заполняем очередь: Q1, Q1 (дубль), Q2.
	d.answerCh <- "Q1?"
	d.answerCh <- "Q1?" // дубликат
	d.answerCh <- "Q2?"

	lastQuestion := ""
	d.drainQueue(&lastQuestion)

	calls := engine.Calls()
	if len(calls) != 2 {
		t.Fatalf("ожидалось 2 вызова (Q1 + Q2, дубль пропущен), получено %d: %v", len(calls), calls)
	}
	if calls[0] != "Q1?" {
		t.Errorf("первый вызов: %q", calls[0])
	}
	if calls[1] != "Q2?" {
		t.Errorf("второй вызов: %q", calls[1])
	}
}

// 4.6: закрыть канал во время drain → выход без паники.
func TestDispatcherDrainQueueChannelClose(t *testing.T) {
	engine := &mockEngine{
		answers: []string{"ответ"},
	}
	overlay := &mockOverlay{}
	logger := &mockLogger{}
	d := New(overlay, engine, logger, DefaultConfig())

	// Закрываем канал.
	close(d.answerCh)

	lastQuestion := ""
	// Не должно паниковать.
	d.drainQueue(&lastQuestion)

	// Ни одного вызова engine.
	calls := engine.Calls()
	if len(calls) != 0 {
		t.Errorf("ожидалось 0 вызовов, получено %d", len(calls))
	}
}

// 4.7: DefaultConfig (AnswerQueueSize=0) → форсируется в 16.
func TestDispatcherNewDefaultQueueSize(t *testing.T) {
	engine := &mockEngine{}
	overlay := &mockOverlay{}
	logger := &mockLogger{}

	// AnswerQueueSize=0 должен форсироваться в default (16).
	cfg := Config{
		AnswerTimeout:   10 * time.Second,
		AnswerQueueSize: 0,
	}
	d := New(overlay, engine, logger, cfg)

	if d.answerCh == nil {
		t.Fatal("answerCh не должен быть nil при AnswerQueueSize=0")
	}
	if cap(d.answerCh) != 16 {
		t.Errorf("размер answerCh = %d, ожидалось 16", cap(d.answerCh))
	}
	if d.answerDone == nil {
		t.Error("answerDone не должен быть nil")
	}

	// Проверяем, что очередь работает.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go d.answerWorker(ctx)

	d.answerCh <- "Test?"
	time.Sleep(50 * time.Millisecond)

	cancel()
	<-d.answerDone
}

// 4.8: LogTranslation возвращает ошибку → логируется.
func TestDispatcherLogTranslationError(t *testing.T) {
	errLogger := &mockErrorLogger{transErr: fmt.Errorf("disk full")}
	engine := &mockEngine{}
	overlay := &mockOverlay{}
	cfg := DefaultConfig()
	cfg.AnswerQueueSize = -1
	d := New(overlay, engine, errLogger, cfg)

	lastOriginal := common.STTEvent{
		Event:     common.EventEndOfTurn,
		ChannelID: "speaker",
		Text:      "Hello world",
		Timestamp: time.Now(),
	}

	// Отправляем перевод — LogTranslation должен вернуть ошибку.
	transEvent := common.STTEvent{
		Event:     common.EventEndOfTurn,
		ChannelID: "translation",
		Text:      "Привет мир",
		Timestamp: time.Now(),
	}
	d.route(transEvent, &lastOriginal)

	// Логирование теперь асинхронное — ждём завершения горутины.
	time.Sleep(50 * time.Millisecond)

	// Должен быть лог об ошибке.
	if !errLogger.HasDebug("ошибка логирования перевода") {
		t.Error("ожидался debug-лог об ошибке логирования перевода")
	}
}

// TestDispatcher_LogTranslationSurvivesShutdown проверяет, что асинхронные
// горутины логирования перевода завершаются до выхода из Run().
// Без logWg.Wait() sessLog.Close() мог быть вызван до завершения LogTranslation
// → паника или потеря данных.
func TestDispatcher_LogTranslationSurvivesShutdown(t *testing.T) {
	engine := &mockEngine{}
	overlay := &mockOverlay{}

	// Используем mockLogger — он уже отслеживает вызовы LogTranslation и LogDebug.
	logger := &mockLogger{}
	cfg := DefaultConfig()
	cfg.AnswerQueueSize = -1
	d := New(overlay, engine, logger, cfg)

	ctx := context.Background()
	textStream := make(chan common.STTEvent, 8)
	done := make(chan struct{}, 1)

	go d.Run(ctx, textStream, done)

	// Финальный транскрипт (фиксирует lastOriginal).
	textStream <- common.STTEvent{
		Event:     common.EventEndOfTurn,
		ChannelID: "speaker",
		Text:      "Hello world",
		Timestamp: time.Now(),
	}
	time.Sleep(20 * time.Millisecond)

	// Перевод — запускает асинхронную LogTranslation.
	textStream <- common.STTEvent{
		Event:     common.EventEndOfTurn,
		ChannelID: "translation",
		Text:      "Привет мир",
		Timestamp: time.Now(),
	}

	// Даём горутине логирования стартовать.
	time.Sleep(20 * time.Millisecond)

	// Закрываем textStream — dispatcher должен дождаться лог-горутины.
	close(textStream)

	// Ждём завершения dispatcher.
	select {
	case <-done:
		// OK — dispatcher завершился.
	case <-time.After(2 * time.Second):
		t.Fatal("dispatcher не завершился после закрытия textStream")
	}

	// Проверяем: LogTranslation была вызвана до сигнала done
	// (т.е. горутина логирования завершилась до выхода из Run).
	transLogs := logger.Translations()
	if len(transLogs) != 1 {
		t.Fatalf("ожидалась 1 запись перевода в лог, получено %d", len(transLogs))
	}
	if transLogs[0].Text != "Hello world" {
		t.Errorf("оригинал в логе: %q", transLogs[0].Text)
	}

	// Проверяем, что debug-лог перевода тоже есть.
	if !logger.HasDebug("перевод получен") {
		t.Error("ожидался debug-лог о получении перевода")
	}
}

// TestDispatcherCandidateContextFn — CandidateContextFn используется вместо
// legacy CandidateContext string при генерации ответа.
func TestDispatcherCandidateContextFn(t *testing.T) {
	engine := &mockEngine{answers: []string{"ответ"}}
	overlay := &mockOverlay{}
	logger := &mockLogger{}
	cfg := DefaultConfig()
	cfg.AnswerQueueSize = -1
	cfg.CandidateContext = "legacy should be ignored"
	cfg.CandidateContextFn = func(question string) string {
		return "FN:" + question
	}
	d := New(overlay, engine, logger, cfg)

	d.GenerateAnswers("What is Redis?")

	reqs := engine.Reqs()
	if len(reqs) != 1 {
		t.Fatalf("ожидался 1 запрос, получено %d", len(reqs))
	}
	if reqs[0].CandidateContext != "FN:What is Redis?" {
		t.Errorf("CandidateContext: got %q, want %q", reqs[0].CandidateContext, "FN:What is Redis?")
	}
}

// TestDispatcherCandidateContextLegacy — без CandidateContextFn используется
// legacy CandidateContext string (обратная совместимость).
func TestDispatcherCandidateContextLegacy(t *testing.T) {
	engine := &mockEngine{answers: []string{"ответ"}}
	overlay := &mockOverlay{}
	logger := &mockLogger{}
	cfg := DefaultConfig()
	cfg.AnswerQueueSize = -1
	cfg.CandidateContext = "legacy context"
	d := New(overlay, engine, logger, cfg)

	d.GenerateAnswers("What is Redis?")

	reqs := engine.Reqs()
	if len(reqs) != 1 {
		t.Fatalf("ожидался 1 запрос, получено %d", len(reqs))
	}
	if reqs[0].CandidateContext != "legacy context" {
		t.Errorf("CandidateContext: got %q, want %q", reqs[0].CandidateContext, "legacy context")
	}
}

// ── Вспомогательные моки ──────────────────────────────────────────────

// mockErrorLogger — логгер, возвращающий ошибку из LogTranslation.
type mockErrorLogger struct {
	mockLogger
	transErr error
}

func (m *mockErrorLogger) LogTranslation(event common.STTEvent, translation string, answers []string) error {
	m.mockLogger.LogTranslation(event, translation, answers)
	return m.transErr
}

func stringsContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
