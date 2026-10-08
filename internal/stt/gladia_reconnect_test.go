package stt

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/mastererik/translator/internal/common"
	"github.com/mastererik/translator/internal/logger"
)

// --------------------------------------------------------------------------
// Fake Gladia-сервер для тестов реконнекта
// --------------------------------------------------------------------------

// reconnectTestServer — фейковый Gladia-эндпоинт, который:
//   - на POST /v2/live отдаёт URL реального WS-эндпоинта того же сервера;
//   - на WS-соединении №1 отправляет одно событие и рвёт сокет (эмуляция
//     сетевого обрыва);
//   - на WS-соединении №2+ отправляет событие «после реконнекта» и держит
//     сокет открытым.
//
// Именно реальный обрыв сокета (не мок supervisor'а) проверяет реконнект.
type reconnectTestServer struct {
	srv      *httptest.Server
	upgrader websocket.Upgrader

	connCount atomic.Int32 // сколько раз сервер принял WS-соединение
	postCount atomic.Int32 // сколько раз сервер получил POST /v2/live

	// firstEventSent / postEventSent — сигналы наблюдателю теста.
	firstEventSent chan struct{}
	postEventSent  chan struct{}

	t *testing.T
}

func newReconnectTestServer(t *testing.T) *reconnectTestServer {
	ts := &reconnectTestServer{
		upgrader:       websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
		firstEventSent: make(chan struct{}),
		postEventSent:  make(chan struct{}),
		t:              t,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v2/live", ts.handleInit)
	mux.HandleFunc("/ws", ts.handleWS)
	ts.srv = httptest.NewServer(mux)
	return ts
}

func (ts *reconnectTestServer) close() { ts.srv.Close() }

// wsURL возвращает ws:// URL WS-эндпоинта этого сервера.
func (ts *reconnectTestServer) wsURL() string {
	return "ws" + strings.TrimPrefix(ts.srv.URL, "http") + "/ws"
}

// handleInit отвечает на POST /v2/live, возвращая URL собственного WS.
func (ts *reconnectTestServer) handleInit(w http.ResponseWriter, r *http.Request) {
	ts.postCount.Add(1)
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(gladiaInitResponse{
		ID:  "reconnect-test-session",
		URL: ts.wsURL(),
	})
}

// handleWS принимает WS; на первом соединении отправляет событие и рвёт сокет.
func (ts *reconnectTestServer) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := ts.upgrader.Upgrade(w, r, nil)
	if err != nil {
		ts.t.Logf("reconnect test: upgrade error: %v", err)
		return
	}

	n := ts.connCount.Add(1)
	defer conn.Close()

	if n == 1 {
		// Первое соединение: отправляем событие, затем ЖЁСТКО рвём сокет,
		// не отправляя close-фрейм (эмуляция сетевого обрыва).
		_ = conn.WriteMessage(websocket.TextMessage, makeTranscriptJSON("before-break", true))
		close(ts.firstEventSent)
		// Закрываем TCP немедленно через CloseControl/underlying close —
		// conn.Close() в defer закроет без close-handshake.
		_ = conn.UnderlyingConn().Close()
		return
	}

	// Второе (и последующие) соединение: событие «после реконнекта» + держим.
	_ = conn.WriteMessage(websocket.TextMessage, makeTranscriptJSON("after-reconnect", true))
	close(ts.postEventSent)

	// Читаем до закрытия сокета/контекста, чтобы сервер не завершал хендлер.
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

// feedAudio — непрерывно шлёт аудио-фреймы в провайдер, пока жив ctx.
// Реалистично для пайплайна (audio идёт постоянно) и заставляет writePump
// заметить мёртвое соединение сразу после обрыва.
func feedAudio(ctx context.Context, audio chan<- []byte) {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	frame := []byte{0x00, 0x01}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			select {
			case audio <- frame:
			default:
			}
		}
	}
}

// --------------------------------------------------------------------------
// Тесты
// --------------------------------------------------------------------------

