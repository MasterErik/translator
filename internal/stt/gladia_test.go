package stt

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/mastererik/translator/internal/common"
	"github.com/mastererik/translator/internal/logger"
)

// makeTranscriptJSON создаёт JSON события transcript для тестов.
func makeTranscriptJSON(text string, isFinal bool) []byte {
	data := gladiaTranscriptData{
		ID:      "test-id-001",
		IsFinal: isFinal,
		Utterance: gladiaUtterance{
			Text:     text,
			Language: "en",
		},
	}
	dataBytes, _ := json.Marshal(data)
	msg := gladiaMessage{
		Type: "transcript",
		Data: dataBytes,
	}
	raw, _ := json.Marshal(msg)
	return raw
}

// makeTranslationJSON создаёт JSON события translation для тестов.
func makeTranslationJSON(original, translated string) []byte {
	data := gladiaTranslationData{
		UtteranceID: "test-id-001",
		Utterance: gladiaUtterance{
			Text:     original,
			Language: "en",
		},
		TranslatedUtterance: gladiaUtterance{
			Text:     translated,
			Language: "ru",
		},
	}
	dataBytes, _ := json.Marshal(data)
	msg := gladiaMessage{
		Type: "translation",
		Data: dataBytes,
	}
	raw, _ := json.Marshal(msg)
	return raw
}

// TestGladiaProvider_ImplementsSTTProvider — проверка на этапе компиляции,
// что GladiaProvider реализует интерфейс STTProvider.
func TestGladiaProvider_ImplementsSTTProvider(t *testing.T) {
	var _ STTProvider = (*GladiaProvider)(nil)
}

// TestGladiaProvider_ParseTranscript проверяет маппинг transcript → STTEvent:
//   - is_final=false → EventUpdate
//   - is_final=true  → EventEndOfTurn
func TestGladiaProvider_ParseTranscript(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		isFinal   bool
		wantEvent string
	}{
		{
			name:      "interim (is_final=false) → Update",
			text:      "Hello world",
			isFinal:   false,
			wantEvent: common.EventUpdate,
		},
		{
			name:      "final (is_final=true) → EndOfTurn",
			text:      "Hello world final",
			isFinal:   true,
			wantEvent: common.EventEndOfTurn,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			provider := &GladiaProvider{
				cfg:      GladiaConfig{APIKey: "test-key"},
				audioCh: make(chan []byte, 8),
				textCh:  make(chan common.STTEvent, 8),
		sessLog: logger.NewNopSessionLogger(),
			}
			provider.ctx, provider.cancel = context.WithCancel(ctx)

			msg := makeTranscriptJSON(tt.text, tt.isFinal)
			provider.parseAndEmit(msg)

			select {
			case event := <-provider.textCh:
				if event.Event != tt.wantEvent {
					t.Errorf("ожидался Event=%s, получен Event=%s", tt.wantEvent, event.Event)
				}
				if event.Text != tt.text {
					t.Errorf("ожидался Text=%q, получен Text=%q", tt.text, event.Text)
				}
				if event.ChannelID != "speaker" {
					t.Errorf("ожидался ChannelID=speaker, получен ChannelID=%q", event.ChannelID)
				}
				if event.Timestamp.IsZero() {
					t.Errorf("ожидался ненулевой Timestamp")
				}
			case <-ctx.Done():
				t.Fatal("таймаут ожидания события transcript")
			}
		})
	}
}

// TestGladiaProvider_ParseTranslation проверяет маппинг translation → STTEvent:
// Event=EventEndOfTurn, Text=translated_utterance.text, ChannelID="translation".
func TestGladiaProvider_ParseTranslation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	provider := &GladiaProvider{
		cfg:      GladiaConfig{APIKey: "test-key"},
		audioCh: make(chan []byte, 8),
		textCh:  make(chan common.STTEvent, 8),
		sessLog: logger.NewNopSessionLogger(),
	}
	provider.ctx, provider.cancel = context.WithCancel(ctx)

	msg := makeTranslationJSON("Hello", "Привет")
	provider.parseAndEmit(msg)

	select {
	case event := <-provider.textCh:
		if event.Event != common.EventEndOfTurn {
			t.Errorf("ожидался Event=EventEndOfTurn, получен Event=%s", event.Event)
		}
		if event.Text != "Привет" {
			t.Errorf("ожидался Text=Привет, получен Text=%q", event.Text)
		}
		if event.ChannelID != "translation" {
			t.Errorf("ожидался ChannelID=translation, получен ChannelID=%q", event.ChannelID)
		}
	case <-ctx.Done():
		t.Fatal("таймаут ожидания события translation")
	}
}

