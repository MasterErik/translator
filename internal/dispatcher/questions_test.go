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

// --- абсолютный счётчик вопросов (A-4) ---

// TestQuestionTimeline_TotalAskedSurvivesEviction — totalAsked растёт на каждом
// Add и НЕ сбрасывается при вытеснении головы кольца: после cap+5 добавлений
// он равен cap+5, что больше числа хранимых меток (cap). Плюс проверяем, что
// счётчик виден через Snapshot/мутации не влияют на него.
func TestQuestionTimeline_TotalAskedSurvivesEviction(t *testing.T) {
	tl := NewQuestionTimeline()

	if got := tl.TotalAsked(); got != 0 {
		t.Fatalf("TotalAsked() на пустом таймлайне = %d, ожидалось 0", got)
	}

	extra := 5
	for i := 0; i < questionTimelineCapacity+extra; i++ {
		tl.Add("q")
	}

	if got := tl.TotalAsked(); got != questionTimelineCapacity+extra {
		t.Fatalf("TotalAsked() после cap+%d Add = %d, ожидалось %d", extra, got, questionTimelineCapacity+extra)
	}
	// Вытеснение не должно уменьшать счётчик: он больше размера окна.
	snap := tl.Snapshot()
	if len(snap) != questionTimelineCapacity {
		t.Fatalf("len(Snapshot()) = %d, ожидалось %d (вместимость)", len(snap), questionTimelineCapacity)
	}
	if tl.TotalAsked() <= len(snap) {
		t.Fatalf("TotalAsked() = %d, ожидалось > len(Snapshot()) = %d (абсолютный счётчик)", tl.TotalAsked(), len(snap))
	}

	// ClearFresh/ResetPending/навигация не трогают счётчик.
	tl.Prev()
	tl.Next()
	tl.ClearFresh()
	tl.ResetPending()
	if got := tl.TotalAsked(); got != questionTimelineCapacity+extra {
		t.Fatalf("TotalAsked() после навигации/ResetPending = %d, ожидалось %d", got, questionTimelineCapacity+extra)
	}
}

// TestQuestionTimeline_ResetPendingResetsStep — ResetPending (Esc) сбрасывает
// Step вместе с состоянием: Queued/Generating → Idle со Step=0, поэтому
// следующий NextStep снова вернёт CommandAnswer. Step готовой метки не трогаем.
func TestQuestionTimeline_ResetPendingResetsStep(t *testing.T) {
	tl := NewQuestionTimeline()
	tl.Add("Q1")
	tl.Add("Q2")
	tl.Add("Q3")

	// Q1 — готовая метка, цикл которой уже продвинут (Step=2).
	tl.SetState(0, QReady)
	for i := 0; i < 2; i++ {
		if _, ok := tl.NextStep(0); !ok {
			t.Fatal("NextStep(0): ok = false, ожидалось true")
		}
	}
	// Q2 — в очереди с продвинутым Step.
	tl.SetState(1, QQueued)
	if _, ok := tl.NextStep(1); !ok {
		t.Fatal("NextStep(1): ok = false, ожидалось true")
	}
	// Q3 — генерируется.
	tl.SetState(2, QGenerating)
	if _, ok := tl.NextStep(2); !ok {
		t.Fatal("NextStep(2): ok = false, ожидалось true")
	}

	tl.ResetPending()

	snap := tl.Snapshot()
	if snap[0].Step != 2 {
		t.Errorf("Step готовой метки после ResetPending = %d, ожидалось 2 (не трогаем)", snap[0].Step)
	}
	if snap[1].State != QIdle || snap[1].Step != 0 {
		t.Errorf("Q2 после ResetPending: state=%v step=%d, ожидалось QIdle/0", snap[1].State, snap[1].Step)
	}
	if snap[2].State != QIdle || snap[2].Step != 0 {
		t.Errorf("Q3 после ResetPending: state=%v step=%d, ожидалось QIdle/0", snap[2].State, snap[2].Step)
	}

	// Идемпотентность: повторный ResetPending не портит готовый Step.
	tl.ResetPending()
	if got := tl.Snapshot()[1].Step; got != 0 {
		t.Fatalf("Step Q2 после повторного ResetPending = %d, ожидалось 0", got)
	}
}

