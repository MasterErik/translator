package dispatcher

import (
	"context"
	"testing"
	"time"

	"github.com/mastererik/translator/internal/translator"
)

// ── F1: автоуточнения (SpeechLog → Clarifications) ────────────────────────

// TestAutoClarifyAttachesWindowRemarksOnly — после генерации CommandAnswer
// прикрепляются только реплики-не-вопросы, озвученные ПОСЛЕ вопроса метки:
//   - реплика «до вопроса» — вне окна (SnapshotAfter по времени вопроса);
//   - реплика «после вопроса» — становится уточнением и консумен;
//   - реплика-вопрос — НЕ уточнение (вопросы сами создают метки в route) и
//     остаётся unconsumed (её заберёт Shift+F2; в RecentContext она и так есть).
//
// Метки времени заданы явно относительно момента вызова: подряд идущие
// time.Now() на Windows совпадают (granularity ~0.5 мс), а окно включает
// границу (>=) — явные смещения делают тест детерминированным.
func TestAutoClarifyAttachesWindowRemarksOnly(t *testing.T) {
	engine := &mockEngine{answers: []string{"EN: A | RU: A"}}
	d := New(&mockOverlay{}, engine, &mockLogger{}, DefaultConfig())

	base := time.Now()
	d.speechLog.Add("We use a config-service for that.", base.Add(-time.Second)) // до вопроса
	d.speechLog.Add("You mean horizontal scaling.", base.Add(time.Second))       // после
	d.speechLog.Add("What about the latency?", base.Add(time.Second))            // вопрос после

	d.GenerateAnswers("How do you scale?")

	if got := d.timeline.Clarifications(0); len(got) != 1 || got[0] != "You mean horizontal scaling." {
		t.Fatalf("Clarifications = %v, ожидалось [You mean horizontal scaling.]", got)
	}

	// Потреблены только прикреплённые реплики: «до» и вопрос остаются unconsumed.
	snap := d.speechLog.SnapshotAfter(time.Time{})
	if len(snap) != 2 || snap[0].Text != "We use a config-service for that." || snap[1].Text != "What about the latency?" {
		t.Fatalf("unconsumed после автоуточнения = %+v, ожидались реплика «до» и вопрос", snap)
	}

	// Шапка метки уехала в overlay с уточнением.
	a := answers0(t, d.overlay.(*mockOverlay))
	if len(a.Clarifications) != 1 || a.Clarifications[0] != "You mean horizontal scaling." {
		t.Errorf("ui.Answer.Clarifications = %v, ожидалось [You mean horizontal scaling.]", a.Clarifications)
	}
}

// TestAutoClarifyDeduplicatesAndIsOneShot — автоуточнение:
//   - дубликат текста (уже в Clarifications ИЛИ повторно в SpeechLog в том же
//     окне) — консумен БЕЗ добавления второго вхождения;
//   - окно одноразовое (labelTimestamps): перегенерация той же метки не
//     приносит новых уточнений и не ломает существующие.
func TestAutoClarifyDeduplicatesAndIsOneShot(t *testing.T) {
	engine := &mockEngine{answers: []string{"EN: A | RU: A"}}
	d := New(&mockOverlay{}, engine, &mockLogger{}, DefaultConfig())

	idx := d.timeline.Add("How do you scale?")
	d.setLabelTimestamp(idx, time.Now().Add(-time.Second))

	now := time.Now()
	d.speechLog.Add("Two identical remarks.", now)
	d.speechLog.Add("Two identical remarks.", now.Add(time.Millisecond))

	d.generateAnswers(context.Background(), "How do you scale?", translator.CommandAnswer, idx)

	if got := d.timeline.Clarifications(idx); len(got) != 1 {
		t.Fatalf("Clarifications = %v, ожидалось ровно одно вхождение (дедуп)", got)
	}
	if snap := d.speechLog.SnapshotAfter(time.Time{}); len(snap) != 0 {
		t.Fatalf("дубликаты остались unconsumed: %+v", snap)
	}

	// Окно НЕ одноразовое (F4: постоянное свойство метки — коалесинг и
	// повторные перегенерации), но уточнения не растут: реплики из окна уже
	// потреблены и скрыты из SnapshotAfter — перегенерации нечего прикреплять.
	d.generateAnswers(context.Background(), "How do you scale?", translator.CommandAnswer, idx)
	if got := d.timeline.Clarifications(idx); len(got) != 1 {
		t.Errorf("после перегенерации Clarifications = %v, ожидалось то же одно вхождение", got)
	}
}