// TestGladiaProvider_ParseTranscriptEmpty проверяет, что события с пустым текстом не эмитятся.
func TestGladiaProvider_ParseTranscriptEmpty(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	provider := &GladiaProvider{
		cfg:      GladiaConfig{APIKey: "test-key"},
		audioCh: make(chan []byte, 8),
		textCh:  make(chan common.STTEvent, 8),
		sessLog: logger.NewNopSessionLogger(),
	}
	provider.ctx, provider.cancel = context.WithCancel(ctx)

	msg := makeTranscriptJSON("", false)
	provider.parseAndEmit(msg)

	select {
	case event := <-provider.textCh:
		t.Errorf("не ожидалось событие для пустого текста, получено %+v", event)
	case <-time.After(200 * time.Millisecond):
		// OK — событие не было отправлено.
	}
}

// TestGladiaProvider_ParseUnknownType проверяет, что неизвестные типы сообщений игнорируются.
func TestGladiaProvider_ParseUnknownType(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	provider := &GladiaProvider{
		cfg:      GladiaConfig{APIKey: "test-key"},
		audioCh: make(chan []byte, 8),
		textCh:  make(chan common.STTEvent, 8),
		sessLog: logger.NewNopSessionLogger(),
	}
	provider.ctx, provider.cancel = context.WithCancel(ctx)

	unknownMsg, _ := json.Marshal(gladiaMessage{
		Type: "metadata",
		Data: json.RawMessage(`{"key":"value"}`),
	})
	provider.parseAndEmit(unknownMsg)

	select {
	case event := <-provider.textCh:
		t.Errorf("не ожидалось событие для типа metadata, получено %+v", event)
	case <-time.After(200 * time.Millisecond):
		// OK.
	}
}

// TestGladiaProvider_StopIdempotent проверяет, что двойной вызов Stop не вызывает панику.
func TestGladiaProvider_StopIdempotent(t *testing.T) {
	provider := NewGladiaProvider(GladiaConfig{APIKey: "test-key", TargetLang: "ru"}, logger.NewNopSessionLogger())

	// Первый Stop без Start — без паники.
	if err := provider.Stop(); err != nil {
		t.Errorf("неожиданная ошибка при первом Stop: %v", err)
	}

	// Двойной Stop — без паники.
	if err := provider.Stop(); err != nil {
		t.Errorf("неожиданная ошибка при двойном Stop: %v", err)
	}

	// Тройной Stop — без паники.
	if err := provider.Stop(); err != nil {
		t.Errorf("неожиданная ошибка при тройном Stop: %v", err)
	}
}

// TestGladiaProvider_DoubleStart проверяет, что двойной вызов Start возвращает ошибку.
func TestGladiaProvider_DoubleStart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	provider := &GladiaProvider{
		cfg:      GladiaConfig{APIKey: "test-key"},
		audioCh: make(chan []byte, 8),
		textCh:  make(chan common.STTEvent, 8),
		sessLog: logger.NewNopSessionLogger(),
	}

	// Устанавливаем ctx/cancel вручную (симуляция успешного Start).
	provider.ctx, provider.cancel = context.WithCancel(ctx)

	// Двойной вызов Start должен вернуть ошибку.
	err := provider.Start(ctx)
	if err == nil {
		t.Error("ожидалась ошибка при двойном Start")
	}
}

