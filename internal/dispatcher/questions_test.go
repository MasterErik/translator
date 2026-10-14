package dispatcher

import (
	"testing"

	"github.com/mastererik/translator/internal/translator"
	"github.com/mastererik/translator/internal/ui"
)

// --- пустой таймлайн ---

func TestQuestionTimeline_Empty(t *testing.T) {
	tl := NewQuestionTimeline()

	if _, _, ok := tl.Active(); ok {
		t.Fatalf("Active() на пустом таймлайне: ok = true, ожидалось false")
	}
	if tl.Prev() {
		t.Fatalf("Prev() на пустом таймлайне: true, ожидалось false")
	}
	if tl.Next() {
		t.Fatalf("Next() на пустом таймлайне: true, ожидалось false")
	}
	if !tl.OnTail() {
		t.Fatalf("OnTail() на пустом таймлайне: false, ожидалось true")
	}
	if idx, ok := tl.HasFresh(); ok {
		t.Fatalf("HasFresh() на пустом таймлайне: ok = true (idx=%d), ожидалось false", idx)
	}
	if got := tl.Snapshot(); len(got) != 0 {
		t.Fatalf("Snapshot() на пустом таймлайне: len = %d, ожидалось 0", len(got))
	}
}

// --- clamp навигации ---

func TestQuestionTimeline_NavigationClamp(t *testing.T) {
	tl := NewQuestionTimeline()
	tl.Add("Q1")
	tl.Add("Q2")

	// Активная = последняя (Q2, idx 1): Next не двигает позицию.
	if tl.Next() {
		t.Fatalf("Next() на последней метке: true, ожидалось false")
	}
	if idx, _, _ := tl.Active(); idx != 1 {
		t.Fatalf("после Next() на последней метке: idx = %d, ожидалось 1", idx)
	}

	// Переход на первую метку, Prev не двигает позицию.
	if !tl.Prev() {
		t.Fatalf("Prev() с последней на первую: false, ожидалось true")
	}
	if idx, _, _ := tl.Active(); idx != 0 {
		t.Fatalf("после Prev(): idx = %d, ожидалось 0", idx)
	}
	if tl.Prev() {
		t.Fatalf("Prev() на первой метке: true, ожидалось false")
	}
	if idx, _, _ := tl.Active(); idx != 0 {
		t.Fatalf("после Prev() на первой метке: idx = %d, ожидалось 0", idx)
	}
}

// --- tail-follow и индикатор новой метки ---

func TestQuestionTimeline_TailFollowAndFresh(t *testing.T) {
	tl := NewQuestionTimeline()
	tl.Add("Q1")
	tl.Add("Q2")
	tl.Add("Q3")

	idx, e, ok := tl.Active()
	if !ok || idx != 2 || e.Question != "Q3" {
		t.Fatalf("после Add Q1..Q3: idx=%d ok=%v question=%q, ожидалось idx=2 ok=true Q3", idx, ok, e.Question)
	}
	if !tl.OnTail() {
		t.Fatalf("OnTail() на последней метке: false, ожидалось true")
	}

	// Пользователь уходит назад на Q2 — tail-follow выключается.
	if !tl.Prev() {
		t.Fatalf("Prev(): false, ожидалось true")
	}
	idx, e, _ = tl.Active()
	if idx != 1 || e.Question != "Q2" {
		t.Fatalf("после Prev(): idx=%d question=%q, ожидалось idx=1 Q2", idx, e.Question)
	}
	if tl.OnTail() {
		t.Fatalf("OnTail() после ухода с последней метки: true, ожидалось false")
	}

	// Новая метка приходит, но активная позиция НЕ двигается (не tail-follow).
	added := tl.Add("Q4")
	if added != 3 {
		t.Fatalf("Add(Q4) вернул idx=%d, ожидалось 3", added)
	}
	idx, e, _ = tl.Active()
	if idx != 1 || e.Question != "Q2" {
		t.Fatalf("после Add(Q4) без tail-follow: idx=%d question=%q, ожидалось idx=1 Q2", idx, e.Question)
	}

	// Появился индикатор новой метки, указывающий на Q4.
	freshIdx, fresh := tl.HasFresh()
	if !fresh || freshIdx != 3 {
		t.Fatalf("HasFresh() после Add(Q4): idx=%d ok=%v, ожидалось idx=3 ok=true", freshIdx, fresh)
	}

	// Двигаемся вперёд до Q4 — при достижении freshAt индикатор сбрасывается.
	if !tl.Next() {
		t.Fatalf("Next() к Q3: false, ожидалось true")
	}
	if _, fresh := tl.HasFresh(); !fresh {
		t.Fatalf("HasFresh() на промежуточной метке Q3: false, ожидалось true (индикатор ещё не сброшен)")
	}
	if !tl.Next() {
		t.Fatalf("Next() к Q4: false, ожидалось true")
	}
	idx, e, _ = tl.Active()
	if idx != 3 || e.Question != "Q4" {
		t.Fatalf("после Next() к Q4: idx=%d question=%q, ожидалось idx=3 Q4", idx, e.Question)
	}
	if freshIdx, fresh := tl.HasFresh(); fresh {
		t.Fatalf("HasFresh() после перехода на freshAt: ok=true (idx=%d), ожидалось false", freshIdx)
	}
	if !tl.OnTail() {
		t.Fatalf("OnTail() на Q4: false, ожидалось true")
	}
}

