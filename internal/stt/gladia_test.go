package stt

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/mastererik/translator/internal/common"
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
				apiKey:  "test-key",
				audioCh: make(chan []byte, 8),
				textCh:  make(chan common.STTEvent, 8),
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
		apiKey:  "test-key",
		audioCh: make(chan []byte, 8),
		textCh:  make(chan common.STTEvent, 8),
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
		apiKey:  "test-key",
		audioCh: make(chan []byte, 8),
		textCh:  make(chan common.STTEvent, 8),
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
		apiKey:  "test-key",
		audioCh: make(chan []byte, 8),
		textCh:  make(chan common.STTEvent, 8),
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
	provider := NewGladiaProvider("test-key", "ru")

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
		apiKey:  "test-key",
		audioCh: make(chan []byte, 8),
		textCh:  make(chan common.STTEvent, 8),
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
		apiKey:  "test-key",
		audioCh: make(chan []byte, 8),
		textCh:  make(chan common.STTEvent, 8),
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
		apiKey:  "test-key",
		audioCh: make(chan []byte, 8),
		textCh:  make(chan common.STTEvent, 8),
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
		apiKey:  "test-key",
		audioCh: make(chan []byte, 8),
		textCh:  make(chan common.STTEvent, 8),
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
		apiKey:  "test-key",
		audioCh: make(chan []byte, 8),
		textCh:  make(chan common.STTEvent, 8),
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
		apiKey:  "test-key",
		audioCh: make(chan []byte, 8),
		textCh:  make(chan common.STTEvent), // unbuffered → always full unless reader
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
		apiKey:  "test-key",
		audioCh: make(chan []byte, 8),
		textCh:  make(chan common.STTEvent), // никто не читает
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
	provider := NewGladiaProvider("test-key", "")
	if provider.targetLang != "ru" {
		t.Errorf("ожидался targetLang=ru, получен %q", provider.targetLang)
	}
}

// TestGladiaProvider_ConcurrentStopAndEmit проверяет, что Stop() и emit не гоняются.
func TestGladiaProvider_ConcurrentStopAndEmit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	provider := &GladiaProvider{
		apiKey:  "test-key",
		audioCh: make(chan []byte, 8),
		textCh:  make(chan common.STTEvent, 32),
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
