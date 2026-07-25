//go:build ignore
// +build ignore

package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mastererik/translator/internal/common"
	"github.com/mastererik/translator/internal/translator"
)

// --------------------------------------------------------------------------
// countingMockLLM — мок LLM-провайдера, подсчитывающий вызовы Translate.
// --------------------------------------------------------------------------

type countingMockLLM struct {
	mu           sync.Mutex
	translations map[string]string
	callCount    int
	callOrder    []string // порядок обработанных текстов
}

func newCountingMockLLM() *countingMockLLM {
	return &countingMockLLM{
		translations: make(map[string]string),
		callOrder:    make([]string, 0),
	}
}

func (m *countingMockLLM) Translate(ctx context.Context, text string, history []string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.callCount++
	m.callOrder = append(m.callOrder, text)

	if t, ok := m.translations[text]; ok {
		return t, nil
	}
	return "translated: " + text, nil
}

func (m *countingMockLLM) GenerateAnswers(ctx context.Context, question string, cvContext string) ([]string, error) {
	return []string{"a1", "a2"}, nil
}

func (m *countingMockLLM) getCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}

func (m *countingMockLLM) getCallOrder() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.callOrder))
	copy(out, m.callOrder)
	return out
}

var _ translator.LLMProvider = (*countingMockLLM)(nil)

// --------------------------------------------------------------------------
// errorMockLLM — мок, возвращающий ошибку на Translate.
// --------------------------------------------------------------------------

type errorMockLLM struct {
	err error
}

func newErrorMockLLM(err error) *errorMockLLM {
	return &errorMockLLM{err: err}
}

func (m *errorMockLLM) Translate(ctx context.Context, text string, history []string) (string, error) {
	return "", m.err
}

func (m *errorMockLLM) GenerateAnswers(ctx context.Context, question string, cvContext string) ([]string, error) {
	return nil, m.err
}

var _ translator.LLMProvider = (*errorMockLLM)(nil)

// --------------------------------------------------------------------------
// TestWorkerPool — проверяет параллельную обработку N=3 заданий.
// Все задания должны быть обработаны (каждое один раз).
// --------------------------------------------------------------------------

func TestWorkerPool(t *testing.T) {
	llm := newCountingMockLLM()
	engine := translator.NewEngine(llm, 5)

	numWorkers := 3
	jobs := make(chan transJob, numWorkers*2)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	transResults := startWorkers(ctx, engine, jobs, numWorkers)

	// Отправляем 6 заданий (по 2 на каждого worker'а при N=3).
	numJobs := 6
	for i := 0; i < numJobs; i++ {
		jobs <- transJob{
			event: common.STTEvent{
				Text:      fmt.Sprintf("text %d", i),
				Event:     common.EventEndOfTurn,
				ChannelID: "speaker",
				Timestamp: time.Now(),
			},
		}
	}
	close(jobs)

	// Собираем результаты.
	results := make([]transResult, 0, numJobs)
	for tr := range transResults {
		results = append(results, tr)
	}

	// Проверяем, что все задания обработаны.
	if len(results) != numJobs {
		t.Errorf("ожидалось %d результатов, получено %d", numJobs, len(results))
	}

	callCount := llm.getCallCount()
	if callCount != numJobs {
		t.Errorf("ожидалось %d вызовов Translate, получено %d", numJobs, callCount)
	}

	// Проверяем, что нет ошибок.
	for _, tr := range results {
		if tr.err != nil {
			t.Errorf("неожиданная ошибка для текста %q: %v", tr.event.Text, tr.err)
		}
		if tr.translation == "" {
			t.Errorf("пустой перевод для текста %q", tr.event.Text)
		}
	}
}

// --------------------------------------------------------------------------
// TestWorkerErrorHandling — проверяет, что worker корректно обрабатывает
// ошибки перевода и передаёт их в результат.
// --------------------------------------------------------------------------

func TestWorkerErrorHandling(t *testing.T) {
	testErr := fmt.Errorf("тестовая ошибка API")
	llm := newErrorMockLLM(testErr)
	engine := translator.NewEngine(llm, 5)

	jobs := make(chan transJob, 2)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	transResults := startWorkers(ctx, engine, jobs, 2)

	// Отправляем задание.
	jobs <- transJob{
		event: common.STTEvent{
			Text:      "error test",
			Event:     common.EventEndOfTurn,
			ChannelID: "speaker",
			Timestamp: time.Now(),
		},
	}
	close(jobs)

	// Читаем результат.
	select {
	case tr, ok := <-transResults:
		if !ok {
			t.Fatal("transResults закрыт до получения результата")
		}
		if tr.err == nil {
			t.Error("ожидалась ошибка в результате, получен nil")
		} else if !strings.Contains(tr.err.Error(), testErr.Error()) {
			t.Errorf("ошибка: %v, ожидалось содержать: %v", tr.err, testErr)
		}
		if tr.translation != "" {
			t.Errorf("перевод должен быть пустым при ошибке, получено: %q", tr.translation)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("таймаут ожидания результата")
	}

	// Проверяем, что канал закрыт после drain.
	for range transResults {
		// drain
	}
}

// --------------------------------------------------------------------------
// TestWorkerGoroutineCleanup — проверяет, что все worker-горутины
// корректно завершаются при закрытии канала заданий.
// --------------------------------------------------------------------------

func TestWorkerGoroutineCleanup(t *testing.T) {
	llm := newCountingMockLLM()
	engine := translator.NewEngine(llm, 5)

	numWorkers := 5
	jobs := make(chan transJob)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_ = startWorkers(ctx, engine, jobs, numWorkers)

	// Закрываем jobs без отправки заданий.
	close(jobs)

	// Ждём — если есть утечка, тест зависнет на таймауте.
	time.Sleep(200 * time.Millisecond)

	// Проверяем, что вызовов не было.
	if callCount := llm.getCallCount(); callCount != 0 {
		t.Errorf("ожидалось 0 вызовов Translate, получено %d", callCount)
	}
}
