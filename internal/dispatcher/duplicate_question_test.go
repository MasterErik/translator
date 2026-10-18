package dispatcher

import (
	"testing"
	"time"
)

// Регрессионный контракт: повторный авто-вопрос с тем же текстом НЕ создаёт
// новую метку таймлайна (дубликат отбрасывается в route до enqueueQuestion) и
// не повторяет вызов LLM.
//
// Повод: повторный ввод одного вопроса плодил лишние метки в ленте.
func TestDuplicateQuestionDoesNotCreateNewMarker(t *testing.T) {
	cfg := DefaultConfig()
	auto := true
	cfg.AutoAnswer = &auto

	const q = "Tell us a bit about yourself and your experience."

	engine := &mockEngine{answers: []string{"EN: five years of Go | RU: пять лет с Go"}}
	d, _, textStream, cancel := startDispatcherWithOverlay(t, engine, cfg)
	defer cancel()

	// Первый вопрос — создаёт единственную метку и запускает генерацию.
	textStream <- question(q)
	waitFor(t, func() bool {
		snap := d.timeline.Snapshot()
		return len(snap) == 1 && snap[0].State == QReady
	}, 2*time.Second)
	waitIdle(t, d)

	// Второй — точный повтор: метка НЕ создаётся, LLM НЕ вызывается повторно.
	textStream <- question(q)
	// Даём dispatcher обработать событие (дубликат отбрасывается в route).
	time.Sleep(200 * time.Millisecond)
	waitIdle(t, d)

	if calls := engine.Calls(); len(calls) != 1 {
		t.Fatalf("engine.Calls() = %d, ожидался 1 (повтор не вызывает LLM): %v", len(calls), calls)
	}

	if snap := d.timeline.Snapshot(); len(snap) != 1 {
		t.Fatalf("len(Snapshot()) = %d, ожидалось 1 (дубликат не создаёт метку)", len(snap))
	}
}
