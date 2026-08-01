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
	GenerateAnswers(ctx context.Context, question string) ([]string, error)
}

// SessionLogger — логирование событий диспетчера.
type SessionLogger interface {
	LogTranslation(event common.STTEvent, translation string, answers []string) error
	LogDebug(msg string) error
}

type Config struct {
	AnswerTimeout time.Duration

	// AnswerQueueSize — размер буфера очереди вопросов.
	// 0 = default (16). Отрицательное = неблокирующая отправка отключена
	// (возврат к старому поведению: go-горутина на каждый вопрос).
	AnswerQueueSize int
}

func DefaultConfig() Config {
	return Config{
		AnswerTimeout:   10 * time.Second,
		AnswerQueueSize: 16,
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

	// logWg отслеживает асинхронные горутины логирования перевода,
	// чтобы гарантировать их завершение перед закрытием логгера.
	logWg sync.WaitGroup
}

func New(overlay OverlayUI, engine AnswerGenerator, sessLog SessionLogger, cfg Config) *Dispatcher {
	if cfg.AnswerTimeout <= 0 {
		cfg.AnswerTimeout = 10 * time.Second
	}
	if cfg.AnswerQueueSize == 0 {
		cfg.AnswerQueueSize = 16
	}

	queueSize := cfg.AnswerQueueSize
	if queueSize < 0 {
		queueSize = 0 // отрицательное — канал не используется
	}

	var answerCh chan string
	var answerDone chan struct{}
	if queueSize > 0 {
		answerCh = make(chan string, queueSize)
		answerDone = make(chan struct{})
	}

	return &Dispatcher{
		overlay:    overlay,
		engine:     engine,
		sessLog:    sessLog,
		cfg:        cfg,
		answerCh:   answerCh,
		answerDone: answerDone,
	}
}

func (d *Dispatcher) Run(ctx context.Context, textStream <-chan common.STTEvent, done chan<- struct{}) {
	if d.sessLog != nil {
		d.sessLog.LogDebug("dispatcher: запущен")
	}

	// Запускаем одного consumer'а для последовательной обработки вопросов.
	if d.answerCh != nil {
		go d.answerWorker(ctx)
	}

	defer func() {
		// Ждём завершения всех горутин логирования перевода.
		d.logWg.Wait()

		// Ждём завершения answerWorker, потом закрываем канал.
		if d.answerCh != nil {
			<-d.answerDone
			close(d.answerCh)
		}

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

	var lastOriginal common.STTEvent
	historySeen := map[string]bool{}

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

			d.route(event, &lastOriginal, historySeen)
		}
	}
}

func (d *Dispatcher) route(event common.STTEvent, lastOriginal *common.STTEvent, historySeen map[string]bool) {
	switch {
	case event.ChannelID == "translation":
		// UI — мгновенно, не ждём логи.
		d.overlay.AddMessage(ui.UIMessage{
			Type:      ui.Translation,
			Text:      event.Text,
			Timestamp: event.Timestamp,
			MsgStatus: "done",
		})

		if !historySeen[lastOriginal.Text] {
			historySeen[lastOriginal.Text] = true
			d.overlay.AddMessage(ui.UIMessage{
				Type:        ui.History,
				Text:        lastOriginal.Text,
				Translation: event.Text,
				Timestamp:   event.Timestamp,
			})
		}

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

		if translator.IsQuestion(event.Text) {
			if d.sessLog != nil {
				d.sessLog.LogDebug(fmt.Sprintf("dispatcher: обнаружен вопрос, запуск генерации: text=%v", event.Text))
			}
			d.enqueueQuestion(event.Text)
		}
	}
}

// enqueueQuestion отправляет вопрос в очередь на обработку.
// Если очередь не настроена (answerCh == nil) — запускает горутину (старое поведение).
// Иначе — неблокирующая отправка в буферизованный канал.
func (d *Dispatcher) enqueueQuestion(question string) {
	if d.answerCh == nil {
		go d.GenerateAnswers(question)
		return
	}

	select {
	case d.answerCh <- question:
		// OK
	default:
		if d.sessLog != nil {
			d.sessLog.LogDebug(fmt.Sprintf("dispatcher: очередь подсказок переполнена, вопрос пропущен: text=%v", question))
		}
	}
}

// answerWorker — единственная горутина, последовательно обрабатывающая очередь вопросов.
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
			// Дедупликация: пропускаем точный повтор предыдущего вопроса.
			if question == lastQuestion {
				if d.sessLog != nil {
					d.sessLog.LogDebug(fmt.Sprintf("dispatcher: дубликат вопроса пропущен: text=%v", question))
				}
				continue
			}
			lastQuestion = question
			d.GenerateAnswers(question)
		}
	}
}

// drainQueue вычитывает все оставшиеся вопросы из очереди.
// Вызывается при shutdown, когда answerCh ещё открыт.
func (d *Dispatcher) drainQueue(lastQuestion *string) {
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

func (d *Dispatcher) GenerateAnswers(question string) {
	if d.engine == nil {
		if d.sessLog != nil {
			d.sessLog.LogDebug(fmt.Sprintf("dispatcher: engine=nil, генерация пропущена: question=%v", question))
		}
		return
	}

	ansCtx, ansCancel := context.WithTimeout(context.Background(), d.cfg.AnswerTimeout)
	defer ansCancel()

	answers, err := d.engine.GenerateAnswers(ansCtx, question)
	if err != nil {
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

	d.overlay.AddMessage(ui.UIMessage{
		Type:      ui.AnswerCandidates,
		Text:      question,
		Answers:   answers,
		Timestamp: time.Now(),
	})

	if d.sessLog != nil {
		d.sessLog.LogDebug(fmt.Sprintf("dispatcher: подсказки сгенерированы: question=%v, count=%v", question, len(answers)))
	}
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
