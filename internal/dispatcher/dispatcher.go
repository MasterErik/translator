package dispatcher

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mastererik/translator/internal/common"
	"github.com/mastererik/translator/internal/translator"
	"github.com/mastererik/translator/internal/ui"
)

// OverlayUI — минимальный UI-интерфейс для диспетчера.
type OverlayUI interface {
	AddMessage(msg ui.UIMessage)
}

// AnswerGenerator — генератор подсказок (LLM).
type AnswerGenerator interface {
	GenerateAnswers(ctx context.Context, req translator.AnswerRequest) ([]string, error)
}

// SessionLogger — логирование событий диспетчера.
type SessionLogger interface {
	LogTranslation(event common.STTEvent, translation string, answers []string) error
	LogDebug(msg string) error
}

type Config struct {
	AnswerTimeout time.Duration

	// AnswerQueueSize — размер буфера очереди вопросов (default 16).
	AnswerQueueSize int

	// CandidateContext — постоянные факты кандидата (база CV).
	CandidateContext string

	// CandidateContextFn — функция точечного retrieval candidate context по
	// вопросу (fact-level). Если задана, используется вместо CandidateContext.
	CandidateContextFn func(question string) string

	// RecentTurns — максимум turns в conversation context (default 6).
	RecentTurns int

	// MaxContextTokens — лимит размера conversation context (default 4000).
	MaxContextTokens int

	// CommandBufferSize — буфер канала команд F1–F4 (default 16).
	CommandBufferSize int
}

func DefaultConfig() Config {
	return Config{
		AnswerTimeout:     10 * time.Second,
		AnswerQueueSize:   16,
		RecentTurns:       6,
		MaxContextTokens:  4000,
		CommandBufferSize: 16,
	}
}

type Dispatcher struct {
	overlay OverlayUI
	engine  AnswerGenerator
	sessLog SessionLogger

	cfg Config

	dropCount    atomic.Int64
	lastDropWarn time.Time
	dropMu       sync.Mutex

	// Очередь вопросов для последовательной обработки одним consumer'ом.
	answerCh   chan string
	answerDone chan struct{}

	// Канал команд F1–F4 и канал отмены (Esc).
	commandCh chan translator.GenerationCommand
	cancelCh  chan struct{}

	// История текущего интервью и база CV.
	history            *translator.ConversationHistory
	candidateContext   string
	candidateContextFn func(string) string

	// Текущий обнаруженный вопрос (для F1–F4 regeneration).
	currentQuestion string
	currentMu       sync.Mutex

	// Отмена активной генерации (Esc).
	activeCancel context.CancelFunc
	activeMu     sync.Mutex

	// cancelled — очередь отменена (Esc). Пока true, вопросы из answerCh дропаются.
	cancelled atomic.Bool

	// logWg отслеживает асинхронные горутины логирования перевода,
	// чтобы гарантировать их завершение перед закрытием логгера.
	logWg sync.WaitGroup
}

func New(overlay OverlayUI, engine AnswerGenerator, sessLog SessionLogger, cfg Config) *Dispatcher {
	if cfg.AnswerTimeout <= 0 {
		cfg.AnswerTimeout = 10 * time.Second
	}
	if cfg.AnswerQueueSize <= 0 {
		cfg.AnswerQueueSize = 16
	}
	if cfg.RecentTurns <= 0 {
		cfg.RecentTurns = 6
	}
	if cfg.MaxContextTokens <= 0 {
		cfg.MaxContextTokens = 4000
	}
	if cfg.CommandBufferSize <= 0 {
		cfg.CommandBufferSize = 16
	}

	answerCh := make(chan string, cfg.AnswerQueueSize)

	return &Dispatcher{
		overlay:            overlay,
		engine:             engine,
		sessLog:            sessLog,
		cfg:                cfg,
		answerCh:           answerCh,
		answerDone:         make(chan struct{}),
		commandCh:          make(chan translator.GenerationCommand, cfg.CommandBufferSize),
		cancelCh:           make(chan struct{}, 1),
		history:            translator.NewConversationHistory(cfg.RecentTurns, cfg.MaxContextTokens),
		candidateContext:   cfg.CandidateContext,
		candidateContextFn: cfg.CandidateContextFn,
	}
}

