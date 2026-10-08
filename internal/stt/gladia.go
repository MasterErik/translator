package stt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/mastererik/translator/internal/common"
	"github.com/mastererik/translator/internal/logger"
)

// gladiaBaseURL — базовый URL Gladia Live API v2.
var gladiaBaseURL = "https://api.gladia.io/v2/live"

// gladiaDialTimeout — таймаут на установку WebSocket-соединения.
const gladiaDialTimeout = 15 * time.Second

// gladiaHTTPTimeout — таймаут на HTTP POST при инициализации сессии.
const gladiaHTTPTimeout = 10 * time.Second

// gladiaWriteTimeout — дедлайн на запись фрейма в WebSocket.
const gladiaWriteTimeout = 5 * time.Second

// Параметры автореконнекта (supervisor). Вынесены в переменные, а не в
// константы, чтобы тесты того же пакета могли ускорить backoff (тест должен
// выполняться за секунды, а не минуты).
var (
	// gladiaReconnectInitial — начальная задержка перед первой попыткой
	// реконнекта (предыдущие попытки не считаются).
	gladiaReconnectInitial = 1 * time.Second

	// gladiaReconnectFactor — множитель экспоненциального роста задержки.
	gladiaReconnectFactor = 2.0

	// gladiaReconnectMax — верхняя граница задержки (cap). Выбрано 60s:
	// Gladia держит live-сессию ограниченное время, поэтому слишком большой
	// cap не даёт преимуществ. На reference-параметрах (1s, ×2) cap 60s
	// достигается на 7-й попытке.
	gladiaReconnectMax = 60 * time.Second

	// gladiaReconnectJitter — доля задержки, добавляемая случайно (±).
	// Сглаживает синхронные реконнекты нескольких сессий.
	gladiaReconnectJitter = 0.25
)

// gladiaMaxReconnectAttempts — после стольких подряд неудачных попыток
// реконнекта supervisor сдаётся и эмитит EventError в textCh (UI зона 3).
const gladiaMaxReconnectAttempts = 5

// gladiaInitRequest — JSON-тело для POST /v2/live при инициализации сессии.
type gladiaInitRequest struct {
	Encoding     string               `json:"encoding"`
	BitDepth     int                  `json:"bit_depth"`
	SampleRate   int                  `json:"sample_rate"`
	Channels     int                  `json:"channels"`
	Model        string               `json:"model"`
	Endpointing  float64              `json:"endpointing"`
	MaxDuration  float64              `json:"maximum_duration_without_endpointing"`
	LanguageCfg  gladiaLanguageConfig `json:"language_config"`
	RealtimeProc gladiaRealtimeConfig `json:"realtime_processing"`
	MessagesCfg  gladiaMessagesConfig `json:"messages_config"`
}

type gladiaLanguageConfig struct {
	Languages     []string `json:"languages"`
	CodeSwitching bool     `json:"code_switching"`
}

type gladiaRealtimeConfig struct {
	Translation    bool                 `json:"translation"`
	TranslationCfg gladiaTranslationCfg `json:"translation_config"`
}

type gladiaTranslationCfg struct {
	TargetLanguages []string `json:"target_languages"`
	Model           string   `json:"model"`
}

type gladiaMessagesConfig struct {
	ReceivePartialTranscripts       bool `json:"receive_partial_transcripts"`
	ReceiveFinalTranscripts         bool `json:"receive_final_transcripts"`
	ReceiveRealtimeProcessingEvents bool `json:"receive_realtime_processing_events"`
}

// gladiaInitResponse — ответ от POST /v2/live: id и url для WebSocket.
type gladiaInitResponse struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// gladiaMessage — общая обёртка для событий Gladia WebSocket.
type gladiaMessage struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// gladiaUtterance — текстовый фрагмент с языком.
type gladiaUtterance struct {
	Text     string `json:"text"`
	Language string `json:"language"`
}

// gladiaTranscriptData — данные события transcript.
type gladiaTranscriptData struct {
	ID        string          `json:"id"`
	IsFinal   bool            `json:"is_final"`
	Utterance gladiaUtterance `json:"utterance"`
}

