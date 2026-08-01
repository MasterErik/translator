package stt

import (
	"context"
	"testing"
	"time"

	"github.com/mastererik/translator/internal/common"
)

// stub — короткий текст для быстрых тестов.
const (
	stubText        = "hello world test"
	stubTranslation = "привет мир тест"
)

// newTestEmulator создаёт эмулятор с нулевыми задержками для быстрых тестов.
func newTestEmulator() *GladiaEmulator {
	e := NewGladiaEmulator(stubText, stubTranslation, "ru")
	e.ConnectDelay = 0
	e.InterimDelay = 0
	e.FinalDelay = 0
	e.TranslationDelay = 0
	e.Jitter = 0
	e.TranslationJitter = 0
	return e
}

// readAllEvents читает все события из textCh и возвращает слайс + булев done.
func readAllEvents(ch <-chan common.STTEvent, timeout time.Duration) ([]common.STTEvent, bool) {
	var events []common.STTEvent
	deadline := time.After(timeout)
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return events, true // канал закрыт
			}
			events = append(events, evt)
		case <-deadline:
			return events, false // таймаут
		}
	}
}

// =========================================================================
// TestGladiaEmulator_StartStop
// =========================================================================

func TestGladiaEmulator_StartStop(t *testing.T) {
	e := newTestEmulator()

	ctx := context.Background()
	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if err := e.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// Повторный Stop должен быть идемпотентным.
	if err := e.Stop(); err != nil {
		t.Fatalf("second Stop failed: %v", err)
	}
}

func TestGladiaEmulator_DoubleStart(t *testing.T) {
	e := newTestEmulator()
	ctx := context.Background()

	if err := e.Start(ctx); err != nil {
		t.Fatalf("first Start failed: %v", err)
	}
	defer e.Stop()

	if err := e.Start(ctx); err == nil {
		t.Fatal("expected error on double Start, got nil")
	}
}

func TestGladiaEmulator_StartCanceledContext(t *testing.T) {
	e := NewGladiaEmulator(stubText, stubTranslation, "ru")
	// Оставляем дефолтные задержки (ненулевые), но отменяем контекст.
	e.ConnectDelay = 500 * time.Millisecond
	e.Jitter = 0

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // отменяем до Start

	err := e.Start(ctx)
	if err == nil {
		t.Fatal("expected error with canceled context, got nil")
	}
}

func TestGladiaEmulator_StopBeforeStart(t *testing.T) {
	e := newTestEmulator()

	// Stop до Start должен быть идемпотентным (nil).
	if err := e.Stop(); err != nil {
		t.Fatalf("Stop before Start should be no-op: %v", err)
	}
}

// =========================================================================
// TestGladiaEmulator_Events
// =========================================================================

func TestGladiaEmulator_EventsSequence(t *testing.T) {
	e := newTestEmulator()
	ctx := context.Background()

	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer e.Stop()

	// Отправляем достаточно аудио (~1 секунда).
	audioCh := e.AudioStream()
	chunk := make([]byte, 2560) // 80ms фрейм @ 16kHz
	for i := 0; i < 13; i++ {  // 13 × 80ms = 1040ms
		audioCh <- chunk
	}

	events, done := readAllEvents(e.TextStream(), 3*time.Second)
	if !done {
		t.Fatal("textCh should be closed after events, but timed out")
	}

	// Проверяем, что получили минимум 2 interim + 1 final + 1 translation = 4 события.
	if len(events) < 4 {
		t.Fatalf("expected at least 4 events, got %d: %+v", len(events), events)
	}

	// Проверяем первое interim-событие.
	if events[0].Event != common.EventUpdate {
		t.Errorf("expected first event Update, got %s", events[0].Event)
	}
	if events[0].ChannelID != "speaker" {
		t.Errorf("expected first event ChannelID=speaker, got %s", events[0].ChannelID)
	}

	// Проверяем финальное событие (должно быть перед переводом).
	foundFinal := false
	foundTranslation := false
	for _, evt := range events {
		if evt.Event == common.EventEndOfTurn && evt.ChannelID == "speaker" {
			foundFinal = true
			if evt.Text != stubText {
				t.Errorf("final text mismatch: got %q, want %q", evt.Text, stubText)
			}
		}
		if evt.Event == common.EventEndOfTurn && evt.ChannelID == "translation" {
			foundTranslation = true
			if evt.Text != stubTranslation {
				t.Errorf("translation text mismatch: got %q, want %q", evt.Text, stubTranslation)
			}
		}
	}
	if !foundFinal {
		t.Error("final speaker event not found")
	}
	if !foundTranslation {
		t.Error("translation event not found")
	}
}

func TestGladiaEmulator_TextStreamClosedAfterStop(t *testing.T) {
	e := newTestEmulator()
	ctx := context.Background()

	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Отправляем аудио.
	audioCh := e.AudioStream()
	chunk := make([]byte, 2560)
	for i := 0; i < 13; i++ {
		audioCh <- chunk
	}

	// Читаем до закрытия.
	_, done := readAllEvents(e.TextStream(), 3*time.Second)
	if !done {
		t.Error("textCh should be closed after events")
	}

	// После Stop канал уже закрыт, но читать из закрытого можно.
	if err := e.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
}