func (d *Dispatcher) Run(ctx context.Context, textStream <-chan common.STTEvent, done chan<- struct{}) {
	if d.sessLog != nil {
		d.sessLog.LogDebug("dispatcher: запущен")
	}

	// Внутренний контекст worker'а: отменяется при завершении Run, чтобы
	// answerWorker гарантированно вышел (даже если textStream закрыт без отмены ctx).
	workerCtx, workerCancel := context.WithCancel(ctx)

	// Один consumer обрабатывает очередь вопросов, команды F1–F4 и отмену.
	go d.answerWorker(workerCtx)

	defer func() {
		// Ждём завершения всех горутин логирования перевода.
		d.logWg.Wait()

		// Ждём завершения answerWorker, потом закрываем канал.
		<-d.answerDone
		close(d.answerCh)

		if done != nil {
			select {
			case done <- struct{}{}:
			default:
			}
		}
		if d.sessLog != nil {
			d.sessLog.LogDebug("dispatcher: завершён")
		}
	}()

	// LIFO: workerCancel выполнится ПЕРЕД ожиданием answerDone выше.
	defer workerCancel()

	var lastOriginal common.STTEvent

	for {
		select {
		case <-ctx.Done():
			if d.sessLog != nil {
				d.sessLog.LogDebug("dispatcher: контекст отменён, завершение")
			}
			return

		case event, ok := <-textStream:
			if !ok {
				if d.sessLog != nil {
					d.sessLog.LogDebug("dispatcher: textStream закрыт, завершение")
				}
				return
			}

			d.route(event, &lastOriginal)
		}
	}
}

func (d *Dispatcher) route(event common.STTEvent, lastOriginal *common.STTEvent) {
	switch {
	case event.ChannelID == "translation":
		// UI — мгновенно, не ждём логи.
		d.overlay.AddMessage(ui.UIMessage{
			Type:      ui.Translation,
			Text:      event.Text,
			Timestamp: event.Timestamp,
			MsgStatus: "done",
		})

		// Логирование — асинхронно, не блокирует dispatcher.
		if d.sessLog != nil {
			origText := lastOriginal.Text
			transText := event.Text
			logEvent := *lastOriginal
			logEvent.Timestamp = time.Now()
			sessLog := d.sessLog
			d.logWg.Add(1)
			go func() {
				defer d.logWg.Done()
				sessLog.LogDebug(fmt.Sprintf("dispatcher: перевод получен: original=%v, translation=%v", origText, transText))
				if err := sessLog.LogTranslation(logEvent, transText, nil); err != nil {
					sessLog.LogDebug(fmt.Sprintf("dispatcher: ошибка логирования перевода: error=%v", err))
				}
			}()
		}

	case event.Event != common.EventEndOfTurn:
		d.overlay.AddMessage(ui.UIMessage{
			Type:      ui.Interim,
			Text:      event.Text,
			Timestamp: event.Timestamp,
		})

	default:
		*lastOriginal = event
		if d.sessLog != nil {
			d.sessLog.LogDebug(fmt.Sprintf("dispatcher: финальный транскрипт: text=%v", event.Text))
		}

		d.overlay.AddMessage(ui.UIMessage{
			Type:      ui.History,
			Text:      event.Text,
			Timestamp: event.Timestamp,
		})

		if translator.IsQuestion(event.Text) {
			if d.sessLog != nil {
				d.sessLog.LogDebug(fmt.Sprintf("dispatcher: обнаружен вопрос, запуск генерации: text=%v", event.Text))
			}
			d.enqueueQuestion(event.Text)
		}
	}
}