// TestGladiaProvider_ReconnectsAfterSocketBreak — основной сценарий B1:
// сервер подключён → рвёт сокет → провайдер (supervisor) переподключается
// и события снова доходят до textCh. Проверяется РЕАЛЬНЫЙ обрыв и РЕАЛЬНЫЙ
// повторный коннект (POST /v2/live дважды + два WS-соединения).
func TestGladiaProvider_ReconnectsAfterSocketBreak(t *testing.T) {
	// Ускоряем backoff для теста (пакетные переменные — тест в том же пакете).
	restore := fastReconnectParams()
	defer restore()

	srv := newReconnectTestServer(t)
	defer srv.close()

	origURL := gladiaBaseURL
	gladiaBaseURL = srv.srv.URL + "/v2/live"
	defer func() { gladiaBaseURL = origURL }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	provider := NewGladiaProvider(GladiaConfig{APIKey: "test-key", TargetLang: "ru"}, logger.NewNopSessionLogger())

	if err := provider.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = provider.Stop() }()

	go feedAudio(ctx, provider.AudioStream())

	// 1. Первое событие («before-break») должно прийти через textCh.
	waitForEventText(t, provider.TextStream(), "before-break", 5*time.Second)

	// 2. Сервер рвёт сокет (первое соединение уже закрыто в handleWS).
	// Ждём событие «after-reconnect» — доказательство успешного реконнекта.
	waitForEventText(t, provider.TextStream(), "after-reconnect", 5*time.Second)

	// 3. Проверяем, что реконнект был РЕАЛЬНЫМ: ≥2 POST и ≥2 WS-соединения.
	if got := srv.postCount.Load(); got < 2 {
		t.Errorf("ожидалось ≥2 POST /v2/live (повторный initSession), получено %d", got)
	}
	if got := srv.connCount.Load(); got < 2 {
		t.Errorf("ожидалось ≥2 WS-соединений (реконнект), получено %d", got)
	}

	// 4. Stop() должен корректно дождаться supervisor'а и pump'ов.
	stopDone := make(chan struct{})
	go func() {
		_ = provider.Stop()
		close(stopDone)
	}()
	select {
	case <-stopDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop() не завершился за 5s — supervisor/pumpWg не дренированы")
	}
}

// TestGladiaProvider_StopDuringReconnect_NoReconnect — Stop() во время обрыва
// должен завершиться немедленно, без попыток реконнекта.
func TestGladiaProvider_StopDuringReconnect_NoReconnect(t *testing.T) {
	restore := fastReconnectParams()
	defer restore()

	srv := newReconnectTestServer(t)
	defer srv.close()

	origURL := gladiaBaseURL
	gladiaBaseURL = srv.srv.URL + "/v2/live"
	defer func() { gladiaBaseURL = origURL }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	provider := NewGladiaProvider(GladiaConfig{APIKey: "test-key", TargetLang: "ru"}, logger.NewNopSessionLogger())

	if err := provider.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Ждём, пока сервер отправит первое событие и оборвёт сокет.
	select {
	case <-srv.firstEventSent:
	case <-time.After(5 * time.Second):
		t.Fatal("сервер не отправил первое событие / не оборвал сокет")
	}

	// Немедленно останавливаем провайдера.
	stopDone := make(chan struct{})
	go func() {
		_ = provider.Stop()
		close(stopDone)
	}()

	select {
	case <-stopDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop() завис при активном supervisor")
	}

	// Даём немного времени: если бы реконнект пытался произойти, postCount рос.
	time.Sleep(150 * time.Millisecond)
	if got := srv.postCount.Load(); got > 1 {
		t.Errorf("после Stop() не должно быть повторных POST, получено %d", got)
	}
	if got := srv.connCount.Load(); got > 1 {
		t.Errorf("после Stop() не должно быть повторных WS-соединений, получено %d", got)
	}
}