// --- ClearFresh ---

func TestQuestionTimeline_ClearFresh(t *testing.T) {
	tl := NewQuestionTimeline()
	tl.Add("Q1")
	tl.Add("Q2")

	// Уходим на Q1 — tail-follow выключен.
	tl.Prev()
	tl.Add("Q3")

	if _, fresh := tl.HasFresh(); !fresh {
		t.Fatalf("HasFresh() до ClearFresh(): false, ожидалось true")
	}
	tl.ClearFresh()
	if _, fresh := tl.HasFresh(); fresh {
		t.Fatalf("HasFresh() после ClearFresh(): true, ожидалось false")
	}

	// ClearFresh не двигает активную позицию и сохраняет tail-состояние.
	if idx, e, _ := tl.Active(); idx != 0 || e.Question != "Q1" {
		t.Fatalf("после ClearFresh(): idx=%d question=%q, ожидалось idx=0 Q1", idx, e.Question)
	}
	if tl.OnTail() {
		t.Fatalf("OnTail() после ClearFresh(): true, ожидалось false")
	}
}

// --- копии ---

func TestQuestionTimeline_CopiesSnapshot(t *testing.T) {
	tl := NewQuestionTimeline()
	tl.Add("Q1")
	tl.SetAnswer(0, []ui.Answer{{Source: "a", Target: "b"}})

	snap := tl.Snapshot()
	if len(snap) != 1 || len(snap[0].Answer) != 1 {
		t.Fatalf("Snapshot(): неверная структура: %+v", snap)
	}

	// Модификация снапшота не влияет на внутреннее состояние.
	snap[0].Answer[0].Source = "MUTATED"
	snap[0].Question = "MUTATED"

	idx, e, _ := tl.Active()
	if idx != 0 {
		t.Fatalf("idx = %d, ожидалось 0", idx)
	}
	if e.Question != "Q1" {
		t.Fatalf("Question после мутации снапшота = %q, ожидалось Q1", e.Question)
	}
	if e.Answer[0].Source != "a" {
		t.Fatalf("Answer[0].Source после мутации снапшота = %q, ожидалось a", e.Answer[0].Source)
	}
}

func TestQuestionTimeline_SetAnswerNoAlias(t *testing.T) {
	tl := NewQuestionTimeline()
	tl.Add("Q1")

	src := []ui.Answer{{Source: "orig", Target: "перевод"}}
	tl.SetAnswer(0, src)

	// Мутация исходного слайса не должна менять внутреннее состояние.
	src[0].Source = "MUTATED"
	src[0].Target = "MUTATED"

	_, e, _ := tl.Active()
	if len(e.Answer) != 1 {
		t.Fatalf("len(Answer) = %d, ожидалось 1", len(e.Answer))
	}
	if e.Answer[0].Source != "orig" || e.Answer[0].Target != "перевод" {
		t.Fatalf("Answer после мутации исходного слайса = %+v, ожидалось orig/перевод", e.Answer[0])
	}
}