// gladiaTranslationData — данные события translation.
type gladiaTranslationData struct {
	UtteranceID         string          `json:"utterance_id"`
	Utterance           gladiaUtterance `json:"utterance"`
	TranslatedUtterance gladiaUtterance `json:"translated_utterance"`
}

// GladiaConfig — конфигурация Gladia Live API.
// Все поля имеют разумные значения по умолчанию через applyDefaults.
type GladiaConfig struct {
	APIKey           string  `yaml:"gladia_api_key"` // x-gladia-key
	SourceLang       string  // язык оригинала, default "en"
	TargetLang       string  // язык перевода, default "ru"
	Model            string  // модель STT, default "solaria-1"
	Endpointing      float64 // чувствительность endpointing, default 0.7
	MaxDuration      float64 // макс. длительность без endpointing, default 8
	CodeSwitching    bool    // code switching
	TranslationModel string  // модель перевода, default "enhanced"
}

// applyDefaults устанавливает значения по умолчанию для незаполненных полей.
func (c *GladiaConfig) applyDefaults() {
	if c.SourceLang == "" {
		c.SourceLang = "en"
	}
	if c.TargetLang == "" {
		c.TargetLang = "ru"
	}
	if c.Model == "" {
		c.Model = "solaria-1"
	}
	if c.Endpointing == 0 {
		c.Endpointing = 0.7
	}
	if c.MaxDuration == 0 {
		c.MaxDuration = 8
	}
	if c.TranslationModel == "" {
		c.TranslationModel = "enhanced"
	}
}

// GladiaProvider реализует STTProvider через Gladia Live API.
//
// Двухфазный коннект:
//  1. POST /v2/live → получить {id, url}
//  2. WebSocket dial к url → отправка PCM, чтение JSON-событий
//
// События transcript → промежуточный (Update) или финальный (EndOfTurn) результат.
// События translation → EventEndOfTurn с ChannelID="translation".
type GladiaProvider struct {
	cfg GladiaConfig

	wsConn  *websocket.Conn
	audioCh chan []byte
	textCh  chan common.STTEvent

	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	pumpWg sync.WaitGroup

	// supMu защищает поля жизненного цикла supervisor (supCancel, supDone).
	// Отдельный мьютекс, чтобы Start/Stop (держат g.mu) и supervisor не
	// конкурировали за один замок. Инвариант «двойной Start запрещён»
	// обеспечивает существующая проверка g.cancel != nil под g.mu.
	supMu     sync.Mutex
	supCancel context.CancelFunc
	supDone   chan struct{}

	// wsClosedCycle — CAS-защита «двойного close» для ТЕКУЩЕЙ пары pump'ов.
	// closeWsConn сбрасывается (CAS true→false) в начале каждого цикла
	// реконнекта, поэтому новый writePump/readPump закрывают уже новый conn.
	wsClosedCycle atomic.Bool

	// connClosed закрывается, когда текущий сокет закрыт (closeWsConn).
	// writePump ждёт его в select — так обрыв на стороне readPump немедленно
	// будит writePump, и пара pump'ов завершается синхронно (без ожидания
	// следующего аудио-фрейма). Пересоздаётся на каждый цикл реконнекта.
	connClosed chan struct{}

	sessLog logger.SessionLogger
}

// NewGladiaProvider создаёт новый GladiaProvider.
//
// Параметры:
//   - cfg: конфигурация Gladia (APIKey обязателен)
//   - sessLog: логгер сессии для operational-логов
func NewGladiaProvider(cfg GladiaConfig, sessLog logger.SessionLogger) *GladiaProvider {
	cfg.applyDefaults()
	return &GladiaProvider{
		cfg:     cfg,
		audioCh: make(chan []byte, 64),
		textCh:  make(chan common.STTEvent, 32),
		sessLog: sessLog,
	}
}

