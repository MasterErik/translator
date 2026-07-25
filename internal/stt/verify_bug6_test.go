package stt

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mastererik/translator/internal/common"
)

// makeEagerResponse creates a Deepgram Flux EagerEndOfTurn JSON response.
func makeEagerResponse(transcript string) []byte {
	r := fluxResponse{Type: "TurnInfo", Event: "EagerEndOfTurn", Transcript: transcript}
	data, _ := json.Marshal(r)
	return data
}

func freshProvider(t *testing.T) *DeepgramProvider {
	t.Helper()
	p := &DeepgramProvider{
		apiKey:  "test-key",
		model:   "flux-general-en",
		audioCh: make(chan []byte, 8),
		textCh:  make(chan common.STTEvent, 16),
	}
	// parseAndEmit читает d.ctx.Done() в select — нужен живой контекст.
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	p.ctx, p.cancel = ctx, cancel
	return p
}

func expectEvent(t *testing.T, p *DeepgramProvider, event, text string) {
	t.Helper()
	select {
	case evt := <-p.textCh:
		if evt.Event != event {
			t.Errorf("expected Event=%q, got %q", event, evt.Event)
		}
		if evt.Text != text {
			t.Errorf("expected Text=%q, got %q", text, evt.Text)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timeout waiting for event %q/%q", event, text)
	}
}

func expectNoEvent(t *testing.T, p *DeepgramProvider, d time.Duration) {
	t.Helper()
	select {
	case evt := <-p.textCh:
		t.Errorf("expected no event, got %+v", evt)
	case <-time.After(d):
		// OK
	}
}

func drainOne(t *testing.T, p *DeepgramProvider) {
	t.Helper()
	select {
	case <-p.textCh:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout draining event")
	}
}

// TestDuplicateUpdateFiltering_BUG6 — ad-hoc verification for BUG #6.
// Проверяет:
//  1. Первый Update проходит.
//  2. Дубликат с тем же текстом — отфильтрован.
//  3. Новый текст после дубликата — проходит.
//  4. EndOfTurn сбрасывает lastInterimText.
//  5. 16 дубликатов — только первый проходит.
//  6. EagerEndOfTurn не ломается от нового фильтра.
func TestDuplicateUpdateFiltering_BUG6(t *testing.T) {

	t.Run("first_update_passes", func(t *testing.T) {
		p := freshProvider(t)
		p.parseAndEmit(makeInterimResponse("hello"))
		expectEvent(t, p, common.EventUpdate, "hello")
	})

	t.Run("duplicate_update_dropped", func(t *testing.T) {
		p := freshProvider(t)
		p.parseAndEmit(makeInterimResponse("hello")) // первый — проходит
		drainOne(t, p)
		p.parseAndEmit(makeInterimResponse("hello")) // дубликат — дроп
		expectNoEvent(t, p, 200*time.Millisecond)
	})

	t.Run("new_text_passes_after_dup", func(t *testing.T) {
		p := freshProvider(t)
		p.parseAndEmit(makeInterimResponse("hello"))
		drainOne(t, p)
		p.parseAndEmit(makeInterimResponse("hello"))       // дубликат — дроп
		p.parseAndEmit(makeInterimResponse("hello world")) // новый текст — проходит
		expectEvent(t, p, common.EventUpdate, "hello world")
	})

	t.Run("end_of_turn_resets_interim_filter", func(t *testing.T) {
		p := freshProvider(t)
		p.parseAndEmit(makeInterimResponse("phrase one"))
		expectEvent(t, p, common.EventUpdate, "phrase one")
		// EndOfTurn — сбрасывает lastInterimText.
		p.parseAndEmit(makeFinalResponse("phrase one"))
		expectEvent(t, p, common.EventEndOfTurn, "phrase one")
		// После EndOfTurn тот же текст в Update — должен пройти заново.
		p.parseAndEmit(makeInterimResponse("phrase one"))
		expectEvent(t, p, common.EventUpdate, "phrase one")
	})

	t.Run("sixteen_duplicates_only_first_passes", func(t *testing.T) {
		p := freshProvider(t)
		p.parseAndEmit(makeInterimResponse("repeating"))
		expectEvent(t, p, common.EventUpdate, "repeating")
		for i := 0; i < 15; i++ {
			p.parseAndEmit(makeInterimResponse("repeating"))
		}
		expectNoEvent(t, p, 200*time.Millisecond)
	})

	t.Run("eager_end_of_turn_not_broken", func(t *testing.T) {
		p := freshProvider(t)
		// Сначала EndOfTurn с текстом "done".
		p.parseAndEmit(makeFinalResponse("done"))
		expectEvent(t, p, common.EventEndOfTurn, "done")
		// EagerEndOfTurn с тем же текстом — дроп (существующий фильтр).
		p.parseAndEmit(makeEagerResponse("done"))
		expectNoEvent(t, p, 200*time.Millisecond)
		// А новый текст в EagerEndOfTurn — проходит.
		p.parseAndEmit(makeEagerResponse("new stuff"))
		expectEvent(t, p, common.EventEagerEndOfTurn, "new stuff")
	})
}