func TestQuestionTimeline_ActiveReturnsCopy(t *testing.T) {
	tl := NewQuestionTimeline()
	tl.Add("Q1")
	tl.SetAnswer(0, []ui.Answer{{Source: "a"}})

	_, e, _ := tl.Active()
	e.Answer[0].Source = "MUTATED"
	e.Question = "MUTATED"

	_, e2, _ := tl.Active()
	if e2.Answer[0].Source != "a" {
		t.Fatalf("Active() вернул алиас: Answer[0].Source = %q, ожидалось a", e2.Answer[0].Source)
	}
	if e2.Question != "Q1" {
		t.Fatalf("Question = %q, ожидалось Q1", e2.Question)
	}
}

// --- NextStep ---

func TestQuestionTimeline_NextStepCycle(t *testing.T) {
	tl := NewQuestionTimeline()
	tl.Add("Q1")

	tests := []struct {
		name     string
		wantCmd  translator.GenerationCommand
		wantStep int
	}{
		{"шаг 0 → CommandAnswer", translator.CommandAnswer, 1},
		{"шаг 1 → CommandMoreContext", translator.CommandMoreContext, 2},
		{"шаг 2 → CommandSimplerEnglish", translator.CommandSimplerEnglish, 0},
		{"шаг 3 (цикл) → CommandAnswer", translator.CommandAnswer, 1},
	}

	for _, tt := range tests {
		cmd, ok := tl.NextStep(0)
		if !ok {
			t.Fatalf("%s: NextStep ok = false, ожидалось true", tt.name)
		}
		if cmd != tt.wantCmd {
			t.Fatalf("%s: cmd = %v, ожидалось %v", tt.name, cmd, tt.wantCmd)
		}
		if _, e, _ := tl.Active(); e.Step != tt.wantStep {
			t.Fatalf("%s: Step = %d, ожидалось %d", tt.name, e.Step, tt.wantStep)
		}
	}
}

// --- некорректный idx ---

func TestQuestionTimeline_InvalidIdxNoOp(t *testing.T) {
	tl := NewQuestionTimeline()
	tl.Add("Q1")

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("паника при неверном idx: %v", r)
		}
	}()

	// SetState/SetAnswer по несуществующим индексам — no-op.
	tl.SetState(-1, QGenerating)
	tl.SetState(1, QReady)
	tl.SetAnswer(-1, []ui.Answer{{Source: "x"}})
	tl.SetAnswer(99, []ui.Answer{{Source: "x"}})

	if _, ok := tl.NextStep(-1); ok {
		t.Fatalf("NextStep(-1): ok = true, ожидалось false")
	}
	if _, ok := tl.NextStep(1); ok {
		t.Fatalf("NextStep(1) при одной метке: ok = true, ожидалось false")
	}

	// Состояние метки не изменилось.
	_, e, _ := tl.Active()
	if e.State != QIdle {
		t.Fatalf("State = %v, ожидалось QIdle", e.State)
	}
	if e.Step != 0 {
		t.Fatalf("Step = %d, ожидалось 0", e.Step)
	}
	if len(e.Answer) != 0 {
		t.Fatalf("len(Answer) = %d, ожидалось 0", len(e.Answer))
	}
}

// --- SetState ---

func TestQuestionTimeline_SetState(t *testing.T) {
	tl := NewQuestionTimeline()
	tl.Add("Q1")
	tl.Add("Q2")

	tl.SetState(0, QGenerating)
	tl.SetState(1, QReady)

	snap := tl.Snapshot()
	if snap[0].State != QGenerating {
		t.Fatalf("snap[0].State = %v, ожидалось QGenerating", snap[0].State)
	}
	if snap[1].State != QReady {
		t.Fatalf("snap[1].State = %v, ожидалось QReady", snap[1].State)
	}
}

// --- навигация не трогает State/Step ---