// Start инициализирует сессию Gladia и запускает фоновые горутины.
//
//  1. POST /v2/live → получение WebSocket URL
//  2. Dial WebSocket
//  3. Запуск supervisor'а, который владеет pump'ами и автореконнектом
//
// После успешного Start провайдер сам восстанавливает соединение при обрыве
// (supervisor) без участия вызывающего.
func (g *GladiaProvider) Start(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.cancel != nil {
		return fmt.Errorf("gladia: уже запущен")
	}

	g.ctx, g.cancel = context.WithCancel(ctx)

	// Фаза 1: POST /v2/live → получить wsURL.
	wsURL, err := g.initSession(g.ctx)
	if err != nil {
		g.cancel = nil
		g.ctx = nil
		return fmt.Errorf("gladia init: %w", err)
	}

	// Фаза 2: WebSocket dial.
	dialer := websocket.Dialer{
		HandshakeTimeout: gladiaDialTimeout,
	}
	conn, _, err := dialer.DialContext(g.ctx, wsURL, nil)
	if err != nil {
		g.cancel = nil
		g.ctx = nil
		return fmt.Errorf("gladia websocket dial: %w", err)
	}
	g.wsConn = conn
	g.wsClosedCycle.Store(false)
	g.connClosed = make(chan struct{})

	g.sessLog.LogDebug(fmt.Sprintf("gladia: сессия запущена: wsURL=%s", wsURL))

	// Supervisor владеет жизненным циклом pump'ов и реконнектом.
	supCtx, supCancel := context.WithCancel(g.ctx)

	g.supMu.Lock()
	g.supCancel = supCancel
	g.supDone = make(chan struct{})
	supDone := g.supDone
	g.supMu.Unlock()

	// Первая пара pump'ов уже учтена в pumpWg (синхронный Add — чтобы Stop
	// гарантированно их дождался даже при мгновенном Stop после Start).
	g.pumpWg.Add(2)
	firstRun := true
	go g.supervise(supCtx, supDone, firstRun)

	return nil
}

// Stop останавливает провайдера: отменяет контекст, ждёт завершения горутин
// и закрывает WebSocket. Идемпотентен — повторные вызовы безопасны.
func (g *GladiaProvider) Stop() error {
	g.mu.Lock()

	if g.cancel == nil {
		g.mu.Unlock()
		return nil
	}

	g.cancel()

	// Забираем supervisor-хэндлы под supMu (supervisor уже остановится,
	// увидев отмену g.ctx) ДО освобождения g.mu — дальше ждём вне замка.
	g.supMu.Lock()
	supCancel := g.supCancel
	supDone := g.supDone
	g.supCancel = nil
	g.supDone = nil
	g.supMu.Unlock()

	g.mu.Unlock()

	if supCancel != nil {
		supCancel()
	}
	if supDone != nil {
		<-supDone
	}

	// Дренаж pump'ов: после остановки supervisor новые Add не появляются,
	// поэтому Wait() гарантированно возвращает управление.
	g.pumpWg.Wait()

	g.mu.Lock()
	g.closeWsConnLocked()
	g.mu.Unlock()

	return nil
}

// closeWsConn закрывает WebSocket-соединение через CAS — только первый вызов
// для текущего цикла выполняет реальное закрытие, остальные игнорируются.
// wsClosedCycle сбрасывается в false (снова «открыто») в начале каждого цикла
// реконнекта, поэтому новая пара pump'ов закрывает уже новый сокет.
// Одновременно закрывается connClosed — это будит writePump.
//
// Берёт g.mu сам (не вызывать при удержании g.mu), т.к. g.wsConn/connClosed
// читаются pump'ами под тем же мьютексом.
func (g *GladiaProvider) closeWsConn() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closeWsConnLocked()
}

// closeWsConnLocked — тело closeWsConn без взятия g.mu (для вызовов из кода,
// который уже держит g.mu).
func (g *GladiaProvider) closeWsConnLocked() {
	if !g.wsClosedCycle.CompareAndSwap(false, true) {
		return
	}
	if g.wsConn != nil {
		_ = g.wsConn.Close()
		g.wsConn = nil
	}
	if g.connClosed != nil {
		close(g.connClosed)
	}
}

// AudioStream возвращает канал для отправки PCM-аудио (16 кГц, mono, 16-bit).
func (g *GladiaProvider) AudioStream() chan<- []byte {
	return g.audioCh
}

// TextStream возвращает канал для чтения событий STTEvent.
func (g *GladiaProvider) TextStream() <-chan common.STTEvent {
	return g.textCh
}

