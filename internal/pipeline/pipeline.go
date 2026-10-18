// Package pipeline предоставляет Pipeline — центральный оркестратор жизненного
// цикла приложения Translator: захват аудио, STT, перевод и UI-оверлей.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/mastererik/translator/internal/common"
	candidatecontext "github.com/mastererik/translator/internal/context"
	"github.com/mastererik/translator/internal/dispatcher"
	"github.com/mastererik/translator/internal/hotkey"
	"github.com/mastererik/translator/internal/logger"
	"github.com/mastererik/translator/internal/stt"
	"github.com/mastererik/translator/internal/translator"
	"github.com/mastererik/translator/internal/ui"
)

// --------------------------------------------------------------------------
// Интерфейсы (ISP — определяются в пакете-потребителе)
// --------------------------------------------------------------------------

// capturer — двухканальный захват аудио (loopback + микрофон), оба 16kHz mono.
type capturer interface {
	Start(ctx context.Context) (loopback, mic <-chan []byte, err error)
}

// Overlay — UI-оверлей для вывода сообщений.
type Overlay interface {
	AddMessage(msg ui.UIMessage)
	GetMessages() []ui.UIMessage
	Run(ctx context.Context) error
	WaitShutdown()
	ToggleTranscriptionHistory()
}

// --------------------------------------------------------------------------
// Структуры
// --------------------------------------------------------------------------

// shutdownHotkeyTimeout — верхняя граница ожидания выхода hotkey.Run в shutdown.
// При штатной отмене цикл завершается мгновенно; таймаут страхует от
// потерянных Post-сообщений (WM_QUIT/wake), чтобы shutdown не завис.
const shutdownHotkeyTimeout = 2 * time.Second

// Pipeline инкапсулирует зависимости и жизненный цикл приложения:
// захват аудио, STT, перевод, UI-оверлей, логгер.
type Pipeline struct {
	cfg Config

	capturer capturer
	sttProv  stt.STTProvider
	engine   *translator.TranslationEngine
	overlay  Overlay
	sessLog  logger.SessionLogger

	textStream   chan common.STTEvent
	dispatchDone chan struct{} // Сигнал завершения dispatcher (буфер 1)
	dispatch     *dispatcher.Dispatcher

	// manualInput — канал ручного ввода текста (debug-режим). nil, если
	// DebugInput выключен: ветка select по nil-каналу блокируется вечно.
	manualInput <-chan string

	hotkeyDone chan struct{} // Закрывается при выходе runHotkeys (r3-2)
	hotkeyOnce sync.Once     // Защита от двойного close(hotkeyDone) (r3-2)
}

// Config — все настройки пайплайна (без магических чисел).
type Config struct {
	// Захват
	Capturer      capturer
	ValidateAudio bool
	LoopbackName  string
	MicName       string

	// STT (Gladia)
	GladiaAPIKey string
	SourceLang   string
	TargetLang   string

	// LLM
	LLMBaseURL string
	LLMAPIKey  string
	LLMModel   string
	MaxTokens  int

	// Candidate context (база CV) и conversation context.
	CandidateContextFile string                        // путь к файлу базы CV (legacy)
	CandidateContext     common.CandidateContextConfig // fact-level retrieval (Dir) — приоритет над File
	Hints                common.HintsConfig            // подсказки-ответы (каталог в формате candidate_context)
	Interview            common.InterviewConfig        // целевой стек интервью (D2; используется retrieval с M7b)
	RecentTurns          int                           // максимум turns в context
	MaxContextTokens     int                           // лимит размера context

	// Overlay
	OverlayCfg ui.OverlayConfig

	// Логгер
	LogDir    string
	SaveAudio bool

	// Каналы и таймауты (избавляемся от магических чисел)
	TextStreamBuffer int           // default: 64
	AnswerTimeout    time.Duration // default: 10s

	// AutoAnswer — авто-генерация ответа при обнаружении вопроса (nil → true).
	// false — вопросы только создают метки в таймлайне, ответ — по F1.
	AutoAnswer *bool

	// Debug
	SilentFrame []byte // для StubCapture

	// DebugInput — debug-режим ручного ввода текста: overlay получает канал,
	// введённые строки идут тем же путём, что и STT-события (speaker/EndOfTurn).
	DebugInput bool
}