// TestQuestionTimeline_ResetStep — ResetStep сбрасывает Step конкретной метки
// по абсолютному idx (дроп задания) и не задевает соседние; вытесненный idx — no-op.
func TestQuestionTimeline_ResetStep(t *testing.T) {
	tl := NewQuestionTimeline()
	tl.Add("Q1")
	tl.Add("Q2")

	for _, idx := range []int{0, 1} {
		if _, ok := tl.NextStep(idx); !ok {
			t.Fatalf("NextStep(%d): ok = false, ожидалось true", idx)
		}
	}

	tl.ResetStep(0)

	snap := tl.Snapshot()
	if snap[0].Step != 0 {
		t.Errorf("Step Q1 после ResetStep(0) = %d, ожидалось 0", snap[0].Step)
	}
	if snap[1].Step != 1 {
		t.Errorf("Step Q2 (соседняя метка) = %d, ожидалось 1 (не тронута)", snap[1].Step)
	}

	// Неверный/вытесненный idx — no-op без паники.
	tl.ResetStep(-1)
	tl.ResetStep(99)
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

// --- уточнения (фаза 2, часть Б: Б-T1) ---

// TestQuestionTimeline_AddClarification — добавление уточнения к метке:
// возвращает true, текст попадает в метку по абсолютному idx, чужие метки не
// затронуты; пустой текст и неверный/вытесненный idx — false (no-op).
func TestQuestionTimeline_AddClarification(t *testing.T) {
	tl := NewQuestionTimeline()
	tl.Add("Q1")
	tl.Add("Q2")

	if !tl.AddClarification(1, "уточнение 1") {
		t.Fatal("AddClarification(1) с непустым текстом: false, ожидалось true")
	}
	if !tl.AddClarification(1, "уточнение 2") {
		t.Fatal("AddClarification(1) второе: false, ожидалось true")
	}

	got := tl.Clarifications(1)
	if len(got) != 2 || got[0] != "уточнение 1" || got[1] != "уточнение 2" {
		t.Fatalf("Clarifications(1) = %v, ожидалось [уточнение 1 уточнение 2]", got)
	}
	// Соседняя метка не получила чужое уточнение.
	if got := tl.Clarifications(0); len(got) != 0 {
		t.Fatalf("Clarifications(0) = %v, ожидалось пусто", got)
	}

	// Пустой текст — false, состояние не меняется.
	if tl.AddClarification(1, "") {
		t.Fatal("AddClarification с пустым текстом: true, ожидалось false")
	}
	if got := tl.Clarifications(1); len(got) != 2 {
		t.Fatalf("пустой текст добавился: %v", got)
	}

	// Неверный/вытесненный idx — false, без паники.
	for _, idx := range []int{-1, 2, 99} {
		if tl.AddClarification(idx, "x") {
			t.Fatalf("AddClarification(%d): true, ожидалось false (idx вне окна)", idx)
		}
	}
}

// TestQuestionTimeline_ClarificationsNilForEmpty — у метки без уточнений
// Clarifications() возвращает nil (контракт: nil для пустого), неверный idx — nil.
func TestQuestionTimeline_ClarificationsNilForEmpty(t *testing.T) {
	tl := NewQuestionTimeline()
	tl.Add("Q1")

	if got := tl.Clarifications(0); got != nil {
		t.Fatalf("Clarifications(0) без уточнений = %#v, ожидалось nil", got)
	}
	if got := tl.Clarifications(-1); got != nil {
		t.Fatalf("Clarifications(-1) = %#v, ожидалось nil", got)
	}
	if got := tl.Clarifications(99); got != nil {
		t.Fatalf("Clarifications(99) = %#v, ожидалось nil", got)
	}
}

// TestQuestionTimeline_RemoveLastClarification — убирает только последнее
// уточнение метки; на пустом списке/неверном idx — false (no-op).
func TestQuestionTimeline_RemoveLastClarification(t *testing.T) {
	tl := NewQuestionTimeline()
	tl.Add("Q1")
	tl.Add("Q2")
	tl.AddClarification(1, "первое")
	tl.AddClarification(1, "второе")

	if !tl.RemoveLastClarification(1) {
		t.Fatal("RemoveLastClarification(1): false, ожидалось true")
	}
	if got := tl.Clarifications(1); len(got) != 1 || got[0] != "первое" {
		t.Fatalf("после первого RemoveLastClarification: %v, ожидалось [первое]", got)
	}
	if !tl.RemoveLastClarification(1) {
		t.Fatal("RemoveLastClarification(1) второй: false, ожидалось true")
	}
	if got := tl.Clarifications(1); got != nil {
		t.Fatalf("после полного опустошения: %#v, ожидалось nil", got)
	}

	// Нет уточнений — false.
	if tl.RemoveLastClarification(1) {
		t.Fatal("RemoveLastClarification на пустом списке: true, ожидалось false")
	}
	// Неверный idx — false, чужие уточнения не трогаются.
	if tl.RemoveLastClarification(-1) {
		t.Fatal("RemoveLastClarification(-1): true, ожидалось false")
	}
	if tl.RemoveLastClarification(99) {
		t.Fatal("RemoveLastClarification(99): true, ожидалось false")
	}
}

// TestQuestionTimeline_ClarificationsNoAlias — возвращённые копии не алиасят
// внутренний стек уточнений: мутация среза (и его элементов) не влияет на
// таймлайн ни через Clarifications(), ни через Snapshot()/Active().
func TestQuestionTimeline_ClarificationsNoAlias(t *testing.T) {
	tl := NewQuestionTimeline()
	tl.Add("Q1")
	tl.AddClarification(0, "оригинал")

	got := tl.Clarifications(0)
	if len(got) != 1 {
		t.Fatalf("Clarifications(0) = %v, ожидалось 1 элемент", got)
	}
	got[0] = "MUTATED"

	if after := tl.Clarifications(0); after[0] != "оригинал" {
		t.Fatalf("мутация Clarifications() просочилась внутрь: %q", after[0])
	}

	// Snapshot() отдаёт копию — мутация не влияет ни на таймлайн, ни на
	// последующие снапшоты.
	snap := tl.Snapshot()
	if len(snap) != 1 || len(snap[0].Clarifications) != 1 {
		t.Fatalf("Snapshot(): неверная структура: %+v", snap)
	}
	snap[0].Clarifications[0] = "SNAP-MUTATED"
	if got := tl.Clarifications(0); got[0] != "оригинал" {
		t.Fatalf("мутация Snapshot() просочилась внутрь: %q", got[0])
	}
	if got := tl.Snapshot()[0].Clarifications[0]; got != "оригинал" {
		t.Fatalf("мутация Snapshot() сохранилась во внутреннем стеке: %q", got)
	}

	// Active() — тоже копия.
	tl.AddClarification(0, "второе")
	_, e, _ := tl.Active()
	if len(e.Clarifications) != 2 {
		t.Fatalf("Active().Clarifications = %v, ожидалось 2 элемента", e.Clarifications)
	}
	e.Clarifications[0] = "ACTIVE-MUTATED"
	if got := tl.Clarifications(0); got[0] != "оригинал" {
		t.Fatalf("мутация Active() просочилась внутрь: %q", got[0])
	}
}

// TestQuestionTimeline_AbsorbLastMergesDuplicate — слияние: последняя метка окна
// является репликой-дублем вопроса (её Question == q). Метка удаляется, её текст
// уходит уточнением в метку idx-1, активность указывает на слитую метку.
func TestQuestionTimeline_AbsorbLastMergesDuplicate(t *testing.T) {
	// Раскладка: [0]=Q1 (prev, с уточнением), [1]=Q3, [2]=дубль (удаляется).
	tl := NewQuestionTimeline()
	tl.Add("Q1")
	tl.AddClarification(0, "уточнение A")
	tl.Add("Q3")
	tl.Add("Дубль")

	// Активность на хвосте (idx 2) — это и есть удаляемая метка.
	if idx, _, _ := tl.Active(); idx != 2 {
		t.Fatalf("подготовка: активная метка idx=%d, ожидалось 2", idx)
	}

	newIdx, ok := tl.AbsorbLast("Дубль")
	if !ok {
		t.Fatal("AbsorbLast с совпадающей последней меткой: ok=false, ожидалось true")
	}
	if newIdx != 1 {
		t.Fatalf("AbsorbLast вернул newIdx=%d, ожидалось 1 (указатель смещён на слитую метку Q3)", newIdx)
	}

	snap := tl.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("после слияния len(Snapshot())=%d, ожидалось 2 (метка удалена)", len(snap))
	}
	if snap[0].Idx != 0 || snap[0].Question != "Q1" {
		t.Fatalf("после слияния snap[0] = %+v, ожидалось idx=0 Q1", snap[0])
	}
	if snap[1].Idx != 1 || snap[1].Question != "Q3" {
		t.Fatalf("после слияния snap[1] = %+v, ожидалось idx=1 Q3 (слитая метка стала хвостом)", snap[1])
	}
	// Текст удалённой метки ушёл уточнением в idx-1 (Q3), а не в Q1.
	if got := tl.Clarifications(1); len(got) != 1 || got[0] != "Дубль" {
		t.Fatalf("Clarifications(1) = %v, ожидалось [Дубль] (текст слитой метки в метке idx-1)", got)
	}
	got := tl.Clarifications(0)
	want := []string{"уточнение A"}
	if len(got) != len(want) {
		t.Fatalf("Clarifications(0) = %v, ожидалось %v (стек Q1 не тронут)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Clarifications(0)[%d] = %q, ожидалось %q", i, got[i], want[i])
		}
	}

	// Активность пересчитана: указатель, смотревший на удалённую метку (idx 2),
	// перенесён на слитую (idx 1), а не «залип» на прежнем абсолютном номере.
	if idx, e, ok := tl.Active(); !ok || idx != 1 || e.Question != "Q3" {
		t.Fatalf("Active() после слияния: idx=%d ok=%v question=%q, ожидалось idx=1 ok=true Q3", idx, ok, e.Question)
	}
	if !tl.OnTail() {
		t.Fatal("OnTail() после слияния: false, ожидалось true (слитая метка стала хвостом)")
	}
	// Слияние не пересоздаёт метки: счётчик заданных вопросов не сбрасывается.
	if got := tl.TotalAsked(); got != 3 {
		t.Fatalf("TotalAsked() после слияния = %d, ожидалось 3 (метка была добавлена)", got)
	}
}