// initSession отправляет POST /v2/live с конфигурацией сессии
// и возвращает WebSocket URL для подключения.
func (g *GladiaProvider) initSession(ctx context.Context) (string, error) {
	reqBody := gladiaInitRequest{
		Encoding:    "wav/pcm",
		BitDepth:    16,
		SampleRate:  16000,
		Channels:    1,
		Model:       g.cfg.Model,
		Endpointing: g.cfg.Endpointing,
		MaxDuration: g.cfg.MaxDuration,
		LanguageCfg: gladiaLanguageConfig{
			Languages:     []string{g.cfg.SourceLang},
			CodeSwitching: g.cfg.CodeSwitching,
		},
		RealtimeProc: gladiaRealtimeConfig{
			Translation: true,
			TranslationCfg: gladiaTranslationCfg{
				TargetLanguages: []string{g.cfg.TargetLang},
				Model:           g.cfg.TranslationModel,
			},
		},
		MessagesCfg: gladiaMessagesConfig{
			ReceivePartialTranscripts:       true,
			ReceiveFinalTranscripts:         true,
			ReceiveRealtimeProcessingEvents: true,
		},
	}

	// Context adaptation не поддерживается в live-режиме Gladia v2 (только pre-recorded).
	// SystemPrompt и CustomVocabulary сохранены в GladiaConfig для будущего использования.

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal init request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, gladiaBaseURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("create init request: %w", err)
	}
	httpReq.Header.Set("x-gladia-key", g.cfg.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: gladiaHTTPTimeout}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("init POST: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("init POST: статус %d (ожидался 201)", resp.StatusCode)
	}

	var initResp gladiaInitResponse
	if err := json.NewDecoder(resp.Body).Decode(&initResp); err != nil {
		return "", fmt.Errorf("decode init response: %w", err)
	}

	if initResp.URL == "" {
		return "", fmt.Errorf("init response: пустой url")
	}

	return initResp.URL, nil
}

// writePump читает PCM-фреймы из audioCh и отправляет их в WebSocket.
// Завершается при отмене контекста, закрытии audioCh или закрытии сокета
// (connClosed — сигнал от readPump/closeWsConn).
func (g *GladiaProvider) writePump() {
	defer g.closeWsConn()

	g.mu.Lock()
	closed := g.connClosed
	g.mu.Unlock()

	for {
		select {
		case <-g.ctx.Done():
			return
		case <-closed:
			return
		case chunk, ok := <-g.audioCh:
			if !ok {
				return
			}

			g.mu.Lock()
			conn := g.wsConn
			g.mu.Unlock()

			if conn == nil {
				return
			}

			if err := conn.SetWriteDeadline(time.Now().Add(gladiaWriteTimeout)); err != nil {
				g.sessLog.LogDebug(fmt.Sprintf("gladia: set write deadline: error=%v", err))
				return
			}

			if err := conn.WriteMessage(websocket.BinaryMessage, chunk); err != nil {
				g.sessLog.LogDebug(fmt.Sprintf("gladia: ошибка записи в WebSocket: error=%v", err))
				return
			}
		}
	}
}

// readPump читает JSON-сообщения из WebSocket и передаёт их в parseAndEmit.
// Завершается при отмене контекста или ошибке чтения.
func (g *GladiaProvider) readPump() {
	defer g.closeWsConn()

	for {
		select {
		case <-g.ctx.Done():
			return
		default:
		}

		g.mu.Lock()
		conn := g.wsConn
		g.mu.Unlock()

		if conn == nil {
			return
		}

		_, message, err := conn.ReadMessage()
		if err != nil {
			if !websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				g.sessLog.LogDebug(fmt.Sprintf("gladia: WebSocket закрыт: error=%v", err))
			} else {
				g.sessLog.LogDebug(fmt.Sprintf("gladia: ошибка чтения из WebSocket: error=%v", err))
			}
			return
		}

		g.parseAndEmit(message)
	}
}

