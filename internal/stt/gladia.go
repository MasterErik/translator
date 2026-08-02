package stt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/mastererik/translator/internal/common"
	"github.com/mastererik/translator/internal/logger"
)

// gladiaBaseURL — базовый URL Gladia Live API v2.
const gladiaBaseURL = "https://api.gladia.io/v2/live"

// gladiaDialTimeout — таймаут на установку WebSocket-соединения.
const gladiaDialTimeout = 15 * time.Second

// gladiaHTTPTimeout — таймаут на HTTP POST при инициализации сессии.
const gladiaHTTPTimeout = 10 * time.Second

// gladiaWriteTimeout — дедлайн на запись фрейма в WebSocket.
const gladiaWriteTimeout = 5 * time.Second

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
	APIKey           string  // x-gladia-key
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

	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	wsClosed atomic.Bool
	pumpWg   sync.WaitGroup

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
		cfg:      cfg,
		audioCh:  make(chan []byte, 64),
		textCh:   make(chan common.STTEvent, 32),
		sessLog:  sessLog,
	}
}

// Start инициализирует сессию Gladia и запускает фоновые горутины.
//
//  1. POST /v2/live → получение WebSocket URL
//  2. Dial WebSocket
//  3. Запуск writePump и readPump
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

	g.sessLog.LogDebug(fmt.Sprintf("gladia: сессия запущена: wsURL=%s", wsURL))

	g.pumpWg.Add(2)
	go func() { defer g.pumpWg.Done(); g.writePump() }()
	go func() { defer g.pumpWg.Done(); g.readPump() }()

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

	g.mu.Unlock()
	g.pumpWg.Wait()
	g.mu.Lock()

	g.closeWsConn()
	g.mu.Unlock()

	return nil
}

// closeWsConn закрывает WebSocket-соединение через CAS — только первый вызов
// выполняет реальное закрытие, остальные игнорируются.
func (g *GladiaProvider) closeWsConn() {
	if !g.wsClosed.CompareAndSwap(false, true) {
		return
	}
	if g.wsConn != nil {
		_ = g.wsConn.Close()
		g.wsConn = nil
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
// Завершается при отмене контекста или закрытии audioCh.
func (g *GladiaProvider) writePump() {
	defer g.closeWsConn()

	for {
		select {
		case <-g.ctx.Done():
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

// Compile-time interface check.
var _ STTProvider = (*GladiaProvider)(nil)