// --------------------------------------------------------------------------
// Конструктор
// --------------------------------------------------------------------------

// New создаёт Pipeline, инициализирует и валидирует все компоненты.
func New(cfg Config) (*Pipeline, error) {
	// Применяем значения по умолчанию.
	if cfg.TextStreamBuffer <= 0 {
		cfg.TextStreamBuffer = 64
	}
	if cfg.AnswerTimeout <= 0 {
		cfg.AnswerTimeout = 10 * time.Second
	}

	// Логгер сессии — создаётся первым, чтобы все компоненты могли писать в него.
	sessLog := logger.SessionLogger(logger.NewNopSessionLogger())
	if cfg.LogDir != "" {
		var err error
		sessLog, err = logger.NewFileSessionLogger(cfg.LogDir, cfg.SaveAudio)
		if err != nil {
			return nil, fmt.Errorf("pipeline: создание логгера сессии: %w", err)
		}
	}
	// Гарантируем закрытие логгера и запись ошибки при сбое.
	var ok bool
	var newErr error
	defer func() {
		if !ok {
			if newErr != nil {
				_ = sessLog.LogDebug(fmt.Sprintf("FATAL: ошибка создания pipeline: %v", newErr))
			}
			_ = sessLog.Close()
		}
	}()

	// STT-провайдер (Gladia).
	sttProv := stt.NewGladiaProvider(stt.GladiaConfig{
		APIKey:     cfg.GladiaAPIKey,
		SourceLang: cfg.SourceLang,
		TargetLang: cfg.TargetLang,
	}, sessLog)

	// LLM-провайдер.
	llmAPIKey := cfg.LLMAPIKey
	if llmAPIKey == "" {
		newErr = fmt.Errorf("pipeline: LLMAPIKey обязателен")
		return nil, newErr
	}
	llmBaseURL := cfg.LLMBaseURL
	if llmBaseURL == "" {
		newErr = fmt.Errorf("pipeline: LLMBaseURL обязателен (например https://api.groq.com/openai/v1)")
		return nil, newErr
	}
	llmModel := cfg.LLMModel
	if llmModel == "" {
		newErr = fmt.Errorf("pipeline: LLMModel обязателен")
		return nil, newErr
	}

	llmProv := translator.NewChatProvider(llmBaseURL, llmAPIKey, llmModel)
	llmProv.SetMaxTokens(cfg.MaxTokens)

	// TranslationEngine.
	engine := translator.NewEngine(llmProv)

	// Candidate context и hints — единая сборка (M0): тот же вход, что у
	// eval/probe. Legacy-файл, fact-level retrieval и подсказки скрыты за
	// одним контрактом BuildContextFn; D14/D15 (fail vs degraded) решаются там.
	logf := func(msg string) { sessLog.LogDebug(msg) }
	contextFn, ccErr := candidatecontext.BuildContextFn(candidatecontext.ContextFnConfig{
		Dir:                 cfg.CandidateContext.Dir,
		TopK:                cfg.CandidateContext.TopK,
		MinScore:            cfg.CandidateContext.MinScore,
		MaxTokens:           cfg.CandidateContext.MaxTokens,
		MaxProfileTokens:    cfg.CandidateContext.MaxProfileTokens,
		LegacyFile:          cfg.CandidateContextFile,
		AllowLegacyFallback: cfg.CandidateContext.AllowLegacyFallback,
		HintsDir:            cfg.Hints.Dir,
		HintsTopK:           cfg.Hints.TopK,
		HintsMinScore:       cfg.Hints.MinScore,
		OnLoadError:         cfg.CandidateContext.OnLoadError,
		Logf:                logf,
	})
	if ccErr != nil {
		newErr = fmt.Errorf("pipeline: candidate context: %w", ccErr)
		return nil, newErr
	}
	// Диспетчер получает только prompt-ready строку: целевой стек сессии
	// (cfg.Interview.TargetStack) попадает в retrieval начиная с M7b.
	session := candidatecontext.SessionContext{TargetStack: cfg.Interview.TargetStack}
	candidateContextFn := func(question string) string {
		return contextFn(question, session).Text
	}

	// Overlay.
	// DebugInput прокидываем в конфиг оверлея: зона ввода и пропуск стилей
	// окна (NOACTIVATE/TRANSPARENT) решаются по OverlayConfig.DebugInput,
	// а канал ниже передаётся отдельным сеттером.
	cfg.OverlayCfg.DebugInput = cfg.DebugInput
	overlay := ui.NewOverlay(cfg.OverlayCfg, sessLog)

	// Debug-режим ручного ввода: создаём канал (владелец — pipeline, живёт до
	// конца) и отдаём его overlay. Выключен — канал остаётся nil, ветка select
	// в runSTT блокируется вечно (эквивалент отсутствия ветки).
	var manualInput <-chan string
	if cfg.DebugInput {
		manualCh := make(chan string, 8)
		overlay.SetManualInput(manualCh)
		manualInput = manualCh
	}

	// Валидация аудиоустройств (опционально).
	if cfg.ValidateAudio && cfg.Capturer != nil {
		// Проверяем, что capturer может стартовать — закрываем контекст сразу.
		validateCtx, validateCancel := context.WithCancel(context.Background())
		validateCancel()
		_, _, err := cfg.Capturer.Start(validateCtx)
		if err != nil {
			sessLog.LogDebug(fmt.Sprintf("ERROR: валидация аудио: %v", err))
			newErr = fmt.Errorf("pipeline: валидация захвата аудио: %w", err)
			return nil, newErr
		}
	}

	p := &Pipeline{
		cfg:          cfg,
		capturer:     cfg.Capturer,
		sttProv:      sttProv,
		engine:       engine,
		overlay:      overlay,
		sessLog:      sessLog,
		textStream:   make(chan common.STTEvent, cfg.TextStreamBuffer),
		manualInput:  manualInput,
		dispatchDone: make(chan struct{}, 1),
		hotkeyDone:   make(chan struct{}), // закрывается при выходе runHotkeys (r3-2)
		dispatch: dispatcher.New(
			overlay,
			engine,
			sessLog,
			dispatcher.Config{
				AnswerTimeout:      cfg.AnswerTimeout,
				AutoAnswer:         cfg.AutoAnswer,
				CandidateContextFn: candidateContextFn,
				RecentTurns:        cfg.RecentTurns,
				MaxContextTokens:   cfg.MaxContextTokens,
				SourceLang:         cfg.SourceLang,
				TargetLang:         cfg.TargetLang,
			},
		),
	}

	ok = true
	return p, nil
}