// parseAndEmit разбирает JSON-сообщение Gladia и маппит его на STTEvent.
//
// Маппинг:
//   - transcript + is_final=false → EventUpdate (промежуточный результат)
//   - transcript + is_final=true  → EventEndOfTurn (финальный результат)
//   - translation                 → EventEndOfTurn с ChannelID="translation"
func (g *GladiaProvider) parseAndEmit(message []byte) {
	var msg gladiaMessage
	if err := json.Unmarshal(message, &msg); err != nil {
		g.sessLog.LogDebug(fmt.Sprintf("gladia: не удалось разобрать сообщение: error=%v, raw=%s", err, string(message)))
		return
	}

	switch msg.Type {
	case "transcript":
		var data gladiaTranscriptData
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			g.sessLog.LogDebug(fmt.Sprintf("gladia: не удалось разобрать transcript: error=%v", err))
			return
		}

		text := data.Utterance.Text
		if text == "" {
			return
		}

		eventType := common.EventUpdate
		if data.IsFinal {
			eventType = common.EventEndOfTurn
		}

		g.emit(common.STTEvent{
			Text:      text,
			Event:     eventType,
			ChannelID: "speaker",
			Timestamp: time.Now(),
		})

	case "translation":
		var data gladiaTranslationData
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			g.sessLog.LogDebug(fmt.Sprintf("gladia: не удалось разобрать translation: error=%v", err))
			return
		}

		text := data.TranslatedUtterance.Text
		if text == "" {
			return
		}

		g.emit(common.STTEvent{
			Text:      text,
			Event:     common.EventEndOfTurn,
			ChannelID: "translation",
			Timestamp: time.Now(),
		})

	default:
		// metadata, error и прочие типы игнорируем.
	}
}

// emit отправляет STTEvent в textCh. Если канал заполнен или контекст отменён,
// событие пишется в лог и отбрасывается.
func (g *GladiaProvider) emit(evt common.STTEvent) {
	select {
	case g.textCh <- evt:
	case <-g.ctx.Done():
		g.sessLog.LogDebug(fmt.Sprintf("gladia: контекст отменён, событие не отправлено: event=%s", evt.Event))
	default:
		g.sessLog.LogDebug(fmt.Sprintf("gladia: канал textCh заполнен, событие отброшено: event=%s", evt.Event))
	}
}

// emitError публикует STTEvent{Event: EventError} в textCh через тот же путь,
// что и parseAndEmit (g.emit). Контекстная ветка g.emit использует g.ctx, а не
// supCtx, — значит ошибка доедет до UI даже когда supervisor вот-вот завершится,
// но Stop() уже не отменял g.ctx. Если g.ctx отменён (идёт Stop) — событие
// молча отбрасывается, что корректно (UI уже останавливается).
func (g *GladiaProvider) emitError(err error) {
	g.emit(common.STTEvent{
		Event:     common.EventError,
		Error:     err,
		Timestamp: time.Now(),
	})
}

// supervise — единая точка владения жизненным циклом pump'ов и реконнектом.
//
// Инварианты:
//   - На входе в supervise первая пара pump'ов УЖЕ запущена, и в pumpWg уже
//     сделано Add(2) (сделано синхронно в Start).
//   - supervisor завершается ровно при отмене supCtx (g.ctx отменён =
//     Stop либо отмена родительского ctx) или после maxReconnectAttempts
//     подряд неудачных переподключений.
//
// Решение о реконнекте выводится из причины завершения pump'ов:
//   - оба вышли при уже отменённом supCtx → это Stop, реконнекта нет;
//   - хотя бы один вышел из-за ошибки сокета, а supCtx жив → реконнект.
//
// Между итерациями supervisor ждёт завершения предыдущей пары pump'ов
// (pumpWg) — поэтому в любой момент времени активна максимум одна пара,
// а остановка supervisor'а гарантирует отсутствие «висящих» pump'ов.
func (g *GladiaProvider) supervise(supCtx context.Context, supDone chan struct{}, firstRun bool) {
	defer close(supDone)

	isFirst := firstRun
	reconnectAttempts := 0
	// В реконнект уходим, только если предыдущая пара pump'ов НЕ была
	// остановлена отменой ctx (иначе молчаливая смерть readPump трактовалась
	// бы как сетевой обрыв).
	needReconnect := false

	for {
		if !isFirst {
			if err := g.dialReconnect(supCtx); err != nil {
				if ctxErr := supCtx.Err(); ctxErr != nil {
					// Stop/ctx.Done во время попытки — немедленный выход.
					return
				}
				reconnectAttempts++
				g.sessLog.LogDebug(fmt.Sprintf(
					"gladia: попытка реконнекта %d/%d не удалась: error=%v",
					reconnectAttempts, gladiaMaxReconnectAttempts, err))
				if reconnectAttempts >= gladiaMaxReconnectAttempts {
					g.emitError(fmt.Errorf(
						"gladia: не удалось восстановить соединение после %d попыток: %w",
						gladiaMaxReconnectAttempts, err))
					g.sessLog.LogDebug("gladia: supervisor сдаётся после исчерпания попыток")
					return
				}
				if !g.sleepBackoff(supCtx, reconnectAttempts) {
					return
				}
				continue
			}
			// Успешный реконнект — счётчик попыток сброшен.
			reconnectAttempts = 0
		}

		// Учёт пары pump'ов: Add(2) ДО запуска горутин. На первой итерации
		// Add(2) уже сделан в Start; на последующих — здесь.
		if !isFirst {
			g.pumpWg.Add(2)
		}
		isFirst = false

		go func() { defer g.pumpWg.Done(); g.writePump() }()
		go func() { defer g.pumpWg.Done(); g.readPump() }()

		// Ждём завершения пары: либо по отмене контекста (Stop), либо по
		// ошибке pump'ов (сетевой обрыв/ошибка записи).
		select {
		case <-supCtx.Done():
			needReconnect = false
		case <-g.pumpDone():
			needReconnect = supCtx.Err() == nil
		}

		g.pumpWg.Wait()

		if needReconnect {
			continue
		}
		return
	}
}