// TestGladiaProvider_ParseBrokenJSON проверяет, что битый JSON не вызывает панику.
func TestGladiaProvider_ParseBrokenJSON(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	provider := &GladiaProvider{
		cfg:      GladiaConfig{APIKey: "test-key"},
		audioCh: make(chan []byte, 8),
		textCh:  make(chan common.STTEvent, 8),
		sessLog: logger.NewNopSessionLogger(),
	}
	provider.ctx, provider.cancel = context.WithCancel(ctx)

	// Битый JSON — не должно быть паники.
	provider.parseAndEmit([]byte("not json at all"))

	// Нет события в канале.
	select {
	case event := <-provider.textCh:
		t.Errorf("не ожидалось событие для битого JSON, получено %+v", event)
	case <-time.After(200 * time.Millisecond):
		// OK.
	}
}

// TestGladiaProvider_ParseTranslationEmpty проверяет, что перевод с пустым текстом игнорируется.
func TestGladiaProvider_ParseTranslationEmpty(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	provider := &GladiaProvider{
		cfg:      GladiaConfig{APIKey: "test-key"},
		audioCh: make(chan []byte, 8),
		textCh:  make(chan common.STTEvent, 8),
		sessLog: logger.NewNopSessionLogger(),
	}
	provider.ctx, provider.cancel = context.WithCancel(ctx)

	// Пустой перевод.
	msg := makeTranslationJSON("Hello", "")
	provider.parseAndEmit(msg)

	select {
	case event := <-provider.textCh:
		t.Errorf("не ожидалось событие для пустого перевода, получено %+v", event)
	case <-time.After(200 * time.Millisecond):
		// OK.
	}
}

// TestGladiaProvider_ParseTranscriptBadData проверяет битые данные внутри transcript.
func TestGladiaProvider_ParseTranscriptBadData(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	provider := &GladiaProvider{
		cfg:      GladiaConfig{APIKey: "test-key"},
		audioCh: make(chan []byte, 8),
		textCh:  make(chan common.STTEvent, 8),
		sessLog: logger.NewNopSessionLogger(),
	}
	provider.ctx, provider.cancel = context.WithCancel(ctx)

	// Корректная обёртка gladiaMessage, но битые данные внутри.
	msg, _ := json.Marshal(gladiaMessage{
		Type: "transcript",
		Data: json.RawMessage(`{broken}`),
	})
	provider.parseAndEmit(msg)

	select {
	case event := <-provider.textCh:
		t.Errorf("не ожидалось событие для битых данных transcript, получено %+v", event)
	case <-time.After(200 * time.Millisecond):
		// OK.
	}
}

// TestGladiaProvider_ParseTranslationBadData проверяет битые данные внутри translation.
func TestGladiaProvider_ParseTranslationBadData(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	provider := &GladiaProvider{
		cfg:      GladiaConfig{APIKey: "test-key"},
		audioCh: make(chan []byte, 8),
		textCh:  make(chan common.STTEvent, 8),
		sessLog: logger.NewNopSessionLogger(),
	}
	provider.ctx, provider.cancel = context.WithCancel(ctx)

	msg, _ := json.Marshal(gladiaMessage{
		Type: "translation",
		Data: json.RawMessage(`{broken}`),
	})
	provider.parseAndEmit(msg)

	select {
	case event := <-provider.textCh:
		t.Errorf("не ожидалось событие для битых данных translation, получено %+v", event)
	case <-time.After(200 * time.Millisecond):
		// OK.
	}
}

// TestGladiaProvider_EmitFullChannel проверяет, что emit не блокируется при заполненном канале.
func TestGladiaProvider_EmitFullChannel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Канал ёмкостью 0 — всегда полон.
	provider := &GladiaProvider{
		cfg:      GladiaConfig{APIKey: "test-key"},
		audioCh: make(chan []byte, 8),
		textCh:  make(chan common.STTEvent), // unbuffered → always full unless reader
		sessLog: logger.NewNopSessionLogger(),
	}
	provider.ctx, provider.cancel = context.WithCancel(ctx)

	// emit не должен блокироваться навсегда — попадает в default.
	provider.emit(common.STTEvent{
		Text:      "test",
		Event:     common.EventUpdate,
		ChannelID: "speaker",
		Timestamp: time.Now(),
	})

	// Проверяем что тест не завис (дошли до сюда).
}