// --------------------------------------------------------------------------
// Жизненный цикл
// --------------------------------------------------------------------------

// Run запускает полный жизненный цикл пайплайна: инициализирует STT, запускает
// горутины захвата, STT, dispatch и UI-оверлея, ожидает сигнала завершения
// (SIGINT/SIGTERM) и выполняет корректный shutdown.
func (p *Pipeline) Run() error {
	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// runCtx — отменяется при SIGINT/SIGTERM (через sigCtx) или при закрытии окна.
	runCtx, runCancel := context.WithCancel(sigCtx)
	defer runCancel()

	if err := p.sttProv.Start(runCtx); err != nil {
		return fmt.Errorf("pipeline: запуск STT: %w", err)
	}
	p.sessLog.LogDebug("STT запущен")

	go p.runCapture(runCtx)
	go p.runSTT(runCtx)
	go p.dispatch.Run(runCtx, p.textStream, p.dispatchDone)
	go p.runUI(runCtx)
	go p.runHotkeys(runCtx)

	// Ждём закрытия UI-окна или SIGINT/SIGTERM.
	overlayDone := make(chan struct{})
	go func() {
		p.overlay.WaitShutdown()
		close(overlayDone)
	}()

	p.sessLog.LogDebug("все горутины запущены")
	p.sessLog.LogDebug("pipeline запущен, ожидание сигнала или закрытия окна")

	select {
	case <-sigCtx.Done():
		p.sessLog.LogDebug("получен сигнал завершения (SIGINT/SIGTERM)")
	case <-overlayDone:
		p.sessLog.LogDebug("UI-окно закрыто, запускается shutdown")
		runCancel() // отменяем runCtx → все горутины завершаются
	}
	p.shutdown()
	return nil
}