func TestGladiaEmulator_CtxCancelStopsEvents(t *testing.T) {
	e := NewGladiaEmulator(stubText, stubTranslation, "ru")
	e.ConnectDelay = 0
	e.Jitter = 0
	// Большие задержки, чтобы успеть отменить контекст.
	e.InterimDelay = 2 * time.Second
	e.FinalDelay = 2 * time.Second
	e.TranslationDelay = 2 * time.Second

	ctx, cancel := context.WithCancel(context.Background())

	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Отправляем аудио.
	audioCh := e.AudioStream()
	chunk := make([]byte, 2560)
	for i := 0; i < 13; i++ {
		audioCh <- chunk
	}

	// Даём время на первое interim событие.
	time.Sleep(50 * time.Millisecond)

	// Отменяем контекст.
	cancel()

	// Ждём закрытия канала.
	events, done := readAllEvents(e.TextStream(), 500*time.Millisecond)
	if !done {
		t.Fatal("textCh should be closed after context cancel")
	}

	// Должны получить только первое interim (до cancel).
	// Может быть 0-1 событий в зависимости от таймингов.
	t.Logf("events after cancel: %d", len(events))
	if len(events) > 3 {
		t.Errorf("too many events after cancel: %d", len(events))
	}
}

func TestGladiaEmulator_EmptyText(t *testing.T) {
	e := NewGladiaEmulator("", "", "ru")
	e.ConnectDelay = 0
	e.Jitter = 0

	ctx := context.Background()
	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer e.Stop()

	audioCh := e.AudioStream()
	chunk := make([]byte, 2560)
	for i := 0; i < 13; i++ {
		audioCh <- chunk
	}

	events, done := readAllEvents(e.TextStream(), 2*time.Second)
	if !done {
		t.Fatal("textCh should be closed")
	}

	// При пустом тексте events не генерируются.
	if len(events) != 0 {
		t.Errorf("expected 0 events for empty text, got %d", len(events))
	}
}

// =========================================================================
// TestGladiaEmulator_DrainAudioTimeout
// =========================================================================

// TestGladiaEmulator_DrainAudioTimeout проверяет, что эмулятор не зависает
// (deadlock) когда отправлено ровно minAudioBytes (32000) и больше аудио не поступает.
// Без фикса drainAudio блокируется навсегда на чтении из audioCh,
// потому что канал открыт, но пуст, а eventsDone ещё не закрыт.
func TestGladiaEmulator_DrainAudioTimeout(t *testing.T) {
	e := newTestEmulator()
	ctx := context.Background()

	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer e.Stop()

	// Отправляем ровно minAudioBytes (32000) — достаточно для фазы 1,
	// но без избытка, чтобы drainAudio не получил лишних чанков.
	audioCh := e.AudioStream()
	chunk := make([]byte, 3200) // 100ms @ 16kHz
	for i := 0; i < 10; i++ {   // 10 × 3200 = 32000 = minAudioBytes
		audioCh <- chunk
	}

	// Ждём закрытия textCh с разумным таймаутом.
	// С нулевыми задержками должно завершиться быстро.
	_, done := readAllEvents(e.TextStream(), 5*time.Second)
	if !done {
		t.Fatal("textCh should be closed — possible deadlock in drainAudio")
	}
	t.Log("textCh closed successfully, no deadlock")
}

// =========================================================================
// TestGladiaEmulator_Jitter
// =========================================================================

func TestJitter_NoJitter(t *testing.T) {
	for i := 0; i < 20; i++ {
		got := jitter(100*time.Millisecond, 0)
		if got != 100*time.Millisecond {
			t.Errorf("expected exact base with zero jitter, got %v", got)
		}
	}
}

func TestJitter_Range(t *testing.T) {
	base := 300 * time.Millisecond
	jit := 100 * time.Millisecond
	minVal := base - jit
	maxVal := base + jit

	for i := 0; i < 100; i++ {
		got := jitter(base, jit)
		if got < minVal {
			t.Errorf("jitter below minimum: %v < %v", got, minVal)
		}
		if got > maxVal {
			t.Errorf("jitter above maximum: %v > %v", got, maxVal)
		}
	}
}

func TestJitter_ZeroBase(t *testing.T) {
	// При base=0, jitter должен вернуть > 0 (минимум 1ms).
	got := jitter(0, 100*time.Millisecond)
	if got <= 0 {
		t.Errorf("expected positive jitter with zero base, got %v", got)
	}
}

// =========================================================================
// TestGladiaEmulator_Interface
// =========================================================================

func TestGladiaEmulator_ImplementsSTTProvider(t *testing.T) {
	// Compile-time check уже есть в emulator.go.
	// Этот тест — рантайм-проверка.
	var prov STTProvider = &GladiaEmulator{}
	_ = prov
}

// =========================================================================
// TestGladiaEmulator_AudioStreamWrite
// =========================================================================

func TestGladiaEmulator_AudioStreamNotBlocked(t *testing.T) {
	e := newTestEmulator()
	ctx := context.Background()

	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer e.Stop()

	audioCh := e.AudioStream()
	chunk := make([]byte, 2560)

	// Отправляем много чанков — не должно блокироваться,
	// так как буфер канала 64.
	for i := 0; i < 20; i++ {
		select {
		case audioCh <- chunk:
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("audioCh blocked at chunk %d", i)
		}
	}
}

// =========================================================================
// TestGladiaEmulator_ConcurrencySafety
// =========================================================================

func TestGladiaEmulator_ConcurrentStartStop(t *testing.T) {
	e := newTestEmulator()
	ctx := context.Background()

	done := make(chan struct{})
	go func() {
		_ = e.Start(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not complete")
	}

	// Параллельные Stop.
	go e.Stop()
	go e.Stop()
	time.Sleep(100 * time.Millisecond)
}
