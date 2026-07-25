// Package pipeline предоставляет Pipeline — центральный оркестратор жизненного
// цикла приложения Translator: захват аудио, STT, перевод и UI-оверлей.
package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mastererik/translator/internal/common"
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
	streamDone   chan struct{}
	dispatchDone chan struct{} // сигнал завершения runDispatch (буфер 1)
}

// Config — все настройки пайплайна (без магических чисел).
type Config struct {
	// Захват
	Capturer      capturer
	ValidateAudio bool
	LoopbackName  string
	MicName       string

	// STT
	DeepgramAPIKey string
	DeepgramModel  string

	// LLM
	LLMBaseURL string
	LLMAPIKey  string
	LLMModel   string
	MaxTokens  int
	WindowSize int

	// Overlay
	OverlayCfg ui.OverlayConfig

	// Логгер
	LogDir    string
	SaveAudio bool

	// Каналы и таймауты (избавляемся от магических чисел)
	TextStreamBuffer int           // default: 64
	StreamDoneBuffer int           // default: 64
	StreamTimeout    time.Duration // default: 15s
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
	if cfg.StreamDoneBuffer <= 0 {
		cfg.StreamDoneBuffer = 64
	}
	if cfg.StreamTimeout <= 0 {
		cfg.StreamTimeout = 15 * time.Second
	}
	if cfg.AnswerTimeout <= 0 {
		cfg.AnswerTimeout = 10 * time.Second
	}

	// STT-провайдер.
	deepgramProv := stt.NewDeepgramProvider(cfg.DeepgramAPIKey, cfg.DeepgramModel)

	// LLM-провайдер.
	llmAPIKey := cfg.LLMAPIKey
	if llmAPIKey == "" {
		return nil, fmt.Errorf("pipeline: LLMAPIKey обязателен")
	}
	llmBaseURL := cfg.LLMBaseURL
	if llmBaseURL == "" {
		llmBaseURL = "https://api.openai.com/v1"
	}
	llmModel := cfg.LLMModel
	if llmModel == "" {
		llmModel = "gpt-4o-mini"
	}

	llmProv := translator.NewOpenAIProviderWithConfig(llmBaseURL, llmAPIKey, llmModel)
	llmProv.DisableThinking()
	llmProv.SetMaxTokens(cfg.MaxTokens)

	// TranslationEngine.
	engine := translator.NewEngine(llmProv, cfg.WindowSize)

	// Overlay.
	overlay := ui.NewOverlay(cfg.OverlayCfg)

	// Логгер сессии: NopLogger если LogDir пуст.
	var sessLog logger.SessionLogger = logger.NewNopSessionLogger()
	if cfg.LogDir != "" {
		var err error
		sessLog, err = logger.NewFileSessionLogger(cfg.LogDir, cfg.SaveAudio)
		if err != nil {
			return nil, fmt.Errorf("pipeline: создание логгера сессии: %w", err)
		}
	}

	// Валидация аудиоустройств (опционально).
	if cfg.ValidateAudio && cfg.Capturer != nil {
		// Проверяем, что capturer может стартовать — закрываем контекст сразу.
		validateCtx, validateCancel := context.WithCancel(context.Background())
		validateCancel()
		_, _, err := cfg.Capturer.Start(validateCtx)
		if err != nil {
			return nil, fmt.Errorf("pipeline: валидация захвата аудио: %w", err)
		}
	}

	p := &Pipeline{
		cfg:          cfg,
		capturer:     cfg.Capturer,
		sttProv:      deepgramProv,
		engine:       engine,
		overlay:      overlay,
		sessLog:      sessLog,
		textStream:   make(chan common.STTEvent, cfg.TextStreamBuffer),
		streamDone:   make(chan struct{}, cfg.StreamDoneBuffer),
		dispatchDone: make(chan struct{}, 1),
	}

	return p, nil
}

// --------------------------------------------------------------------------
// Жизненный цикл
// --------------------------------------------------------------------------

// Run запускает полный жизненный цикл пайплайна: инициализирует STT, запускает
// горутины захвата, STT, dispatch и UI-оверлея, ожидает сигнала завершения
// (SIGINT/SIGTERM) и выполняет корректный shutdown.
func (p *Pipeline) Run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := p.sttProv.Start(ctx); err != nil {
		return fmt.Errorf("pipeline: запуск STT: %w", err)
	}
	p.sessLog.LogDebug("STT запущен")

	go p.runCapture(ctx)
	go p.runSTT(ctx)
	go p.runDispatch(ctx)
	go p.runUI(ctx)

	p.sessLog.LogDebug("все горутины запущены")
	slog.Info("pipeline запущен, ожидание сигнала")

	<-ctx.Done()
	slog.Info("получен сигнал завершения, запускается shutdown")
	p.sessLog.LogDebug("получен сигнал завершения")
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
		slog.Warn("ошибка остановки STT", "err", err)
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
			slog.Warn("ошибка закрытия логгера", "err", err)
		}
	}

	slog.Info("shutdown завершён")
}