// pumpDone возвращает канал, который закрывается, когда счётчик pumpWg
// доходит до нуля. Реализовано через отдельную горутину-наблюдателя; горутина
// завершается вместе с парой pump'ов, поэтому не течёт.
func (g *GladiaProvider) pumpDone() <-chan struct{} {
	done := make(chan struct{})
	go func() {
		g.pumpWg.Wait()
		close(done)
	}()
	return done
}

// dialReconnect закрывает старый сокет (через per-cycle CAS) и выполняет
// повторный двухфазный коннект: initSession (POST /v2/live) → DialContext.
// Соответствует документации docs/gladia-flow.md (reconnect = повторный
// POST). Параметры (Model/Endpointing/MaxDuration/…) неизменны, поэтому
// POST идемпотентен.
func (g *GladiaProvider) dialReconnect(ctx context.Context) error {
	// Закрываем старый сокет (CAS защищает от двойного close, даже если его
	// уже закрыла завершившаяся пара pump'ов).
	g.mu.Lock()
	g.closeWsConnLocked()
	g.mu.Unlock()

	wsURL, err := g.initSession(ctx)
	if err != nil {
		return fmt.Errorf("gladia reconnect init: %w", err)
	}

	dialer := websocket.Dialer{HandshakeTimeout: gladiaDialTimeout}
	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("gladia reconnect dial: %w", err)
	}

	g.mu.Lock()
	g.wsConn = conn
	g.wsClosedCycle.Store(false)       // новый сокет — CAS-флаг снова «открыт»
	g.connClosed = make(chan struct{}) // свежий сигнал для новой пары pump'ов
	g.mu.Unlock()

	g.sessLog.LogDebug("gladia: переподключение успешно")
	return nil
}

// sleepBackoff выдерживает экспоненциальную задержку (initial * factor^attempt,
// cap = gladiaReconnectMax) c jitter. attempt — номер уже сработавшей неудачной
// попытки (1, 2, …). Возвращает false, если во время ожидания отменён ctx —
// тогда supervisor должен немедленно завершиться без реконнекта.
func (g *GladiaProvider) sleepBackoff(ctx context.Context, attempt int) bool {
	delay := gladiaReconnectInitial
	// attempt — 1-based; после 1-й неудачи пауза = initial.
	for i := 1; i < attempt; i++ {
		delay = time.Duration(float64(delay) * gladiaReconnectFactor)
		if delay >= gladiaReconnectMax {
			delay = gladiaReconnectMax
			break
		}
	}
	// ± jitter.
	if gladiaReconnectJitter > 0 {
		delta := time.Duration(float64(delay) * gladiaReconnectJitter)
		delay += time.Duration(rand.Int63n(int64(delta)*2)) - delta
		if delay < 0 {
			delay = 0
		}
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// Compile-time interface check.
var _ STTProvider = (*GladiaProvider)(nil)