// enqueueQuestion отправляет вопрос в очередь на обработку (неблокирующе).
func (d *Dispatcher) enqueueQuestion(question string) {
	// Новый вопрос сбрасывает отмену Esc — очередь снова активна.
	d.cancelled.Store(false)
	d.setCurrentQuestion(question)

	select {
	case d.answerCh <- question:
		// OK
	default:
		if d.sessLog != nil {
			d.sessLog.LogDebug(fmt.Sprintf("dispatcher: очередь подсказок переполнена, вопрос пропущен: text=%v", question))
		}
	}
}

// answerWorker — единственная горутина, последовательно обрабатывающая очередь
// вопросов, команды F1–F4 и отмену (Esc).
// Дедупликация: повторяющийся подряд вопрос пропускается.
func (d *Dispatcher) answerWorker(ctx context.Context) {
	defer close(d.answerDone)

	var lastQuestion string

	for {
		select {
		case <-ctx.Done():
			// Дренируем оставшиеся вопросы перед выходом.
			d.drainQueue(&lastQuestion)
			return

		case question, ok := <-d.answerCh:
			if !ok {
				return
			}
			// Очередь отменена (Esc) — дропаем оставшиеся вопросы.
			if d.cancelled.Load() {
				continue
			}
			// Дедупликация: пропускаем точный повтор предыдущего вопроса.
			if question == lastQuestion {
				if d.sessLog != nil {
					d.sessLog.LogDebug(fmt.Sprintf("dispatcher: дубликат вопроса пропущен: text=%v", question))
				}
				continue
			}
			lastQuestion = question
			d.setCurrentQuestion(question)
			d.generateAnswers(question, translator.CommandAnswer)

		case cmd := <-d.commandCh:
			// F2–F4: повторная генерация на текущий вопрос.
			q := d.currentQ()
			if q == "" {
				if d.sessLog != nil {
					d.sessLog.LogDebug(fmt.Sprintf("dispatcher: команда без текущего вопроса пропущена: cmd=%v", cmd))
				}
				continue
			}
			d.generateAnswers(q, cmd)

		case <-d.cancelCh:
			// Esc: очищаем очередь — следующие вопросы не уйдут в LLM.
			if d.sessLog != nil {
				d.sessLog.LogDebug("dispatcher: очередь отменена (Esc)")
			}
			d.dropQueue()
		}
	}
}

// drainQueue вычитывает все оставшиеся вопросы из очереди.
// Вызывается при shutdown, когда answerCh ещё открыт.
func (d *Dispatcher) drainQueue(lastQuestion *string) {
	if d.answerCh == nil {
		return
	}
	for {
		select {
		case question, ok := <-d.answerCh:
			if !ok {
				return
			}
			if question != *lastQuestion {
				d.GenerateAnswers(question)
				*lastQuestion = question
			}
		default:
			return
		}
	}
}

// dropQueue выбрасывает все ожидающие вопросы из очереди (без генерации).
func (d *Dispatcher) dropQueue() {
	if d.answerCh == nil {
		return
	}
	for {
		select {
		case <-d.answerCh:
		default:
			return
		}
	}
}

// GenerateAnswers запускает обычную генерацию ответа на заданный вопрос (F1/авто).
func (d *Dispatcher) GenerateAnswers(question string) {
	d.generateAnswers(question, translator.CommandAnswer)
}

// HandleCommand отправляет команду F1–F4 в обработку.
func (d *Dispatcher) HandleCommand(cmd translator.GenerationCommand) {
	select {
	case d.commandCh <- cmd:
	default:
		if d.sessLog != nil {
			d.sessLog.LogDebug(fmt.Sprintf("dispatcher: очередь команд переполнена, команда пропущена: cmd=%v", cmd))
		}
	}
}

// Cancel отменяет активную генерацию и очищает очередь вопросов (Esc).
func (d *Dispatcher) Cancel() {
	d.cancelled.Store(true)

	d.activeMu.Lock()
	if d.activeCancel != nil {
		d.activeCancel()
	}
	d.activeMu.Unlock()

	select {
	case d.cancelCh <- struct{}{}:
	default:
	}
}