// TestQuestionTimeline_AbsorbLastMismatchNoOp — несовпадение текста последней
// метки и q → no-op: метки не тронуты, ok=false, активность не двигается.
func TestQuestionTimeline_AbsorbLastMismatchNoOp(t *testing.T) {
	tl := NewQuestionTimeline()
	tl.Add("Q1")
	tl.Add("Что такое Kafka?")

	before := tl.Snapshot()
	idxBefore := mustActiveIdx(t, tl)

	newIdx, ok := tl.AbsorbLast("Что такое RAID?")
	if ok {
		t.Fatal("AbsorbLast при несовпадении Question: ok=true, ожидалось false")
	}
	if newIdx != idxBefore {
		t.Fatalf("AbsorbLast вернул newIdx=%d, ожидалось текущий хвост %d", newIdx, idxBefore)
	}

	after := tl.Snapshot()
	if len(after) != len(before) {
		t.Fatalf("no-op изменил число меток: %d → %d", len(before), len(after))
	}
	for i := range after {
		if after[i].Idx != before[i].Idx || after[i].Question != before[i].Question || len(after[i].Clarifications) != 0 {
			t.Fatalf("no-op изменил метку %d: %+v (было %+v)", i, after[i], before[i])
		}
	}
	if idx := mustActiveIdx(t, tl); idx != idxBefore {
		t.Fatalf("no-op сдвинул активную метку: %d → %d", idxBefore, idx)
	}
}