func TestQuestionTimeline_NavigationKeepsStateAndStep(t *testing.T) {
	tl := NewQuestionTimeline()
	tl.Add("Q1")
	tl.Add("Q2")

	tl.SetState(0, QGenerating)
	if _, ok := tl.NextStep(0); !ok {
		t.Fatalf("NextStep(0): ok = false, ожидалось true")
	}

	tl.Prev()
	tl.Next()

	snap := tl.Snapshot()
	if snap[0].State != QGenerating {
		t.Fatalf("State после навигации = %v, ожидалось QGenerating", snap[0].State)
	}
	if snap[0].Step != 1 {
		t.Fatalf("Step после навигации = %d, ожидалось 1", snap[0].Step)
	}
}

// --- SetMode / Mode в Snapshot ---

func TestQuestionTimeline_SetMode(t *testing.T) {
	tl := NewQuestionTimeline()
	tl.Add("Q1")
	tl.Add("Q2")

	tl.SetMode(0, "more context")
	tl.SetMode(1, "simpler")

	// SetMode по неверному индексу — no-op.
	tl.SetMode(-1, "x")
	tl.SetMode(99, "x")

	snap := tl.Snapshot()
	if snap[0].Mode != "more context" {
		t.Fatalf("snap[0].Mode = %q, ожидалось %q", snap[0].Mode, "more context")
	}
	if snap[1].Mode != "simpler" {
		t.Fatalf("snap[1].Mode = %q, ожидалось %q", snap[1].Mode, "simpler")
	}
}

// --- ResetPending ---

func TestQuestionTimeline_ResetPending(t *testing.T) {
	tl := NewQuestionTimeline()
	tl.Add("Q1")
	tl.Add("Q2")
	tl.Add("Q3")
	tl.Add("Q4")
	tl.Add("Q5")

	tl.SetState(0, QReady)
	tl.SetState(1, QQueued)
	tl.SetState(2, QGenerating)
	tl.SetState(3, QFailed)
	// Q4 (idx 4) остаётся QIdle.

	tl.SetAnswer(0, []ui.Answer{{Source: "готовый ответ"}})

	tl.ResetPending()

	snap := tl.Snapshot()
	want := []QState{QReady, QIdle, QIdle, QFailed, QIdle}
	for i, w := range want {
		if snap[i].State != w {
			t.Fatalf("snap[%d].State = %v, ожидалось %v", i, snap[i].State, w)
		}
	}
	// Ответы готовой метки не затронуты.
	if len(snap[0].Answer) != 1 || snap[0].Answer[0].Source != "готовый ответ" {
		t.Fatalf("ResetPending испортил ответы QReady-метки: %+v", snap[0].Answer)
	}
}

// --- String ---

func TestQState_String(t *testing.T) {
	tests := []struct {
		state QState
		want  string
	}{
		{QIdle, "idle"},
		{QQueued, "queued"},
		{QGenerating, "generating"},
		{QReady, "ready"},
		{QFailed, "failed"},
		{QState(42), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Fatalf("QState(%d).String() = %q, ожидалось %q", int(tt.state), got, tt.want)
		}
	}
}

// --- кольцевой буфер: абсолютная нумерация при вытеснении головы ---

// TestQuestionTimeline_CapacityEvictsHead — после cap+5 добавлений таймлайн
// хранит ровно cap меток; первая метка окна имеет абсолютный номер 5 (голова
// вытеснена), а последняя — cap+4.
func TestQuestionTimeline_CapacityEvictsHead(t *testing.T) {
	tl := NewQuestionTimeline()
	extra := 5
	for i := 0; i < questionTimelineCapacity+extra; i++ {
		if got, want := tl.Add("q"), i; got != want {
			t.Fatalf("Add #%d вернул idx=%d, ожидалось %d (абсолютная нумерация)", i, got, want)
		}
	}

	snap := tl.Snapshot()
	if len(snap) != questionTimelineCapacity {
		t.Fatalf("len(Snapshot()) = %d, ожидалось %d (вместимость)", len(snap), questionTimelineCapacity)
	}
	if snap[0].Idx != extra {
		t.Fatalf("Snapshot()[0].Idx = %d, ожидалось %d (голова вытеснена)", snap[0].Idx, extra)
	}
	if snap[len(snap)-1].Idx != questionTimelineCapacity+extra-1 {
		t.Fatalf("последняя метка Idx = %d, ожидалось %d",
			snap[len(snap)-1].Idx, questionTimelineCapacity+extra-1)
	}
	// Абсолютный номер равен позиции в кольце + base: проверяем, что нумерация
	// монотонна и без дыр по всему окну.
	for i := 1; i < len(snap); i++ {
		if snap[i].Idx != snap[i-1].Idx+1 {
			t.Fatalf("нумерация с дырой: snap[%d].Idx=%d, snap[%d].Idx=%d",
				i-1, snap[i-1].Idx, i, snap[i].Idx)
		}
	}
}