// --------------------------------------------------------------------------
// shutdown — централизованный порядок завершения компонентов
// --------------------------------------------------------------------------

// shutdown выполняет корректное завершение всех компонентов в правильном порядке:
//  1. Остановка STT-провайдера (больше не шлёт события).
//  2. Ожидание завершения runDispatch (контекст уже отменён).
//  3. Ожидание завершения UI-оверлея.
//  4. Закрытие логгера сессии.
func (p *Pipeline) shutdown() {
	// 1. STT-провайдер — закрывает WebSocket,
	//    runSTT закрывает textStream → runDispatch завершается.
	if err := p.sttProv.Stop(); err != nil {
		p.sessLog.LogDebug(fmt.Sprintf("ошибка остановки STT: err=%v", err))
	}

	// 2. Дождаться завершения runDispatch — все стриминг-горутины
	//    гарантированно завершены (включая LogTranslation).
	if p.dispatchDone != nil {
		<-p.dispatchDone
	}

	// 2b. Дождаться выхода hotkey.Run (runHotkeys закрывает hotkeyDone при выходе
	//     из Run). Закрытие идемпотентно для читателей — любой shutdown-путь
	//     увидит его сразу. Ограничиваем ожидание таймаутом: если Post-сообщения
	//     потеряны и GetMessageW не разбудился, shutdown не должен висеть вечно (r3-2).
	if p.hotkeyDone != nil {
		select {
		case <-p.hotkeyDone:
		case <-time.After(shutdownHotkeyTimeout):
			p.sessLog.LogDebug("shutdown: hotkeys не завершились за отведённый таймаут")
		}
	}

	// 3. UI-оверлей.
	p.overlay.WaitShutdown()

	// 4. Логгер сессии — только после того, как все переводы записаны.
	if p.sessLog != nil {
		if err := p.sessLog.Close(); err != nil {
			p.sessLog.LogDebug(fmt.Sprintf("ошибка закрытия логгера: err=%v", err))
		}
	}

	p.sessLog.LogDebug("shutdown завершён")
}

// --------------------------------------------------------------------------
// runCapture — захват аудио
// --------------------------------------------------------------------------

// runCapture запускает двухканальный захват аудио и направляет PCM-данные
// в аудиопоток STT-провайдера и (опционально) логгер сессии.
func (p *Pipeline) runCapture(ctx context.Context) {
	if p.capturer == nil {
		p.sessLog.LogDebug("capturer не задан, захват аудио пропущен")
		return
	}

	loopbackCh, micCh, err := p.capturer.Start(ctx)
	if err != nil {
		p.sessLog.LogDebug(fmt.Sprintf("не удалось запустить захват аудио: error=%v", err))
		return
	}

	audioStream := p.sttProv.AudioStream()

	// Микрофон идёт только в запись сессии (логгер), не в STT —
	// переводится только собеседник (loopback).
	var wg sync.WaitGroup

	if p.cfg.SaveAudio && p.sessLog != nil && micCh != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.routeRawMic(ctx, micCh)
		}()
	}

	// Направляем loopback (собеседник).
	wg.Add(1)
	go func() {
		defer wg.Done()
		p.routeAudioChannel(ctx, loopbackCh, audioStream)
	}()

	<-ctx.Done()
	wg.Wait()
	p.sessLog.LogDebug("пайплайн захвата остановлен")
}