// TestGladiaProvider_EmitContextCancelled проверяет emit при отменённом контексте.
func TestGladiaProvider_EmitContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // сразу отменяем

	provider := &GladiaProvider{
		cfg:      GladiaConfig{APIKey: "test-key"},
		audioCh: make(chan []byte, 8),
		textCh:  make(chan common.STTEvent), // никто не читает
		sessLog: logger.NewNopSessionLogger(),
	}
	provider.ctx, provider.cancel = context.WithCancel(ctx)

	// emit должен уйти в ветку ctx.Done(), не паника.
	provider.emit(common.STTEvent{
		Text:      "test",
		Event:     common.EventUpdate,
		ChannelID: "speaker",
		Timestamp: time.Now(),
	})
}

// TestGladiaProvider_NewDefaultTargetLang проверяет язык по умолчанию.
func TestGladiaProvider_NewDefaultTargetLang(t *testing.T) {
	provider := NewGladiaProvider(GladiaConfig{APIKey: "test-key"}, logger.NewNopSessionLogger())
	if provider.cfg.TargetLang != "ru" {
		t.Errorf("ожидался targetLang=ru, получен %q", provider.cfg.TargetLang)
	}
}

// TestGladiaProvider_ConcurrentStopAndEmit проверяет, что Stop() и emit не гоняются.
func TestGladiaProvider_ConcurrentStopAndEmit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	provider := &GladiaProvider{
		cfg:      GladiaConfig{APIKey: "test-key"},
		audioCh: make(chan []byte, 8),
		textCh:  make(chan common.STTEvent, 32),
		sessLog: logger.NewNopSessionLogger(),
	}
	provider.ctx, provider.cancel = context.WithCancel(ctx)

	var wg sync.WaitGroup
	stopCh := make(chan struct{})

	// Горутина 1: постоянно шлёт события.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stopCh:
				return
			default:
				provider.emit(common.STTEvent{
					Text:      "test",
					Event:     common.EventUpdate,
					ChannelID: "speaker",
					Timestamp: time.Now(),
				})
			}
		}
	}()

	// Даём поработать 50ms.
	time.Sleep(50 * time.Millisecond)

	// Горутина 2: вызывает Stop().
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = provider.Stop()
	}()

	// Даём Stop() завершиться.
	time.Sleep(50 * time.Millisecond)
	close(stopCh)
	cancel()
	wg.Wait()
}

// =========================================================================
// TestGladiaProvider_StopWaitsForPumps
// =========================================================================

// mockWSServer создаёт тестовый WebSocket-сервер, который принимает соединения
// и держит их открытыми до отмены контекста.
func mockWSServer(t *testing.T, ctx context.Context) *httptest.Server {
	t.Helper()

	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("mock ws upgrade error: %v", err)
			return
		}
		defer conn.Close()

		// Читаем до закрытия или отмены контекста.
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			_, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
		}
	}))
	return srv
}

// TestGladiaProvider_StopWaitsForPumps проверяет, что Stop() дожидается
// завершения writePump и readPump перед возвратом.
func TestGladiaProvider_StopWaitsForPumps(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	srv := mockWSServer(t, ctx)
	defer srv.Close()

	// Преобразуем http:// в ws://
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	provider := &GladiaProvider{
		cfg:      GladiaConfig{APIKey: "test-key"},
		audioCh: make(chan []byte, 64),
		textCh:  make(chan common.STTEvent, 32),
		sessLog: logger.NewNopSessionLogger(),
	}

	// Ручная инициализация (без initSession) — выставляем wsConn.
	provider.ctx, provider.cancel = context.WithCancel(ctx)
	dialer := websocket.Dialer{}
	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("не удалось подключиться к mock WS: %v", err)
	}
	provider.wsConn = conn

	// Запускаем горутины как в Start().
	provider.pumpWg.Add(2)
	go func() { defer provider.pumpWg.Done(); provider.writePump() }()
	go func() { defer provider.pumpWg.Done(); provider.readPump() }()

	// Даём горутинам немного поработать.
	time.Sleep(50 * time.Millisecond)

	// Вызываем Stop — он должен дождаться завершения pump'ов.
	stopDone := make(chan struct{})
	go func() {
		_ = provider.Stop()
		close(stopDone)
	}()

	select {
	case <-stopDone:
		// OK — Stop завершился.
	case <-time.After(3 * time.Second):
		t.Fatal("Stop() не завершился за 3 секунды — вероятно, завис на pumpWg.Wait()")
	}

	// Проверяем, что pumpWg обнулён (все горутины вышли).
	// Если Wait() вернулся, значит счётчик = 0.
}