// TestQuestionTimeline_EvictActiveMovesToHead — активная метка вытеснена →
// active указывает на новую голову; Prev/Next не паникуют.
func TestQuestionTimeline_EvictActiveMovesToHead(t *testing.T) {
	tl := NewQuestionTimeline()
	// Активной делаем метку на нулевом offset (уходим на начало).
	tl.Add("Q1")
	for i := 1; i < questionTimelineCapacity; i++ {
		tl.Add("q")
	}
	// Уходим на первую метку — она же будет вытеснена следующим Add.
	for tl.Prev() {
	}
	firstAbs := mustActiveIdx(t, tl)

	// Добавление вытесняет голову — именно она активна.
	tl.Add("overflow")

	idx, _, ok := tl.Active()
	if !ok {
		t.Fatal("Active() после вытеснения активной метки: ok=false")
	}
	if idx != firstAbs+1 {
		t.Fatalf("после вытеснения активной: idx=%d, ожидалось %d (новая голова)", idx, firstAbs+1)
	}
	// Навигация не паникует и остаётся в границах окна.
	if tl.Prev() {
		t.Fatalf("Prev() на новой голове: true, ожидалось false (граница окна)")
	}
	if !tl.Next() {
		t.Fatal("Next() с новой головы: false, ожидалось true")
	}
	if idx2, _, _ := tl.Active(); idx2 != firstAbs+2 {
		t.Fatalf("Next() дал idx=%d, ожидалось %d", idx2, firstAbs+2)
	}
}

// TestQuestionTimeline_AbsoluteIdxAfterEviction — SetState/SetAnswer/SetMode по
// абсолютному idx валидны после сдвига base; idx вытесненной метки (idx<base) —
// no-op.
func TestQuestionTimeline_AbsoluteIdxAfterEviction(t *testing.T) {
	tl := NewQuestionTimeline()
	last := -1
	for i := 0; i < questionTimelineCapacity+5; i++ {
		last = tl.Add("q")
	}

	// Валидная (хранимая) метка правится по абсолютному номеру.
	tl.SetAnswer(last, []ui.Answer{{Source: "abs", Target: "ру"}})
	tl.SetState(last, QReady)
	tl.SetMode(last, "answer")

	snap := tl.Snapshot()
	got := snap[len(snap)-1]
	if got.State != QReady || got.Mode != "answer" || len(got.Answer) != 1 || got.Answer[0].Source != "abs" {
		t.Fatalf("мутация по абсолютному idx не применилась: %+v", got)
	}

	// Вытесненные индексы (0..base-1) — no-op, без паники.
	tl.SetState(0, QFailed)
	tl.SetAnswer(0, []ui.Answer{{Source: "stale"}})
	tl.SetMode(0, "stale")
	if _, ok := tl.NextStep(0); ok {
		t.Fatal("NextStep(0) для вытесненной метки: ok=true, ожидалось false")
	}
	if snap2 := tl.Snapshot(); len(snap2) != questionTimelineCapacity {
		t.Fatalf("no-op сломал окно: len=%d", len(snap2))
	}
	// Свежая голова не получила чужую мутацию.
	if head := tl.Snapshot()[0]; head.State == QFailed {
		t.Fatalf("SetState вытесненного idx попал в голову окна: %+v", head)
	}
}

// --- вспомогательные проверки для тестов кольца ---

// mustActiveIdx возвращает абсолютный номер активной метки или падает.
func mustActiveIdx(t *testing.T, tl *QuestionTimeline) int {
	t.Helper()
	idx, _, ok := tl.Active()
	if !ok {
		t.Fatal("Active(): ok=false")
	}
	return idx
}