// TestQuestionTimeline_AbsorbLastSingleEntryNoOp — единственная метка: сливать
// не с чем, ok=false, таймлайн не изменён.
func TestQuestionTimeline_AbsorbLastSingleEntryNoOp(t *testing.T) {
	tl := NewQuestionTimeline()
	tl.Add("Q1")

	newIdx, ok := tl.AbsorbLast("Q1")
	if ok {
		t.Fatal("AbsorbLast при единственной метке: ok=true, ожидалось false")
	}
	if newIdx != 0 {
		t.Fatalf("AbsorbLast при единственной метке вернул newIdx=%d, ожидалось 0", newIdx)
	}
	if snap := tl.Snapshot(); len(snap) != 1 || snap[0].Question != "Q1" {
		t.Fatalf("таймлайн изменён при единственной метке: %+v", snap)
	}
}

// TestQuestionTimeline_AbsorbLastEmptyTimelineNoOp — пустой таймлайн: no-op без
// паники, ok=false.
func TestQuestionTimeline_AbsorbLastEmptyTimelineNoOp(t *testing.T) {
	tl := NewQuestionTimeline()

	newIdx, ok := tl.AbsorbLast("Q1")
	if ok {
		t.Fatal("AbsorbLast на пустом таймлайне: ok=true, ожидалось false")
	}
	if newIdx != 0 {
		t.Fatalf("AbsorbLast на пустом таймлайне вернул newIdx=%d, ожидалось 0", newIdx)
	}
	if snap := tl.Snapshot(); len(snap) != 0 {
		t.Fatalf("пустой таймлайн изменён: %+v", snap)
	}
}

