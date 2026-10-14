package dispatcher

import (
	"context"
	"fmt"
	"strings"
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

	// SourceLang/TargetLang — пара языков подсказок (ISO 639-1, default en/ru).
	SourceLang string
	TargetLang string

	// CandidateContext — постоянные факты кандидата (база CV).
	CandidateContext string

	// CandidateContextFn — функция точечного retrieval candidate context по
	// вопросу (fact-level). Если задана, используется вместо CandidateContext.
	CandidateContextFn func(question string) string

	// RecentTurns — максимум turns в conversation context (default 6).
	RecentTurns int

	// MaxContextTokens — лимит размера conversation context (default 4000).
	MaxContextTokens int

	// HotkeyBufferSize — буфер канала hotkey-действий F1–F3 (default 16).
	HotkeyBufferSize int

	// AutoAnswer включает авто-генерацию ответа сразу при обнаружении вопроса.
	// nil трактуется как true (сохранение прежнего поведения: вопрос →
	// немедленная генерация). false — вопрос лишь создаёт метку в таймлайне,
	// ответ генерируется по F1. Указатель, а не bool: ноль-значение bool не
	// позволяет отличить «не задано» от «явно выключено», а сохранение
	// авто-ответа по умолчанию важнее, чем удобство литерала.
	AutoAnswer *bool
}