// TestGladiaProvider_GivesUpAfterMaxAttempts — если реконнект невозможен, после
// gladiaMaxReconnectAttempts попыток supervisor эмитит EventError в textCh.
func TestGladiaProvider_GivesUpAfterMaxAttempts(t *testing.T) {
	restore := fastReconnectParams()
	defer restore()

	// Сервер: первый POST /v2/live отдаёт валидный WS-URL (Start проходит),
	// все последующие — нерабочий URL, поэтому Dial падает на КАЖДОЙ попытке
	// реконнекта. Так исчерпание попыток детерминировано (без гонки с портами).
	srv := newFailAfterFirstServer()
	defer srv.close()

	origURL := gladiaBaseURL
	gladiaBaseURL = srv.srv.URL + "/v2/live"
	defer func() { gladiaBaseURL = origURL }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	provider := NewGladiaProvider(GladiaConfig{APIKey: "test-key", TargetLang: "ru"}, logger.NewNopSessionLogger())
	if err := provider.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = provider.Stop() }()

	// Первое соединение установлено и оборвано самим сервером (handleWS).
	select {
	case <-srv.firstConnected:
	case <-time.After(5 * time.Second):
		t.Fatal("сервер не принял первое WS-соединение")
	}

	// Ждём EventError из textCh (UI зона 3).
	deadline := time.After(8 * time.Second)
	for {
		select {
		case evt := <-provider.TextStream():
			if evt.Event == common.EventError {
				if evt.Error == nil {
					t.Error("EventError получен, но поле Error == nil")
				}
				return // успех
			}
		case <-deadline:
			t.Fatal("EventError не получен после исчерпания попыток реконнекта")
		}
	}
}

// failAfterFirstServer: первый POST отдаёт валидный WS-URL, последующие —
// нерабочий (Dial всегда падает). Реконнект исчерпает попытки детерминированно.
type failAfterFirstServer struct {
	srv            *httptest.Server
	postCount      atomic.Int32
	firstConnected chan struct{}
	upgrader       websocket.Upgrader
}

func newFailAfterFirstServer() *failAfterFirstServer {
	f := &failAfterFirstServer{
		firstConnected: make(chan struct{}),
		upgrader:       websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v2/live", func(w http.ResponseWriter, r *http.Request) {
		n := f.postCount.Add(1)
		w.WriteHeader(http.StatusCreated)
		url := f.wsURL()
		if n > 1 {
			// Порт 1 — гарантированно нерабочий адрес для Dial.
			url = "ws://127.0.0.1:1/ws"
		}
		_ = json.NewEncoder(w).Encode(gladiaInitResponse{ID: "fail", URL: url})
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := f.upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		close(f.firstConnected)
		// Рвём сокет сразу, чтобы readPump вышел и supervisor пошёл на реконнект.
		_ = conn.UnderlyingConn().Close()
	})
	f.srv = httptest.NewServer(mux)
	return f
}

func (f *failAfterFirstServer) wsURL() string {
	return "ws" + strings.TrimPrefix(f.srv.URL, "http") + "/ws"
}

func (f *failAfterFirstServer) close() { f.srv.Close() }

// --------------------------------------------------------------------------
// Хелперы
// --------------------------------------------------------------------------

// fastReconnectParams ускоряет backoff для теста и возвращает функцию
// восстановления исходных значений.
func fastReconnectParams() func() {
	origInitial := gladiaReconnectInitial
	origFactor := gladiaReconnectFactor
	origMax := gladiaReconnectMax
	origJitter := gladiaReconnectJitter

	gladiaReconnectInitial = 10 * time.Millisecond
	gladiaReconnectFactor = 2.0
	gladiaReconnectMax = 50 * time.Millisecond
	gladiaReconnectJitter = 0

	return func() {
		gladiaReconnectInitial = origInitial
		gladiaReconnectFactor = origFactor
		gladiaReconnectMax = origMax
		gladiaReconnectJitter = origJitter
	}
}

// waitForEventText ждёт из textCh событие с заданным Text.
func waitForEventText(t *testing.T, ch <-chan common.STTEvent, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case evt := <-ch:
			if evt.Text == want {
				return
			}
			// Игнорируем прочие события (interim и т.п.).
		case <-deadline:
			t.Fatalf("таймаут ожидания события с Text=%q", want)
		}
	}
}