// routeAudioChannel читает PCM-фрагменты из src и пересылает в dst (STT).
func (p *Pipeline) routeAudioChannel(ctx context.Context, src <-chan []byte, dst chan<- []byte) {
	for {
		select {
		case <-ctx.Done():
			return
		case chunk, ok := <-src:
			if !ok {
				return
			}
			select {
			case dst <- chunk:
			case <-ctx.Done():
				return
			default:
				// Буфер аудиопотока полон — пропускаем кадр.
			}
		}
	}
}

// routeRawMic читает 16kHz mono PCM с микрофона и пишет в логгер.
func (p *Pipeline) routeRawMic(ctx context.Context, src <-chan []byte) {
	for {
		select {
		case <-ctx.Done():
			return
		case chunk, ok := <-src:
			if !ok {
				return
			}
			if err := p.sessLog.SaveAudioChunk("mic", chunk); err != nil {
				p.sessLog.LogDebug(fmt.Sprintf("не удалось сохранить аудио-фрагмент: error=%v", err))
			}
		}
	}
}

// --------------------------------------------------------------------------
// runSTT — обработка транскрипций
// --------------------------------------------------------------------------

// runSTT читает транскрипции STTEvent из провайдера, логирует их асинхронно
// и маршрутизирует в textStream.
//
// Ключевое изменение: маршрутизация разделена:
//   - EventEndOfTurn (final) → блокирующая отправка (перевод не теряется).
//   - Остальные (interim) → неблокирующая (можно пропустить).
func (p *Pipeline) runSTT(ctx context.Context) {
	defer close(p.textStream)

	src := p.sttProv.TextStream()
	for {
		select {
		case <-ctx.Done():
			return
		case text := <-p.manualInput:
			// Debug-режим ручного ввода: введённый текст проходит тем же путём,
			// что и финальное STT-событие собеседника.
			event := common.STTEvent{
				Event:     common.EventEndOfTurn,
				ChannelID: "speaker",
				Text:      text,
				Timestamp: time.Now(),
			}
			if p.sessLog != nil {
				if err := p.sessLog.LogText(event); err != nil {
					p.sessLog.LogDebug(fmt.Sprintf("ошибка логирования STT: err=%v", err))
				}
			}
			p.routeSTTEvent(ctx, event)
		case event, ok := <-src:
			if !ok {
				return
			}

			if event.Error != nil {
				p.sessLog.LogDebug(fmt.Sprintf("ошибка STT-события: err=%v", event.Error))
				continue
			}

			// Логирование — синхронно (LogText неблокирующий, пишет в буферизованный writeCh).
			if p.sessLog != nil {
				if err := p.sessLog.LogText(event); err != nil {
					p.sessLog.LogDebug(fmt.Sprintf("ошибка логирования STT: err=%v", err))
				}
			}

			// Маршрутизация.
			p.routeSTTEvent(ctx, event)
		}
	}
}

// routeSTTEvent маршрутизирует STT-событие в textStream:
//   - Final (EventEndOfTurn) — блокирующая отправка с таймаутом 5s.
//   - Interim — неблокирующая отправка.
func (p *Pipeline) routeSTTEvent(ctx context.Context, event common.STTEvent) {
	if event.Event == common.EventEndOfTurn {
		// Final: блокирующая отправка с таймаутом — перевод не теряется,
		// но и не блокирует весь пайплайн при переполнении textStream.
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case p.textStream <- event:
		case <-timer.C:
			p.sessLog.LogDebug("textStream backpressure: final event dropped after 5s")
		case <-ctx.Done():
		}
	} else {
		// Interim: неблокирующая — можно пропустить.
		select {
		case p.textStream <- event:
		case <-ctx.Done():
		default:
		}
	}
}

// ── Методы-делегаты для обратной совместимости тестов ─────────────────