// --------------------------------------------------------------------------
// runCapture — захват аудио
// --------------------------------------------------------------------------

// runCapture запускает двухканальный захват аудио и направляет PCM-данные
// в аудиопоток STT-провайдера и (опционально) логгер сессии.
func (p *Pipeline) runCapture(ctx context.Context) {
	if p.capturer == nil {
		slog.Warn("capturer не задан, захват аудио пропущен")
		return
	}

	loopbackCh, micCh, err := p.capturer.Start(ctx)
	if err != nil {
		slog.Error("не удалось запустить захват аудио", "error", err)
		return
	}

	audioStream := p.sttProv.AudioStream()

	// Fan-out micCh: копируем каждый фрейм в логгер и STT.
	var logCh chan []byte
	if p.cfg.SaveAudio && p.sessLog != nil {
		logCh = make(chan []byte, 32)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// Микрофон → fan-out → STT + логгер.
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
	go func() {
		defer wg.Done()
		p.routeAudioChannel(ctx, loopbackCh, audioStream, "speaker")
	}()

	<-ctx.Done()
	wg.Wait()
	slog.Info("пайплайн захвата остановлен")
}

// routeAudioChannel читает PCM-фрагменты из src и пересылает в dst (STT).
func (p *Pipeline) routeAudioChannel(ctx context.Context, src <-chan []byte, dst chan<- []byte, channelID string) {
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
				slog.Warn("не удалось сохранить аудио-фрагмент", "error", err)
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
				slog.Warn("ошибка STT-события", "err", event.Error)
				continue
			}

			// Логирование — асинхронно, best-effort.
			if p.sessLog != nil {
				go func(ev common.STTEvent) {
					if err := p.sessLog.LogText(ev); err != nil {
						slog.Warn("ошибка логирования STT", "err", err)
					}
				}(event)
			}

			// Маршрутизация.
			p.routeSTTEvent(ctx, event)
		}
	}
}

