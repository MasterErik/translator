package dispatcher

import (
	"sync"
	"testing"

	"github.com/mastererik/translator/internal/ui"
)

// TestQuestionTimelineConcurrent — конкурентный доступ к таймлайну под -race:
// параллельные Add (из «потока STT-событий»), Snapshot/SetState/SetAnswer
// (из «потока UI/worker»). Инвариант: без data race и без паники, после
// завершения число меток == числу успешных Add, все индексы валидны.
func TestQuestionTimelineConcurrent(t *testing.T) {
	tl := NewQuestionTimeline()

	const (
		adders  = 4
		readers = 4
		perAdd  = 25
	)
	perRead := 100

	var wg sync.WaitGroup
	wg.Add(adders + readers)

	// Писатели: Add + сразу же SetAnswer/SetState на свою метку.
	for w := 0; w < adders; w++ {
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perAdd; i++ {
				idx := tl.Add("q")
				tl.SetAnswer(idx, []ui.Answer{{Source: "en", Target: "ru", Total: idx + 1}})
				tl.SetState(idx, QReady)
			}
		}(w)
	}

	// Читатели: Snapshot/Active/OnTail/NextStep, мутации копий не должны
	// затрагивать внутренности таймлайна.
	for r := 0; r < readers; r++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perRead; i++ {
				snap := tl.Snapshot()
				for j := range snap {
					snap[j].Question = "mutated"
					snap[j].Answer = append(snap[j].Answer, ui.Answer{})
				}
				if idx, e, ok := tl.Active(); ok {
					_, _ = tl.NextStep(idx)
					_ = e.Question
				}
				_ = tl.OnTail()
				_, _ = tl.HasFresh()
			}
		}()
	}

	wg.Wait()

	// Метки не потерялись и индексы консистентны.
	snap := tl.Snapshot()
	if len(snap) != adders*perAdd {
		t.Fatalf("len(snapshot) = %d, want %d", len(snap), adders*perAdd)
	}
	for i, e := range snap {
		if e.Question != "q" {
			t.Fatalf("snap[%d].Question = %q, want %q (копия алиасит внутренности)", i, e.Question, "q")
		}
	}
}