// TestQuestionTimeline_AbsorbLastResetsFreshOn — если индикатор freshAt указывал
// на удаляемую метку, после слияния он сбрасывается (метки больше нет).
func TestQuestionTimeline_AbsorbLastResetsFreshOn(t *testing.T) {
	tl := NewQuestionTimeline()
	tl.Add("Q1")
	tl.Add("Q2")
	tl.Prev() // уходим с хвоста (active -> 0) — следующие Add дают freshAt
	tl.Add("Q3")
	tl.Add("Дубль")

	freshIdx, fresh := tl.HasFresh()
	if !fresh || freshIdx != 3 {
		t.Fatalf("подготовка: HasFresh() = (%d,%v), ожидалось (3,true)", freshIdx, fresh)
	}

	// Слияние удаляет метку freshAt — индикатор исчезает; активность остаётся
	// на Q1 (idx 0): пользователь не смотрел на удалённую, поэтому указатель не
	// смещается, а лишь clamp'ится в новое окно.
	newIdx, ok := tl.AbsorbLast("Дубль")
	if !ok {
		t.Fatal("AbsorbLast с совпадающим текстом: ok=false, ожидалось true")
	}
	if newIdx != 0 {
		t.Fatalf("AbsorbLast вернул newIdx=%d, ожидалось 0", newIdx)
	}
	if idx, fresh := tl.HasFresh(); fresh {
		t.Fatalf("HasFresh() после слияния с freshAt-меткой: ok=true (idx=%d), ожидалось false", idx)
	}
	if idx, e, _ := tl.Active(); idx != 0 || e.Question != "Q1" {
		t.Fatalf("Active() = idx %d question %q, ожидалось idx=0 Q1 (указатель не смотрел на удалённую)", idx, e.Question)
	}
}

// TestQuestionTimeline_ClarificationsSurviveEviction — уточнения живут в QEntry и
// переживают вытеснение головы кольца: >cap Add'ов, уточнение в хвостовой метке,
// вытеснение головы — уточнение на месте, соседние метки не «унаследовали» его.
func TestQuestionTimeline_ClarificationsSurviveEviction(t *testing.T) {
	tl := NewQuestionTimeline()

	// Голова с уточнением — уедет при вытеснении.
	headIdx := tl.Add("head")
	tl.AddClarification(headIdx, "уточнение головы")

	// Хвост с уточнением — переживёт вытеснение.
	var tailIdx int
	for i := 0; i < questionTimelineCapacity+10; i++ {
		tailIdx = tl.Add("q")
	}
	tl.AddClarification(tailIdx, "уточнение хвоста")

	snap := tl.Snapshot()
	if len(snap) != questionTimelineCapacity {
		t.Fatalf("len(Snapshot())=%d, ожидалось %d (вместимость)", len(snap), questionTimelineCapacity)
	}
	// Голова вытеснена — её уточнение недоступно вместе с меткой.
	if got := tl.Clarifications(headIdx); got != nil {
		t.Fatalf("Clarifications вытесненной метки = %v, ожидалось nil", got)
	}
	if tl.AddClarification(headIdx, "позднее") {
		t.Fatal("AddClarification вытесненной метки: true, ожидалось false")
	}

	// Уточнение хвоста на месте и ровно одно.
	got := tl.Clarifications(tailIdx)
	if len(got) != 1 || got[0] != "уточнение хвоста" {
		t.Fatalf("Clarifications(tail)=%v, ожидалось [уточнение хвоста]", got)
	}
	// Ни одна другая метка окна не получила чужой стек уточнений.
	for i, e := range snap {
		if e.Idx == tailIdx {
			continue
		}
		if len(e.Clarifications) != 0 {
			t.Fatalf("метка snap[%d] (idx=%d) получила чужие уточнения: %v", i, e.Idx, e.Clarifications)
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

// mustActiveIdx возвращает абсолютный номер активной метки или падает.
func mustActiveIdx(t *testing.T, tl *QuestionTimeline) int {
	t.Helper()
	idx, _, ok := tl.Active()
	if !ok {
		t.Fatal("Active(): ok=false")
	}
	return idx
}