// runDispatch делегирует маршрутизацию в dispatcher.Dispatcher.
// Оставлен для совместимости с существующими тестами.
// Если dispatcher не задан (тесты, создающие Pipeline вручную), создаётся
// экземпляр по умолчанию.
func (p *Pipeline) runDispatch(ctx context.Context) {
	if p.dispatch == nil {
		p.dispatch = dispatcher.New(p.overlay, p.engine, p.sessLog,
			dispatcher.Config{AnswerTimeout: p.cfg.AnswerTimeout})
		if p.dispatchDone == nil {
			p.dispatchDone = make(chan struct{}, 1)
		}
	}
	p.dispatch.Run(ctx, p.textStream, p.dispatchDone)
}

// runHotkeys запускает глобальные hotkeys (F1–F4, Esc, Shift+F2/F3) и
// маршрутизирует их в dispatcher: F1–F3 → HandleDemand/Prev/Next,
// Shift+F2/F3 → HandleClarify/HandleUnclarify (уточнения, план Б-T3),
// Esc → Cancel.
//
// По завершении (в т.ч. при штатной отмене ctx) ЗАКРЫВАЕТ hotkeyDone — closure
// виден всем читателям сразу и идемпотентен для них: любой shutdown-путь (их
// несколько) дождётся выхода runHotkeys. Двойной close исключён через
// hotkeyOnce: закрытие происходит ровно здесь и ровно один раз, даже если
// runHotkeys по какой-то причине будет вызван дважды (r3-2).
// sessLog передаётся в hotkey.Run (r3-4), чтобы отказы PostThreadMessageW/
// RegisterHotKey не терялись; он nil-безопасен.
// Ошибки отмены ctx (Canceled/DeadlineExceeded) — штатное завершение, не ошибка (B-9).
func (p *Pipeline) runHotkeys(ctx context.Context) {
	defer func() {
		if p.hotkeyDone != nil {
			p.hotkeyOnce.Do(func() { close(p.hotkeyDone) })
		}
	}()

	err := hotkey.Run(ctx, p.handleHotkey, p.sessLog)
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	p.sessLog.LogDebug(fmt.Sprintf("hotkey: %v", err))
}

// handleHotkey маппит функциональную клавишу на действие диспетчера.
// Раскладка: F1 — ответить (HandleDemand), F2 — предыдущий вопрос (HandlePrev),
// F3 — следующий вопрос (HandleNext),
// Shift+F2 — добавить уточнение (HandleClarify), Shift+F3 — убрать последнее
// уточнение (HandleUnclarify), F4 — тумблер окна истории оригиналов.
// F4 работает без dispatcher и маршрутизируется прямо в overlay.
func (p *Pipeline) handleHotkey(k hotkey.Key) {
	switch k {
	case hotkey.KeyF4:
		if p.overlay != nil {
			p.overlay.ToggleTranscriptionHistory()
		}
	case hotkey.KeyF1, hotkey.KeyF2, hotkey.KeyF3, hotkey.KeyEsc,
		hotkey.KeyShiftF2, hotkey.KeyShiftF3:
		if p.dispatch == nil {
			return
		}
		switch k {
		case hotkey.KeyF1:
			p.dispatch.HandleDemand()
		case hotkey.KeyF2:
			p.dispatch.HandlePrev()
		case hotkey.KeyF3:
			p.dispatch.HandleNext()
		case hotkey.KeyShiftF2:
			p.dispatch.HandleClarify()
		case hotkey.KeyShiftF3:
			p.dispatch.HandleUnclarify()
		case hotkey.KeyEsc:
			p.dispatch.Cancel()
		}
	}
}

// --------------------------------------------------------------------------
// runUI — цикл событий оверлея
// --------------------------------------------------------------------------

// runUI запускает цикл событий оверлея и блокируется до отмены контекста
// или закрытия окна оверлея.
func (p *Pipeline) runUI(ctx context.Context) {
	p.sessLog.LogDebug("UI-оверлей запущен")
	if err := p.overlay.Run(ctx); err != nil {
		p.sessLog.LogDebug(fmt.Sprintf("UI-оверлей упал: err=%v", err))
	}
}