// TestAutoClarifySkipsNotOnTail — автоуточнение прикрепляется только к хвостовой
// метке: активная «прошлая» метка при новой генерации не получает чужие реплики.
//
// Активна метка 0, генерируется метка 1 (CommandAnswer по 1-й час F3-очереди).
// Реплика в SpeechLog с ts внутри окна метки 1 НЕ должна уйти в уточнения метки 0.
func TestAutoClarifySkipsNotOnTail(t *testing.T) {
	engine := &mockEngine{answers: []string{"EN: A | RU: A"}}
	d := New(&mockOverlay{}, engine, &mockLogger{}, DefaultConfig())

	d.timeline.Add("First question?")
	d.timeline.Add("Second question?")
	// Уходим на метку 0: активна НЕ хвостовая метка ⇒ OnTail() = false.
	d.timeline.Prev()
	if _, _, ok := d.timeline.Active(); !ok {
		t.Fatal("предусловие: активная метка не найдена")
	}

	now := time.Now()
	d.speechLog.Add("A remark for the tail.", now.Add(time.Second))

	d.generateAnswers(context.Background(), "Second question?", translator.CommandAnswer, 1)

	// Уточнений нет: OnTail=false закрыл окно; реплика осталась unconsumed —
	// её заберут Shift+F2/RecentContext.
	if got := d.timeline.Clarifications(0); len(got) != 0 {
		t.Errorf("не-хвостовая метка получила уточнение: %v", got)
	}
	if got := d.timeline.Clarifications(1); len(got) != 0 {
		t.Errorf("метка 1 получила уточнение при OnTail=false: %v", got)
	}
	if snap := d.speechLog.SnapshotAfter(time.Time{}); len(snap) != 1 {
		t.Errorf("реплика должна остаться unconsumed: %+v", snap)
	}
}

// TestAutoClarifyViaRoute — интеграция полного маршрута: EndOfTurn-вопрос →
// авто-генерация → реплика интервьюера ПОСЛЕ вопроса прикрепляется метке и
// видна в шапке AnswerCandidates. Реплика проходит через textStream (route →
// speechLog.Add), а не добавляется вручную.
func TestAutoClarifyViaRoute(t *testing.T) {
	engine := &mockEngine{answers: []string{"EN: A | RU: A"}}
	d, overlay, textStream, cancel := startDispatcherWithOverlay(t, engine, DefaultConfig())
	defer cancel()

	// Вопрос (метка 0) → авто-генерация; затем не-вопрос-реплика уже ПОСЛЕ
	// вопроса (в окне, даже при совпадающих time.Now() из-за >=-границы).
	textStream <- question("How do you scale?")
	textStream <- question("I mean across regions.")

	waitFor(t, func() bool {
		return len(d.timeline.Clarifications(0)) == 1
	}, 2*time.Second)

	if got := d.timeline.Clarifications(0); got[0] != "I mean across regions." {
		t.Fatalf("Clarifications = %v, ожидалось [I mean across regions.]", got)
	}

	// Шапка с уточнением доставлена в overlay.
	waitFor(t, func() bool {
		msg, ok := lastCandidates(overlay)
		return ok && len(msg.Answers) > 0 && len(msg.Answers[0].Clarifications) == 1
	}, 2*time.Second)

	// Реплика потреблена автоуточнением; в SpeechLog осталась только реплика
	// «I mean...» — нет, потреблена она: остаётся только сам вопрос метки.
	waitFor(t, func() bool {
		snap := d.speechLog.SnapshotAfter(time.Time{})
		return len(snap) == 1 && snap[0].Text == "How do you scale?"
	}, 2*time.Second)
}