// routeSTTEvent маршрутизирует STT-событие в textStream:
//   - Final (EventEndOfTurn) — блокирующая отправка.
//   - Interim — неблокирующая отправка.
func (p *Pipeline) routeSTTEvent(ctx context.Context, event common.STTEvent) {
	if event.Event == common.EventEndOfTurn {
		// Final: блокирующая отправка — перевод не теряется.
		select {
		case p.textStream <- event:
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

// --------------------------------------------------------------------------
// runDispatch — центральный dispatch-узел
// --------------------------------------------------------------------------

// runDispatch — центральный select-узел, координирующий поток STT-событий
// и стриминг-перевод с асинхронной генерацией подсказок.
//
// Логика:
//   - interim-события: сразу в UI (предпросмотр).
//   - final-события: запускается стриминг-перевод, токены инкрементально в UI.
//     После завершения стрима — классификация вопроса и генерация подсказок.
//
// При отмене контекста корректно завершает все горутины.
func (p *Pipeline) runDispatch(ctx context.Context) {
	slog.Info("dispatch-узел запущен (стриминг)")

	// Гарантируем сигнал завершения при любом выходе из runDispatch.
	defer func() {
		if p.dispatchDone != nil {
			p.dispatchDone <- struct{}{}
		}
	}()

	activeStreams := 0

	for {
		select {
		case <-ctx.Done():
			slog.Info("dispatch: контекст отменён, ожидание стримов",
				"active_streams", activeStreams)
			for activeStreams > 0 {
				<-p.streamDone
				activeStreams--
			}
			slog.Info("dispatch-узел остановлен")
			return

		case event, ok := <-p.textStream:
			if !ok {
				slog.Info("textStream закрыт, dispatch завершает работу",
					"active_streams", activeStreams)
				for activeStreams > 0 {
					<-p.streamDone
					activeStreams--
				}
				return
			}

			if event.Event != common.EventEndOfTurn {
				// Промежуточный результат: показываем оригинал в верхней зоне.
				p.overlay.AddMessage(ui.UIMessage{
					Type:      ui.Interim,
					Text:      event.Text,
					Timestamp: event.Timestamp,
				})
				continue
			}

			// Финальный результат: запускаем стриминг-перевод.
			activeStreams++
			go func(ev common.STTEvent) {
				defer func() { p.streamDone <- struct{}{} }()
				p.handleStreamingTranslation(ev)
			}(event)

		case <-p.streamDone:
			activeStreams--
		}
	}
}

// --------------------------------------------------------------------------
// handleStreamingTranslation — стриминг-перевод одной фразы
// --------------------------------------------------------------------------

// handleStreamingTranslation выполняет стриминг-перевод одной фразы:
// токены инкрементально отправляются в UI, после завершения стрима
// определяется вопрос и генерируются подсказки.
func (p *Pipeline) handleStreamingTranslation(event common.STTEvent) {
	// Показываем "[переводится...]" сразу.
	p.overlay.AddMessage(ui.UIMessage{
		Type:      ui.Translation,
		Text:      "[переводится...]",
		Timestamp: event.Timestamp,
		MsgStatus: "pending",
	})

	streamCtx, streamCancel := context.WithTimeout(context.Background(), p.cfg.StreamTimeout)
	defer streamCancel()

	tokenCh, err := p.engine.ProcessFinalTranscriptStream(streamCtx, event.Text)
	if err != nil {
		slog.Error("стриминг-перевод упал",
			"original_text", event.Text,
			"error", err,
		)
		p.overlay.AddMessage(ui.UIMessage{
			Type:      ui.Translation,
			Text:      "[ошибка перевода]",
			Timestamp: event.Timestamp,
			MsgStatus: "done",
		})
		return
	}

	var fullText strings.Builder
	for token := range tokenCh {
		if strings.HasPrefix(token, "[ERROR:") {
			slog.Error("ошибка в стриме", "text", event.Text, "token_error", token)
			return
		}
		fullText.WriteString(token)
		p.overlay.AddMessage(ui.UIMessage{
			Type:      ui.Translation,
			Text:      fullText.String(),
			Timestamp: event.Timestamp,
			MsgStatus: "streaming",
		})
	}

	translation := fullText.String()
	if translation == "" {
		slog.Warn("пустой перевод", "text", event.Text)
		return
	}

	p.overlay.AddMessage(ui.UIMessage{
		Type:      ui.Translation,
		Text:      translation,
		Timestamp: event.Timestamp,
		MsgStatus: "done",
	})

	// Добавляем оригинал и перевод в историю — теперь перевод известен.
	p.overlay.AddMessage(ui.UIMessage{
		Type:        ui.History,
		Text:        event.Text,
		Translation: translation,
		Timestamp:   event.Timestamp,
	})

	isQ := translator.IsQuestion(event.Text)

	slog.Info("стриминг-перевод завершён",
		"text", event.Text,
		"translation", translation,
		"is_question", isQ,
	)

	if p.sessLog != nil {
		// BUG #5: используем time.Now() чтобы записи в CSV шли
		// в хронологическом порядке, а не в порядке STT-событий.
		logEvent := event
		logEvent.Timestamp = time.Now()
		if err := p.sessLog.LogTranslation(logEvent, translation, nil); err != nil {
			slog.Warn("не удалось записать перевод в лог", "error", err)
		}
	}

	if isQ {
		go p.generateAnswersAsync(event, translation)
	}
}

// generateAnswersAsync запускает потоковую генерацию подсказок в фоне.
// Не блокирует handleStreamingTranslation — перевод сразу показывается пользователю.
func (p *Pipeline) generateAnswersAsync(event common.STTEvent, translation string) {
	ansCtx, ansCancel := context.WithTimeout(context.Background(), p.cfg.AnswerTimeout)
	defer ansCancel()

	tokenCh, err := p.engine.GenerateAnswersStream(ansCtx, event.Text)
	if err != nil {
		slog.Error("генерация подсказок не удалась", "text", event.Text, "error", err)
		return
	}

	// Собираем все токены в полный ответ.
	var fullText strings.Builder
	for token := range tokenCh {
		if strings.HasPrefix(token, "[ERROR:") {
			slog.Error("ошибка в стриме подсказок", "text", event.Text, "token_error", token)
			return
		}
		fullText.WriteString(token)
	}

	answers := parseAnswerHints(fullText.String())
	if len(answers) == 0 {
		return
	}

	p.overlay.AddMessage(ui.UIMessage{
		Type:      ui.AnswerCandidates,
		Text:      event.Text,
		Answers:   answers,
		Timestamp: event.Timestamp,
	})

	// Логируем подсказки.
	if p.sessLog != nil {
		if err := p.sessLog.LogTranslation(event, translation, answers); err != nil {
			slog.Warn("не удалось записать подсказки в лог", "error", err)
		}
	}
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
	slog.Info("UI-оверлей запущен")
	if err := p.overlay.Run(ctx); err != nil {
		slog.Error("UI-оверлей упал", "err", err)
	}
}
