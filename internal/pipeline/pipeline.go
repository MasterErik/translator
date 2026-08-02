// Package pipeline предоставляет Pipeline — центральный оркестратор жизненного
// цикла приложения Translator: захват аудио, STT, перевод и UI-оверлей.
package pipeline

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mastererik/translator/internal/common"
	"github.com/mastererik/translator/internal/dispatcher"
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
}

// --------------------------------------------------------------------------
// Структуры
// --------------------------------------------------------------------------

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
}

// Config — все настройки пайплайна (без магических чисел).
type Config struct {
	// Захват
	Capturer      capturer
	ValidateAudio bool
	LoopbackName  string
	MicName       string

	// STT (Gladia)
	GladiaAPIKey     string
	SourceLang       string
	TargetLang       string
	SystemPrompt     string   // системный промпт для context_adaptation
	CustomVocabulary []string // пользовательский словарь терминов

	// LLM
	LLMBaseURL string
	LLMAPIKey  string
	LLMModel   string
	MaxTokens  int
	CVContext  string // контекст резюме для подсказок

	// Overlay
	OverlayCfg ui.OverlayConfig

	// Логгер
	LogDir    string
	SaveAudio bool

	// Каналы и таймауты (избавляемся от магических чисел)
	TextStreamBuffer int           // default: 64
	AnswerTimeout    time.Duration // default: 10s

	// Debug
	SilentFrame []byte // для StubCapture
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
		APIKey:           cfg.GladiaAPIKey,
		SourceLang:       cfg.SourceLang,
		TargetLang:       cfg.TargetLang,
		SystemPrompt:     cfg.SystemPrompt,
		CustomVocabulary: cfg.CustomVocabulary,
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
		newErr = fmt.Errorf("pipeline: LLMModel обязателен (например llama-3.3-70b-versatile)")
		return nil, newErr
	}

	llmProv := translator.NewChatProvider(llmBaseURL, llmAPIKey, llmModel)
	llmProv.SetMaxTokens(cfg.MaxTokens)

	// TranslationEngine.
	engine := translator.NewEngine(llmProv)
	if cfg.CVContext != "" {
		engine.SetCVContext(cfg.CVContext)
	}

	// Overlay.
	overlay := ui.NewOverlay(cfg.OverlayCfg, sessLog)

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
		dispatchDone: make(chan struct{}, 1),
		dispatch: dispatcher.New(
			overlay,
			engine,
			sessLog,
			dispatcher.Config{AnswerTimeout: cfg.AnswerTimeout},
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

	// Fan-out micCh: копируем каждый фрейм в логгер и STT.
	var logCh chan []byte
	if p.cfg.SaveAudio && p.sessLog != nil {
		logCh = make(chan []byte, 32)
	}

	var wg sync.WaitGroup

	// Микрофон → fan-out → STT + логгер.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case chunk, ok := <-micCh:
				if !ok {
					return
				}
				// В логгер (копия).
				if logCh != nil {
					raw := make([]byte, len(chunk))
					copy(raw, chunk)
					select {
					case logCh <- raw:
					default:
					}
				}
				// В STT (оригинал).
				select {
				case audioStream <- chunk:
				case <-ctx.Done():
					return
				default:
				}
			}
		}
	}()

	// Запись микрофона → логгер (отдельная горутина).
	if logCh != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.routeRawMic(ctx, logCh)
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
		select {
		case p.textStream <- event:
		case <-time.After(5 * time.Second):
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

// generateAnswersAsync делегирует генерацию подсказок в dispatcher.
// Оставлен для совместимости с существующими тестами.
func (p *Pipeline) generateAnswersAsync(question string) {
	if p.dispatch == nil {
		p.dispatch = dispatcher.New(p.overlay, p.engine, p.sessLog,
			dispatcher.Config{AnswerTimeout: p.cfg.AnswerTimeout})
	}
	p.dispatch.GenerateAnswers(question)
}

// parseAnswerHints разбирает сырой ответ LLM на отдельные подсказки.
// Поддерживает формат: "- EN: <eng> | RU: <rus>" (двуязычный).
func parseAnswerHints(raw string) []string {
	lines := strings.Split(raw, "\n")
	var hints []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Убираем ведущий маркер списка ("- ", "* ", "• ").
		clean := strings.TrimLeft(trimmed, "-*• ")
		clean = strings.TrimSpace(clean)
		if clean == "" {
			continue
		}
		hints = append(hints, clean)
	}
	if len(hints) > 3 {
		hints = hints[:3]
	}
	return hints
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