func DefaultConfig() Config {
	return Config{
		AnswerTimeout:    10 * time.Second,
		AnswerQueueSize:  16,
		RecentTurns:      6,
		MaxContextTokens: 4000,
		HotkeyBufferSize: 16,
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

	// Очередь заданий на генерацию (последовательная обработка одним consumer'ом).
	answerCh   chan answerTask
	answerDone chan struct{}

	// Канал hotkey-действий F1–F3 (Demand/Prev/Next) и канал отмены (Esc).
	hotkeyCh chan HotkeyAction
	cancelCh chan struct{}

	// История текущего интервью и база CV.
	history            *translator.ConversationHistory
	candidateContext   string
	candidateContextFn func(string) string

	// Пара языков подсказок (ISO 639-1) — для формата промпта и разбора ответа.
	srcLang string
	tgtLang string

	// timeline — таймлайн обнаруженных вопросов (метки + ответы + навигация).
	timeline *QuestionTimeline

	// autoAnswer — авто-генерация ответа при обнаружении вопроса (default true).
	autoAnswer bool

	// busy — true, пока worker выполняет генерацию. F1, пришедший во время
	// генерации, дропается в HandleDemand (коалесинг), не скапливаясь в очереди.
	busy atomic.Bool

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
	if cfg.HotkeyBufferSize <= 0 {
		cfg.HotkeyBufferSize = 16
	}
	if cfg.SourceLang == "" {
		cfg.SourceLang = translator.DefaultSourceLang
	}
	if cfg.TargetLang == "" {
		cfg.TargetLang = translator.DefaultTargetLang
	}

	// AutoAnswer=nil → true (прежнее поведение: авто-генерация при вопросе).
	autoAnswer := cfg.AutoAnswer == nil || *cfg.AutoAnswer

	answerCh := make(chan answerTask, cfg.AnswerQueueSize)

	return &Dispatcher{
		overlay:            overlay,
		engine:             engine,
		sessLog:            sessLog,
		cfg:                cfg,
		answerCh:           answerCh,
		answerDone:         make(chan struct{}),
		hotkeyCh:           make(chan HotkeyAction, cfg.HotkeyBufferSize),
		cancelCh:           make(chan struct{}, 1),
		history:            translator.NewConversationHistory(cfg.RecentTurns, cfg.MaxContextTokens),
		candidateContext:   cfg.CandidateContext,
		candidateContextFn: cfg.CandidateContextFn,
		srcLang:            cfg.SourceLang,
		tgtLang:            cfg.TargetLang,
		timeline:           NewQuestionTimeline(),
		autoAnswer:         autoAnswer,
	}
}

// answerTask — задание на генерацию подсказок для конкретной метки таймлайна.
// question — снимок текста вопроса на момент постановки: к моменту обработки
// активной меткой может быть уже другая, а задание продолжает относиться к своей
// метке (idx) и своему тексту. fresh=true — задание создано авто-генерацией при
// обнаружении нового вопроса (подлежит дедупу по тексту); false — задание по
// Demand/regeneration (дедуп не применяется).
type answerTask struct {
	idx      int
	cmd      translator.GenerationCommand
	question string
	fresh    bool
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

		// Финальная (полная/исправленная) версия фразы заменяет последний
		// partial в зоне 1 — фраза выведена целиком до начала следующей
		// (механизм замены Interim уже есть в AddMessage). Зона 1 не очищается.
		d.overlay.AddMessage(ui.UIMessage{
			Type:      ui.Interim,
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

// enqueueQuestion регистрирует вопрос в таймлайне и, при включённом
// AutoAnswer, ставит задание на генерацию (неблокирующе). Возвращает индекс
// метки. Если AutoAnswer выключен, метка остаётся в QIdle — ответ по F1.
func (d *Dispatcher) enqueueQuestion(question string) int {
	// Новый вопрос сбрасывает отмену Esc — очередь снова активна.
	d.cancelled.Store(false)

	idx := d.timeline.Add(question)

	// Task 1: шапка метки уходит в overlay сразу, ДО генерации — иначе метка
	// невидима, пока LLM молчит. Пуш идёт ПОСЛЕ перевода метки в очередь, чтобы
	// шапка несла State=queued (при tail-follow — каркасная шапка активной
	// метки; при просмотре истории — шапка остаётся на метке пользователя,
	// новая только помечается fresh).
	if d.autoAnswer {
		d.timeline.SetState(idx, QQueued)
		// Первый ответ всегда CommandAnswer, но Step метки продвигается вперёд:
		// следующий F1 после готового авто-ответа сразу даёт CommandMoreContext.
		d.timeline.NextStep(idx)
		d.timeline.SetMode(idx, modeAnswer)
	}

	d.pushHeaderForState(idx)

	if !d.autoAnswer {
		return idx
	}

	d.enqueueAnswer(answerTask{idx: idx, cmd: translator.CommandAnswer, question: question, fresh: true})
	return idx
}

// pushHeaderForState отправляет шапку после изменения состояния метки: при
// tail-follow — шапку самой метки idx (её новое состояние уже видно в
// таймлайне), иначе — шапку активной метки пользователя (idx только помечена
// fresh-индикатором).
func (d *Dispatcher) pushHeaderForState(idx int) {
	if d.timeline.OnTail() {
		d.pushHeader(idx, nil)
		return
	}
	d.pushActiveHeader()
}

// enqueueAnswer кладёт задание на генерацию в очередь (неблокирующе).
// При переполнении задание дропается с логом, состояние метки возвращается в QIdle.
func (d *Dispatcher) enqueueAnswer(task answerTask) {
	select {
	case d.answerCh <- task:
		// OK
	default:
		d.timeline.SetState(task.idx, QIdle)
		if d.sessLog != nil {
			d.sessLog.LogDebug(fmt.Sprintf("dispatcher: очередь подсказок переполнена, вопрос пропущен: text=%v", task.question))
		}
	}
}

// answerWorker — единственная горутина, последовательно обрабатывающая задания
// генерации, hotkey-действия (F1–F3) и отмену (Esc).
// Дедупликация: задание с тем же текстом вопроса, что и предыдущее, пропускается.
func (d *Dispatcher) answerWorker(ctx context.Context) {
	defer close(d.answerDone)

	var lastQuestion string

	for {
		select {
		case <-ctx.Done():
			// Дренируем оставшиеся задания перед выходом.
			d.drainQueue(&lastQuestion)
			return

		case task, ok := <-d.answerCh:
			if !ok {
				return
			}
			// Очередь отменена (Esc) — дропаем оставшиеся задания.
			if d.cancelled.Load() {
				d.timeline.SetState(task.idx, QIdle)
				continue
			}
			// Дедупликация: пропускаем точный повтор предыдущего вопроса,
			// но только для авто-заданий (fresh). Regeneration по F1 не дедупим.
			if task.fresh && task.question == lastQuestion {
				if d.sessLog != nil {
					d.sessLog.LogDebug(fmt.Sprintf("dispatcher: дубликат вопроса пропущен: text=%v", task.question))
				}
				continue
			}
			lastQuestion = task.question
			d.runAnswer(task)

		case act := <-d.hotkeyCh:
			d.handleHotkeyAction(act)

		case <-d.cancelCh:
			// Esc: очищаем очередь — следующие вопросы не уйдут в LLM.
			if d.sessLog != nil {
				d.sessLog.LogDebug("dispatcher: очередь отменена (Esc)")
			}
			d.dropQueue()
			// Метки в Queued/Generating возвращаем в Idle; готовые сохраняем.
			d.timeline.ResetPending()
		}
	}
}

// handleHotkeyAction обрабатывает hotkey-действие в контексте таймлайна.
// Prev/Next только двигают активную позицию и обновляют шапку; Demand (F1)
// ставит задание на генерацию для активной метки (коалесинг: во время генерации
// повторный F1 игнорируется).
func (d *Dispatcher) handleHotkeyAction(act HotkeyAction) {
	switch act {
	case ActionPrev:
		if !d.timeline.Prev() {
			return
		}
		d.pushActiveHeader()
	case ActionNext:
		if !d.timeline.Next() {
			return
		}
		d.pushActiveHeader()
	case ActionDemand:
		d.handleDemand()
	}
}

// handleDemand реализует F1: генерация ответа для активной метки.
//   - пустой таймлайн → no-op с логом;
//   - метка уже в очереди/генерируется → игнор (коалесинг повторного F1);
//   - метка QIdle → первая генерация (всегда CommandAnswer, Step не трогаем);
//   - метка QReady/QFailed → следующий шаг цикла (more context → simpler → ...).
func (d *Dispatcher) handleDemand() {
	// Task 4: F1 после Esc снова включает очередь — иначе задание, поставленное
	// этим F1, молча дропается воркером (cancelled=true) и метка залипает в
	// QQueued. Сброс здесь, а не в HandleDemand: только осознанный запрос
	// пользователя реактивирует очередь (сброс в answerWorker бессмысленен —
	// он читает флаг до вызова handleDemand).
	d.cancelled.Store(false)

	idx, entry, ok := d.timeline.Active()
	if !ok {
		if d.sessLog != nil {
			d.sessLog.LogDebug("dispatcher: F1 без активной метки пропущен")
		}
		return
	}

	switch entry.State {
	case QQueued, QGenerating:
		if d.sessLog != nil {
			d.sessLog.LogDebug(fmt.Sprintf("dispatcher: F1 во время генерации пропущен: question=%v, state=%v", entry.Question, entry.State.String()))
		}
		return
	case QIdle:
		// Первый ответ на метку — всегда CommandAnswer, но Step продвигаем:
		// следующий F1 даст CommandMoreContext.
		d.timeline.NextStep(idx)
		d.timeline.SetState(idx, QQueued)
		d.timeline.SetMode(idx, modeAnswer)
		d.enqueueAnswer(answerTask{idx: idx, cmd: translator.CommandAnswer, question: entry.Question})
	default:
		// QReady/QFailed: следующий режим цикла (answer→more context→simpler).
		cmd, _ := d.timeline.NextStep(idx)
		d.timeline.SetState(idx, QQueued)
		d.timeline.SetMode(idx, modeOf(cmd))
		d.enqueueAnswer(answerTask{idx: idx, cmd: cmd, question: entry.Question})
	}
}

// runAnswer выполняет задание генерации для метки task.idx.
func (d *Dispatcher) runAnswer(task answerTask) {
	d.busy.Store(true)
	defer d.busy.Store(false)
	d.timeline.SetState(task.idx, QGenerating)
	d.generateAnswers(task.question, task.cmd, task.idx)
}

// drainQueue вычитывает все оставшиеся задания из очереди при shutdown.
// Генерирует только те, что ещё можно поставить в очередь (QIdle/QFailed →
// попытка ответить; QQueued переводится в QGenerating перед генерацией).
func (d *Dispatcher) drainQueue(lastQuestion *string) {
	if d.answerCh == nil {
		return
	}
	for {
		select {
		case task, ok := <-d.answerCh:
			if !ok {
				return
			}
			if d.cancelled.Load() {
				d.timeline.SetState(task.idx, QIdle)
				continue
			}
			if task.fresh && task.question == *lastQuestion {
				continue
			}
			*lastQuestion = task.question
			d.runAnswer(task)
		default:
			return
		}
	}
}

// dropQueue выбрасывает все ожидающие задания из очереди (без генерации).
func (d *Dispatcher) dropQueue() {
	if d.answerCh == nil {
		return
	}
	for {
		select {
		case task := <-d.answerCh:
			d.timeline.SetState(task.idx, QIdle)
		default:
			return
		}
	}
}

// GenerateAnswers запускает обычную генерацию ответа на заданный вопрос (F1/авто).
// Добавляет метку в таймлайн и отвечает на неё — используется при shutdown
// (drainQueue) и в тестах.
func (d *Dispatcher) GenerateAnswers(question string) {
	idx := d.timeline.Add(question)
	d.timeline.SetState(idx, QGenerating)
	d.generateAnswers(question, translator.CommandAnswer, idx)
}

// HandleDemand — F1: запросить ответ для активной метки (неблокирующе).
// Коалесинг: если worker сейчас генерирует ответ (busy), запрос дропается —
// чтобы во время долгой генерации не накапливались лишние задания.
func (d *Dispatcher) HandleDemand() {
	if d.busy.Load() {
		if d.sessLog != nil {
			d.sessLog.LogDebug("dispatcher: F1 во время генерации пропущен (коалесинг)")
		}
		return
	}
	d.sendHotkey(ActionDemand)
}

// HandlePrev — F2: перейти к предыдущей метке таймлайна.
// Коалесинг: во время генерации (busy) действие дропается — шапку диктует
// генерация, а сдвиг метки под ней привёл бы к рассинхрону шапки и ответа.
func (d *Dispatcher) HandlePrev() {
	if d.busy.Load() {
		if d.sessLog != nil {
			d.sessLog.LogDebug("dispatcher: F2 во время генерации пропущен (коалесинг)")
		}
		return
	}
	d.sendHotkey(ActionPrev)
}

// HandleNext — F3: перейти к следующей метке таймлайна.
// Коалесинг: см. HandlePrev.
func (d *Dispatcher) HandleNext() {
	if d.busy.Load() {
		if d.sessLog != nil {
			d.sessLog.LogDebug("dispatcher: F3 во время генерации пропущен (коалесинг)")
		}
		return
	}
	d.sendHotkey(ActionNext)
}

// sendHotkey отправляет hotkey-действие в worker (неблокирующе).
func (d *Dispatcher) sendHotkey(act HotkeyAction) {
	select {
	case d.hotkeyCh <- act:
	default:
		if d.sessLog != nil {
			d.sessLog.LogDebug(fmt.Sprintf("dispatcher: очередь hotkey переполнена, действие пропущено: action=%v", act))
		}
	}
}

// Cancel отменяет активную генерацию и очищает очередь заданий (Esc).
// Метки и уже готовые ответы сохраняются: QQueued/QGenerating → QIdle.
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

// generateAnswers собирает AnswerRequest (candidate context + conversation
// context + команда) и выполняет синхронную генерацию для метки idx.
// Активная генерация может быть отменена через Cancel() (Esc).
//
// Результат кладётся в таймлайн (SetAnswer/SetState) и в overlay — но только
// если метка idx всё ещё активна (иначе ответ устаревший: пользователь ушёл на
// другую метку, и перезаписывать её шапку нельзя). RecordAnswer выполняется
// только для хвостовой метки, иначе хронология истории интервью ломается.
func (d *Dispatcher) generateAnswers(question string, cmd translator.GenerationCommand, idx int) {
	if d.engine == nil {
		if d.sessLog != nil {
			d.sessLog.LogDebug(fmt.Sprintf("dispatcher: engine=nil, генерация пропущена: question=%v", question))
		}
		d.timeline.SetState(idx, QFailed)
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
		SourceLang:          d.srcLang,
		TargetLang:          d.tgtLang,
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
		// Отменено через Esc / истёк таймаут — тихо, без ошибки в UI.
		if ansCtx.Err() != nil {
			if d.sessLog != nil {
				d.sessLog.LogDebug(fmt.Sprintf("dispatcher: генерация отменена (Esc): question=%v", question))
			}
			d.timeline.SetState(idx, QIdle)
			return
		}
		if d.sessLog != nil {
			d.sessLog.LogDebug(fmt.Sprintf("dispatcher: генерация подсказок не удалась: question=%v, error=%v", question, err))
		}
		d.timeline.SetState(idx, QFailed)
		if d.overlay != nil {
			d.overlay.AddMessage(ui.UIMessage{
				Type:      ui.Error,
				Text:      fmt.Sprintf("%v", err),
				Timestamp: time.Now(),
			})
		}
		return
	}

	if len(answers) == 0 {
		if d.sessLog != nil {
			d.sessLog.LogDebug(fmt.Sprintf("dispatcher: подсказки пусты: question=%v", question))
		}
		d.timeline.SetState(idx, QFailed)
		return
	}

	// Парсим сырые строки LLM в структурированные пары ДО добавления в UI.
	// Непарсящиеся строки сохраняются целиком как Source (текст не теряем).
	parsed := d.parseAnswers(answers)

	d.timeline.SetAnswer(idx, parsed)
	d.timeline.SetState(idx, QReady)
	d.timeline.SetMode(idx, modeOf(cmd))

	if d.sessLog != nil {
		d.sessLog.LogDebug(fmt.Sprintf("dispatcher: подсказки сгенерированы: question=%v, command=%v, count=%v", question, cmd, len(parsed)))
	}

	// UI-шапка: отправляем, только если метка idx всё ещё активна — иначе
	// устаревший ответ не должен перезаписывать шапку текущей метки.
	if d.isActive(idx) {
		d.pushHeader(idx, parsed)
	}

	// Сохраняем финальный ответ в историю только для хвостовой метки, иначе
	// хронология turns в conversation context ломается (ответ на старый вопрос
	// не должен стать последним turn'ом интервью).
	if d.timeline.OnTail() {
		d.history.RecordAnswer(question, parsed[0].Source)
	}
}

// modeOf отображает команду генерации в строковый режим для UI.
func modeOf(cmd translator.GenerationCommand) string {
	switch cmd {
	case translator.CommandMoreContext:
		return modeMoreContext
	case translator.CommandSimplerEnglish:
		return modeSimpler
	default:
		return modeAnswer
	}
}

// Режимы генерации для шапки метки (ui.Answer.Mode).
const (
	modeAnswer      = "answer"
	modeMoreContext = "more context"
	modeSimpler     = "simpler"
)

// isActive сообщает, является ли метка idx активной в таймлайне.
func (d *Dispatcher) isActive(idx int) bool {
	cur, _, ok := d.timeline.Active()
	return ok && cur == idx
}

// pushActiveHeader отправляет в overlay шапку активной метки (для Prev/Next):
// готовый ответ из таймлайна, если он есть, иначе пустые Answers с её State
// (UI покажет «F1 — ответить»).
func (d *Dispatcher) pushActiveHeader() {
	idx, entry, ok := d.timeline.Active()
	if !ok {
		return
	}
	d.pushHeader(idx, entry.Answer)
}

// pushHeader формирует и отправляет в overlay сообщение шапки метки idx.
// Заполняет QuestionIdx/Total/QuestionText/State/Mode по актуальному состоянию
// таймлайна; Mode берётся из метки (задаётся при постановке задания/ответе).
//
// Если у метки ещё нет ответа (Idle/Queued/Generating/Failed), отправляется
// один «каркасный» Answer с пустым Source/Target — он несёт метаданные
// (QuestionIdx/State/Mode), по которым UI рисует шапку и подсказку «F1 — ответить».
func (d *Dispatcher) pushHeader(idx int, answers []ui.Answer) {
	if d.overlay == nil {
		return
	}
	// Метка может быть неактивной (устаревший ответ) — берём её из снапшота.
	// Снапшот — окно кольца: Idx каждой метки абсолютный, поэтому ищем по
	// абсолютному idx (вытесненная метка не найдётся — шапка не отправляется).
	snap := d.timeline.Snapshot()
	var entry QEntry
	found := false
	for i := range snap {
		if snap[i].Idx == idx {
			entry = snap[i]
			found = true
			break
		}
	}
	if !found {
		return
	}

	header := make([]ui.Answer, len(answers))
	copy(header, answers)
	if len(header) == 0 {
		// Каркасный элемент: метаданные шапки без текста ответа.
		header = []ui.Answer{{}}
	}
	for i := range header {
		header[i].QuestionIdx = idx
		header[i].Total = len(snap)
		header[i].QuestionText = entry.Question
		header[i].State = entry.State.String()
		header[i].Mode = entry.Mode
	}

	d.overlay.AddMessage(ui.UIMessage{
		Type:      ui.AnswerCandidates,
		Text:      entry.Question,
		Answers:   header,
		Timestamp: time.Now(),
	})
}

// parseAnswers превращает сырые строки ответа LLM в структурированные пары
// Source/Target для настроенной пары языков. Строка без разделителя с целевым
// тегом сохраняется целиком как Source (текст никогда не теряется).
func (d *Dispatcher) parseAnswers(raw []string) []ui.Answer {
	srcTag := strings.ToUpper(d.srcLang)
	tgtTag := strings.ToUpper(d.tgtLang)

	out := make([]ui.Answer, 0, len(raw))
	for _, s := range raw {
		ans, ok := translator.ParseAnswer(s, srcTag, tgtTag)
		if !ok {
			// Нет целевой части — оставляем исходную строку целиком.
			ans = ui.Answer{Source: strings.TrimSpace(s)}
		}
		out = append(out, ans)
	}
	return out
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
