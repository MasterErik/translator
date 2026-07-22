package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mastererik/translator/internal/common"
	"github.com/mastererik/translator/internal/translator"
	"github.com/mastererik/translator/internal/ui"
)

// --------------------------------------------------------------------------
// slowMockLLM — мок LLM-провайдера с настраиваемой задержкой.
// Позволяет симулировать медленный API-вызов для проверки того, что
// interim-события не блокируются переводом.
// --------------------------------------------------------------------------

type slowMockLLM struct {
	mu           sync.Mutex
	translations map[string]string
	delay        time.Duration
}

func newSlowMockLLM(delay time.Duration) *slowMockLLM {
	return &slowMockLLM{
		translations: make(map[string]string),
		delay:        delay,
	}
}

func (m *slowMockLLM) Translate(ctx context.Context, text string, history []string) (string, error) {
	// Симулируем длительный сетевой вызов.
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(m.delay):
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.translations[text]; ok {
		return t, nil
	}
	return "translated: " + text, nil
}

func (m *slowMockLLM) GenerateAnswers(ctx context.Context, question string, cvContext string) ([]string, error) {
	return []string{"answer 1", "answer 2"}, nil
}

var _ translator.LLMProvider = (*slowMockLLM)(nil)

// --------------------------------------------------------------------------
// TestInterimDuringTranslation — проверяет, что поток interim-событий
// не блокируется во время выполнения перевода worker'ом.
// --------------------------------------------------------------------------

func TestInterimDuringTranslation(t *testing.T) {
	// Медленный LLM: 500ms на перевод.
	llm := newSlowMockLLM(500 * time.Millisecond)
	llm.translations["final text"] = "финальный перевод"

	engine := translator.NewEngine(llm, 3)
	overlay := newStubOverlay()

	textStream := make(chan common.STTEvent, 16)
	numWorkers := 1

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Запускаем dispatch в фоне.
	go runDispatch(ctx, textStream, overlay, engine, numWorkers)

	// Отправляем final-событие — worker начнёт перевод (500ms).
	textStream <- common.STTEvent{
		Text:      "final text",
		IsFinal:   true,
		ChannelID: "speaker",
		Timestamp: time.Now(),
	}

	// Сразу отправляем несколько interim-событий.
	for i := 0; i < 3; i++ {
		textStream <- common.STTEvent{
			Text:      "interim text",
			IsFinal:   false,
			ChannelID: "speaker",
			Timestamp: time.Now(),
		}
	}

	// Ждём завершения перевода + небольшой запас.
	time.Sleep(800 * time.Millisecond)

	msgs := overlay.GetMessages()
	t.Logf("получено %d сообщений", len(msgs))

	// Должны быть: 3 interim + 1 перевод = минимум 4 сообщения.
	if len(msgs) < 4 {
		t.Errorf("ожидалось ≥4 сообщений, получено %d", len(msgs))
	}

	// Проверяем, что все interim пришли.
	var interimCount int
	for _, msg := range msgs {
		if msg.Type == ui.Translation && msg.Text == "interim text" {
			interimCount++
		}
	}
	if interimCount < 3 {
		t.Errorf("ожидалось 3 interim-сообщения, получено %d", interimCount)
	}

	// Проверяем, что перевод получен.
	var foundTranslation bool
	for _, msg := range msgs {
		if msg.Type == ui.Translation && msg.Text == "финальный перевод" {
			foundTranslation = true
			break
		}
	}
	if !foundTranslation {
		t.Error("перевод не получен")
	}

	// Проверяем, что interim появились ПЕРЕД переводом
	// (или как минимум все interim раньше перевода).
	var lastInterimIdx int = -1
	var transIdx int = -1
	for i, msg := range msgs {
		if msg.Text == "interim text" {
			lastInterimIdx = i
		}
		if msg.Text == "финальный перевод" {
			transIdx = i
		}
	}

	if transIdx >= 0 && lastInterimIdx >= 0 && transIdx <= lastInterimIdx {
		t.Errorf("перевод (idx=%d) появился раньше или одновременно с последним interim (idx=%d) — блокировка!",
			transIdx, lastInterimIdx)
	} else if transIdx >= 0 && lastInterimIdx >= 0 {
		t.Logf("OK: последний interim (idx=%d) был до перевода (idx=%d)", lastInterimIdx, transIdx)
	}
}

// --------------------------------------------------------------------------
// TestDispatchShutdown — проверяет graceful shutdown dispatch-узла:
// все горутины завершаются, оставшиеся результаты drain'ятся.
// --------------------------------------------------------------------------

func TestDispatchShutdown(t *testing.T) {
	llm := newSlowMockLLM(100 * time.Millisecond)
	llm.translations["shutdown test"] = "тест остановки"

	engine := translator.NewEngine(llm, 3)
	overlay := newStubOverlay()

	textStream := make(chan common.STTEvent, 16)
	numWorkers := 2

	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		runDispatch(ctx, textStream, overlay, engine, numWorkers)
	}()

	// Отправляем одно final-событие.
	textStream <- common.STTEvent{
		Text:      "shutdown test",
		IsFinal:   true,
		ChannelID: "speaker",
		Timestamp: time.Now(),
	}

	// Даём время на обработку.
	time.Sleep(300 * time.Millisecond)

	// Отменяем контекст — dispatch должен завершиться.
	cancel()

	// Ждём завершения dispatch.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Log("dispatch завершился корректно")
	case <-time.After(5 * time.Second):
		t.Fatal("dispatch не завершился в течение таймаута — возможна утечка горутин")
	}

	// Проверяем, что результат перевода получен.
	msgs := overlay.GetMessages()
	var foundTranslation bool
	for _, msg := range msgs {
		if msg.Type == ui.Translation && msg.Text == "тест остановки" {
			foundTranslation = true
			break
		}
	}
	if !foundTranslation {
		t.Error("перевод не получен после shutdown")
	}
}
