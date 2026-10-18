package dispatcher

import (
	"testing"
	"time"
)

// Регрессионный контракт: повторный авто-вопрос с тем же текстом не должен
// оставлять метку в QQueued без ответа и не должен повторно дёргать LLM —
// последний готовый ответ копируется в новую метку.
//
// Повод: лог сессии session_2026-10-18_20-14-56.csv — вторая одинаковая фраза
// отсекалась дедупликацией в answerWorker, метка зависала в QQueued.
func TestAnswerWorkerDuplicateQuestionCopiesAnswer(t *testing.T) {
	cfg := DefaultConfig()
	auto := true
	cfg.AutoAnswer = &auto

	const q = "Tell us a bit about yourself and your experience."

	engine := &mockEngine{answers: []string{"EN: five years of Go | RU: пять лет с Go"}}
	d, _, textStream, cancel := startDispatcherWithOverlay(t, engine, cfg)
	defer cancel()

	// Первый вопрос — обычная генерация.
	textStream <- question(q)
	waitFor(t, func() bool {
		snap := d.timeline.Snapshot()
		return len(snap) == 1 && snap[0].State == QReady
	}, 2*time.Second)
	waitIdle(t, d)

	// Второй — точный повтор: ответ копируется из предыдущей метки.
	textStream <- question(q)
	waitFor(t, func() bool {
		snap := d.timeline.Snapshot()
		return len(snap) == 2 && snap[1].State == QReady
	}, 2*time.Second)
	waitIdle(t, d)

	if calls := engine.Calls(); len(calls) != 1 {
		t.Fatalf("engine.Calls() = %d, ожидался 1 (повтор не должен вызывать LLM): %v", len(calls), calls)
	}

	snap := d.timeline.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("len(Snapshot()) = %d, ожидалось 2", len(snap))
	}
	for i, e := range snap {
		if e.State != QReady {
			t.Errorf("метка %d: State = %v, ожидалось QReady", i, e.State)
		}
	}
	if len(snap[1].Answer) == 0 {
		t.Fatalf("ответ второй метки пуст: %+v", snap[1])
	}
	if len(snap[1].Answer) != len(snap[0].Answer) {
		t.Fatalf("len(Answer) второй = %d, первой = %d", len(snap[1].Answer), len(snap[0].Answer))
	}
	for i := range snap[0].Answer {
		if got, want := snap[1].Answer[i], snap[0].Answer[i]; got.Source != want.Source || got.Target != want.Target {
			t.Errorf("Answer[%d] второй метки = %+v, ожидалась копия %+v", i, got, want)
		}
	}
	if snap[1].Question != q {
		t.Errorf("Question второй метки = %q, ожидалось %q", snap[1].Question, q)
	}
}