// =========================================================================
// TestGladiaProvider_DoubleCloseWsConn
// =========================================================================

// TestGladiaProvider_DoubleCloseWsConn проверяет, что при одновременном
// завершении writePump и readPump не происходит паники из-за двойного
// закрытия wsConn (CAS-защита).
func TestGladiaProvider_DoubleCloseWsConn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	srv := mockWSServer(t, ctx)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	provider := &GladiaProvider{
		cfg:      GladiaConfig{APIKey: "test-key"},
		audioCh: make(chan []byte, 64),
		textCh:  make(chan common.STTEvent, 32),
		sessLog: logger.NewNopSessionLogger(),
	}

	provider.ctx, provider.cancel = context.WithCancel(ctx)
	dialer := websocket.Dialer{}
	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("не удалось подключиться к mock WS: %v", err)
	}
	provider.wsConn = conn

	// Сигнальные каналы для отслеживания выхода горутин.
	writeDone := make(chan struct{})
	readDone := make(chan struct{})

	// Запускаем writePump.
	provider.pumpWg.Add(1)
	go func() {
		defer provider.pumpWg.Done()
		defer close(writeDone)
		provider.writePump()
	}()

	// Запускаем readPump.
	provider.pumpWg.Add(1)
	go func() {
		defer provider.pumpWg.Done()
		defer close(readDone)
		provider.readPump()
	}()

	// Даём горутинам стартовать.
	time.Sleep(50 * time.Millisecond)

	// Симулируем ошибку writePump: закрываем audioCh, чтобы writePump вышел.
	close(provider.audioCh)

	// Ждём выхода writePump.
	select {
	case <-writeDone:
		// OK — writePump завершился.
	case <-time.After(2 * time.Second):
		t.Fatal("writePump не завершился после закрытия audioCh")
	}

	// Теперь отменяем контекст — readPump должен выйти.
	cancel()

	// Ждём выхода readPump.
	select {
	case <-readDone:
		// OK — readPump завершился.
	case <-time.After(2 * time.Second):
		t.Fatal("readPump не завершился после отмены контекста")
	}

	// Обе горутины вышли. Проверяем, что паники не было.
	// Если бы closeWsConn не был защищён CAS, вторая горутина
	// вызвала бы панику при повторном Close().
	provider.pumpWg.Wait()

	// Проверяем, что wsConn всё ещё можно безопасно закрыть через Stop.
	// (closeWsConn в Stop тоже пройдёт через CAS и не запаникует.)
	if err := provider.Stop(); err != nil {
		t.Errorf("неожиданная ошибка Stop после завершения pump'ов: %v", err)
	}
}

// TestInitSession_NoContextAdaptation проверяет, что Gladia v2 live init
// НЕ отправляет context_adaptation (не поддерживается в live-режиме).
func TestInitSession_NoContextAdaptation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-gladia-key") == "" {
			t.Error("x-gladia-key header missing")
		}

		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("failed to decode body: %v", err)
		}

		if _, ok := body["context_adaptation"]; ok {
			t.Error("context_adaptation присутствует в теле запроса — Gladia v2 live не поддерживает")
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(gladiaInitResponse{
			ID:  "test-id",
			URL: "ws://test/ws",
		})
	}))
	defer server.Close()

	origURL := gladiaBaseURL
	gladiaBaseURL = server.URL
	defer func() { gladiaBaseURL = origURL }()

	cfg := GladiaConfig{
		APIKey: "test-key",
	}
	cfg.applyDefaults()

	provider := NewGladiaProvider(cfg, nil)
	wsURL, err := provider.initSession(context.Background())
	if err != nil {
		t.Fatalf("initSession: %v", err)
	}
	if wsURL != "ws://test/ws" {
		t.Errorf("wsURL: got %q, want %q", wsURL, "ws://test/ws")
	}
}