func (d *Dispatcher) setCurrentQuestion(q string) {
	d.currentMu.Lock()
	d.currentQuestion = q
	d.currentMu.Unlock()
}

func (d *Dispatcher) currentQ() string {
	d.currentMu.Lock()
	defer d.currentMu.Unlock()
	return d.currentQuestion
}

// generateAnswers собирает AnswerRequest (candidate context + conversation
// context + команда) и выполняет синхронную генерацию. Активная генерация может
// быть отменена через Cancel() (Esc).
func (d *Dispatcher) generateAnswers(question string, cmd translator.GenerationCommand) {
	if d.engine == nil {
		if d.sessLog != nil {
			d.sessLog.LogDebug(fmt.Sprintf("dispatcher: engine=nil, генерация пропущена: question=%v", question))
		}
		return
	}

	cc := d.candidateContext
	if d.candidateContextFn != nil {
		cc = d.candidateContextFn(question)
	}

	req := translator.AnswerRequest{
		Question:            question,
		CandidateContext:    cc,
		ConversationContext: d.history.BuildContext(),
		Command:             cmd,
	}

	ansCtx, ansCancel := context.WithTimeout(context.Background(), d.cfg.AnswerTimeout)
	d.activeMu.Lock()
	d.activeCancel = ansCancel
	d.activeMu.Unlock()

	defer func() {
		ansCancel()
		d.activeMu.Lock()
		d.activeCancel = nil
		d.activeMu.Unlock()
	}()

	answers, err := d.engine.GenerateAnswers(ansCtx, req)
	if err != nil {
		// Отменено через Esc — тихо, без ошибки в UI.
		if ansCtx.Err() != nil {
			if d.sessLog != nil {
				d.sessLog.LogDebug(fmt.Sprintf("dispatcher: генерация отменена (Esc): question=%v", question))
			}
			return
		}
		if d.sessLog != nil {
			d.sessLog.LogDebug(fmt.Sprintf("dispatcher: генерация подсказок не удалась: question=%v, error=%v", question, err))
		}
		if d.overlay != nil {
			d.overlay.AddMessage(ui.UIMessage{
				Type:      ui.AnswerCandidates,
				Text:      question,
				Answers:   []string{fmt.Sprintf("\u26a0\ufe0f Ошибка: %v", err)},
				Timestamp: time.Now(),
			})
		}
		return
	}

	if len(answers) == 0 {
		if d.sessLog != nil {
			d.sessLog.LogDebug(fmt.Sprintf("dispatcher: подсказки пусты: question=%v", question))
		}
		return
	}

	if d.overlay != nil {
		d.overlay.AddMessage(ui.UIMessage{
			Type:      ui.AnswerCandidates,
			Text:      question,
			Answers:   answers,
			Timestamp: time.Now(),
		})
	}

	if d.sessLog != nil {
		d.sessLog.LogDebug(fmt.Sprintf("dispatcher: подсказки сгенерированы: question=%v, command=%v, count=%v", question, cmd, len(answers)))
	}

	// Сохраняем финальный ответ в историю. Regeneration (F2–F4) заменяет
	// последнюю версию ответа на тот же вопрос (не создаёт новый turn).
	d.history.RecordAnswer(question, answers[0])
}

func (d *Dispatcher) ReportDrop(channel string) {
	d.dropCount.Add(1)

	d.dropMu.Lock()
	now := time.Now()
	if now.Sub(d.lastDropWarn) >= 10*time.Second {
		d.lastDropWarn = now
		total := d.dropCount.Swap(0)
		if d.sessLog != nil {
			d.sessLog.LogDebug(fmt.Sprintf("dispatcher: переполнение буфера: channel=%v, drops_since_last_warn=%v", channel, total))
		}
	}
	d.dropMu.Unlock()
}

func (d *Dispatcher) DropCount() int64 {
	return d.dropCount.Load()
}
