package translator

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mastererik/translator/internal/common"
)

// speechBase — фиксированная точка отсчёта тестовых Timestamp.
var speechBase = time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)

// speechAt возвращает speechBase + offset секунд.
func speechAt(offset int) time.Time {
	return speechBase.Add(time.Duration(offset) * time.Second)
}

// TestSpeechLogAddSkipsEmpty — пустые реплики не записываются; текст
// нормализуется TrimSpace; первая запись получает Seq 0.
func TestSpeechLogAddSkipsEmpty(t *testing.T) {
	log := NewSpeechLog(5, 100)

	for _, in := range []string{"", "   ", "\t\n", " \t \n "} {
		log.Add(in, speechBase)
	}
	if got := log.SnapshotAfter(time.Time{}); len(got) != 0 {
		t.Fatalf("пустые реплики не должны записываться: SnapshotAfter = %v", got)
	}

	log.Add("  Tell me about Go.  ", speechBase)
	got := log.SnapshotAfter(time.Time{})
	if len(got) != 1 {
		t.Fatalf("SnapshotAfter = %d записей, ожидалась 1", len(got))
	}
	if got[0].Text != "Tell me about Go." {
		t.Errorf("Text = %q, ожидалось %q (TrimSpace-нормализация)", got[0].Text, "Tell me about Go.")
	}
	if got[0].Seq != 0 {
		t.Errorf("Seq = %d, ожидался 0 (первая запись)", got[0].Seq)
	}
}

// TestSpeechLogAddZeroTimestamp — Timestamp == 0 заменяется на time.Now():
// реплика не должна навсегда выпасть из окон SnapshotAfter.
func TestSpeechLogAddZeroTimestamp(t *testing.T) {
	log := NewSpeechLog(5, 100)

	log.Add("Hi.", time.Time{})
	nowish := time.Now()

	if got := log.SnapshotAfter(nowish.Add(time.Minute)); len(got) != 0 {
		t.Fatalf("нормализованный Timestamp должен быть ~now: SnapshotAfter(+1min) = %v", got)
	}
	if got := log.SnapshotAfter(nowish.Add(-time.Minute)); len(got) != 1 {
		t.Fatalf("Zero Timestamp должен быть заменён на ~now: SnapshotAfter(-1min) = %d записей, ожидалась 1", len(got))
	}
}

// TestSpeechLogTakeLatestUnconsumed — берётся последняя unconsumed-реплика,
// вызов потребляет её: повторный вызов без новой фразы — no-op
// (замена старых takeLastFinal/Shift+F2).
func TestSpeechLogTakeLatestUnconsumed(t *testing.T) {
	log := NewSpeechLog(5, 100)

	if text, ok := log.TakeLatestUnconsumed(); ok || text != "" {
		t.Fatalf("пустой лог: (%q, %v), ожидалось (%q, false)", text, ok, "")
	}

	log.Add("First question?", speechAt(1))
	log.Add("Second question?", speechAt(2))

	if text, ok := log.TakeLatestUnconsumed(); !ok || text != "Second question?" {
		t.Fatalf("TakeLatestUnconsumed = (%q, %v), ожидалось (%q, true)", text, ok, "Second question?")
	}

	// Более старая unconsumed-реплика остаётся доступной (буфер уточнений F3):
	// потребляется следующая по свежести.
	if text, ok := log.TakeLatestUnconsumed(); !ok || text != "First question?" {
		t.Fatalf("второй вызов без новой Add = (%q, %v), ожидалось (%q, true)", text, ok, "First question?")
	}
	// Когда очередь unconsumed пуста — no-op.
	if text, ok := log.TakeLatestUnconsumed(); ok || text != "" {
		t.Fatalf("третий вызов без unconsumed = (%q, %v), ожидалось (%q, false)", text, ok, "")
	}

	// Новая фраза снова доступна.
	log.Add("Third question?", speechAt(3))
	if text, ok := log.TakeLatestUnconsumed(); !ok || text != "Third question?" {
		t.Fatalf("после новой Add = (%q, %v), ожидалось (%q, true)", text, ok, "Third question?")
	}
	// После потребления свежей реплики очередь пуста — no-op.
}

// TestSpeechLogRestore — потреблённый текст возвращается как новая
// unconsumed-реплика с ts = time.Now() и новым Seq (семантика C2).
func TestSpeechLogRestore(t *testing.T) {
	log := NewSpeechLog(5, 100)
	log.Add("Clarify this point?", speechAt(1))

	beforeRestore := time.Now()
	text, ok := log.TakeLatestUnconsumed()
	if !ok || text != "Clarify this point?" {
		t.Fatalf("TakeLatestUnconsumed = (%q, %v), ожидалось (%q, true)", text, ok, "Clarify this point?")
	}
	if got := log.SnapshotAfter(time.Time{}); len(got) != 0 {
		t.Fatalf("потреблённая реплика скрыта из SnapshotAfter, но что-то вернулось: %v", got)
	}

	log.Restore(text)
	afterRestore := time.Now()

	got := log.SnapshotAfter(time.Time{})
	if len(got) != 1 {
		t.Fatalf("Restore должен вернуть текст как новую unconsumed-реплику: %d записей", len(got))
	}
	if got[0].Text != text {
		t.Errorf("Restore текст = %q, ожидалось %q", got[0].Text, text)
	}
	if got[0].Seq != 1 {
		t.Errorf("Seq после Restore = %d, ожидался 1 (глобальная монотонность)", got[0].Seq)
	}
	if got[0].Timestamp.Before(beforeRestore) || got[0].Timestamp.After(afterRestore) {
		t.Errorf("Timestamp = %v, ожидалось в диапазоне Restore (%v…%v)", got[0].Timestamp, beforeRestore, afterRestore)
	}

	// Пустой restore — no-op, как в Add.
	log.Restore("   ")
	if got := log.SnapshotAfter(time.Time{}); len(got) != 1 {
		t.Fatalf("Restore пустого текста должен быть no-op: %d записей", len(got))
	}
}

// TestSpeechLogConsume — потребление по Seq: true на первой попытке,
// false при повторе и для неизвестных Seq.
func TestSpeechLogConsume(t *testing.T) {
	log := NewSpeechLog(5, 100)
	log.Add("A.", speechAt(1)) // Seq 0
	log.Add("B.", speechAt(2)) // Seq 1

	if !log.Consume(0) {
		t.Fatal("Consume(seq 0): ожидалось true")
	}
	if log.Consume(0) {
		t.Error("повторный Consume того же Seq должен вернуть false")
	}
	for _, seq := range []uint64{33, 99} {
		if log.Consume(seq) {
			t.Errorf("Consume(неизвестный Seq %d) должен вернуть false", seq)
		}
	}
	if !log.Consume(1) {
		t.Error("Consume(seq 1): ожидалось true")
	}
}

// TestSpeechLogRingEvictionKeepsNewest — кольцо вытесняет самые старые
// записи, Seq не переиспользуется: вытесненная реплика больше не потребляется.
func TestSpeechLogRingEvictionKeepsNewest(t *testing.T) {
	log := NewSpeechLog(2, 10_000)
	log.Add("A first remark.", speechAt(1))
	log.Add("B second remark.", speechAt(2))
	log.Add("C third remark.", speechAt(3))

	got := log.SnapshotAfter(time.Time{})
	if len(got) != 2 {
		t.Fatalf("кольцо capacity=2: %d записей, ожидалось 2", len(got))
	}
	if got[0].Text != "B second remark." || got[1].Text != "C third remark." {
		t.Errorf("в кольце должны остаться новейшие в хронологическом порядке: %v", got)
	}

	if log.Consume(0) {
		t.Error("Consume вытесненного Seq 0 должен вернуть false")
	}
	if !log.Consume(2) {
		t.Error("Consume живой реплики (Seq 2) должен вернуть true")
	}
}

// TestSpeechLogRecentContextEmpty — пустая история → пустой блок.
func TestSpeechLogRecentContextEmpty(t *testing.T) {
	if got := NewSpeechLog(5, 100).RecentContext(3, 100); got != "" {
		t.Errorf("RecentContext на пустой истории = %q, ожидалось \"\"", got)
	}
}

// TestSpeechLogRecentContextFormatAndChronology — точный формат блока,
// старые реплики сверху.
func TestSpeechLogRecentContextFormatAndChronology(t *testing.T) {
	log := NewSpeechLog(5, 10_000)
	log.Add("Tell me about Project X.", speechAt(1))
	log.Add("What was the stack?", speechAt(2))

	want := "Interviewer remarks:\n- Tell me about Project X.\n- What was the stack?"
	if got := log.RecentContext(5, 10_000); got != want {
		t.Errorf("RecentContext =\n%q\nожидалось\n%q", got, want)
	}
}

// TestSpeechLogRecentContextMaxRemarks — окно последних maxRemarks реплик.
func TestSpeechLogRecentContextMaxRemarks(t *testing.T) {
	log := NewSpeechLog(10, 10_000)
	for i := 1; i <= 5; i++ {
		log.Add(fmt.Sprintf("Remark %d.", i), speechAt(i))
	}

	want := "Interviewer remarks:\n- Remark 4.\n- Remark 5."
	if got := log.RecentContext(2, 10_000); got != want {
		t.Errorf("RecentContext(2) =\n%q\nожидалось\n%q", got, want)
	}

	// maxRemarks=0 → весь размер кольца (дефолт).
	if got, lines := log.RecentContext(0, 10_000), strings.Count(log.RecentContext(0, 10_000), "\n- "); lines != 5 {
		t.Errorf("RecentContext(0) должен выбрать всё кольцо: строк = %d, ожидалось 5; блок:\n%s", lines, got)
	}
}

// TestSpeechLogRecentContextBudgetClipsOldestKeepsNewest — обрезка по бюджету
// токенов отбрасывает самые СТАРЫЕ реплики, самая свежая сохраняется всегда
// (семантика ConversationHistory.BuildContext).
func TestSpeechLogRecentContextBudgetClipsOldestKeepsNewest(t *testing.T) {
	log := NewSpeechLog(10, 10_000)
	log.Add("Aaa oldest remark.", speechAt(1))
	log.Add("Bbb middle remark.", speechAt(2))
	log.Add("Ccc fresh remark.", speechAt(3))

	// Бюджет 1 токен: любая непустая строка ">=", выживает только свежая.
	want := "Interviewer remarks:\n- Ccc fresh remark."
	if got := log.RecentContext(5, 1); got != want {
		t.Errorf("maxTokens=1: RecentContext =\n%q\nожидалось\n%q", got, want)
	}

	// Точный бюджет под две свежие реплики: самая старая отбрасывается.
	fresh := common.EstimateTokens("- Ccc fresh remark.")
	mid := common.EstimateTokens("- Bbb middle remark.")
	want = "Interviewer remarks:\n- Bbb middle remark.\n- Ccc fresh remark."
	if got := log.RecentContext(5, fresh+mid); got != want {
		t.Errorf("бюджет на 2 реплики: RecentContext =\n%q\nожидалось\n%q", got, want)
	}

	// Бюджет сразу на три — влезает и самая старая.
	got := log.RecentContext(5, fresh+mid+common.EstimateTokens("- Aaa oldest remark."))
	if !strings.Contains(got, "- Aaa oldest remark.") {
		t.Errorf("бюджет на 3 реплики должен включить самую старую: %q", got)
	}
}

// TestSpeechLogRecentContextIncludesConsumed — consumed-реплики остаются
// разговорной историей и рендерятся в блок наравне с unconsumed.
func TestSpeechLogRecentContextIncludesConsumed(t *testing.T) {
	log := NewSpeechLog(5, 10_000)
	log.Add("First.", speechAt(1))
	log.Add("Second.", speechAt(2))

	if text, ok := log.TakeLatestUnconsumed(); !ok || text != "Second." {
		t.Fatalf("TakeLatestUnconsumed = (%q, %v), ожидалось (%q, true)", text, ok, "Second.")
	}

	got := log.RecentContext(5, 10_000)
	for _, want := range []string{"- First.", "- Second."} {
		if !strings.Contains(got, want) {
			t.Errorf("RecentContext должен содержать потреблённую реплику же как историю: отсутствует %q:\n%s", want, got)
		}
	}
}

// TestSpeechLogSnapshotAfterFiltering — окна по Timestamp и consumed,
// порядок «старые первыми».
func TestSpeechLogSnapshotAfterFiltering(t *testing.T) {
	log := NewSpeechLog(10, 10_000)
	log.Add("A old.", speechAt(1))
	log.Add("B clarif.", speechAt(10))
	log.Add("C new.", speechAt(20))
	log.Consume(1) // B потреблена и всегда скрыта из снапшота

	cases := []struct {
		name string
		ts   time.Time
		want []string
	}{
		{name: "с начала, consumed скрыт", ts: time.Time{}, want: []string{"A old.", "C new."}},
		{name: "по consumed-реплике ровно", ts: speechAt(10), want: []string{"C new."}},
		{name: "ключ >= включает границу", ts: speechAt(20), want: []string{"C new."}},
		{name: "после всех", ts: speechAt(21), want: []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := log.SnapshotAfter(tc.ts)
			if len(got) != len(tc.want) {
				t.Fatalf("SnapshotAfter(%v) = %v, ожидалось %v", tc.ts, got, tc.want)
			}
			for i := range tc.want {
				if got[i].Text != tc.want[i] {
					t.Errorf("SnapshotAfter[%d] = %q, ожидалось %q (порядок «старые первыми»)", i, got[i].Text, tc.want[i])
				}
			}
		})
	}
}

// TestSpeechLogDefaultCapacityAndBudget — дефолты конструктора при <=0 и
// фолбэки RecentContext(maxRemarks<=0 / maxTokens<=0) на сохранённые лимиты.
func TestSpeechLogDefaultCapacityAndBudget(t *testing.T) {
	t.Run("constructor defaults", func(t *testing.T) {
		log := NewSpeechLog(0, 0)
		for i := 0; i < 21; i++ {
			log.Add(fmt.Sprintf("Remark %02d.", i), speechAt(i))
		}
		// Дефолт capacity=20: самая первая реплика вытеснена.
		if got := log.RecentContext(0, 0); strings.Contains(got, "- Remark 00.") {
			t.Errorf("дефолт capacity=20: первая реплика должна быть вытеснена:\n%s", got)
		}
		// maxRemarks<=0 → размер кольца (20 строк), бюджет 1500 не режет.
		if lines := strings.Count(log.RecentContext(0, 0), "\n- "); lines != 20 {
			t.Errorf("дефолт capacity=20 с RecentContext(0,0): строк = %d, ожидалось 20", lines)
		}
	})

	t.Run("constructor budget stored", func(t *testing.T) {
		log := NewSpeechLog(10, 1)
		log.Add("First.", speechAt(1))
		log.Add("Second.", speechAt(2))
		// maxTokens<=0 → сохранённый бюджет конструктора (1 токен): только свежая.
		want := "Interviewer remarks:\n- Second."
		if got := log.RecentContext(0, 0); got != want {
			t.Errorf("RecentContext(0, 0) =\n%q\nожидалось\n%q", got, want)
		}
	})
}

// TestSpeechLogConcurrentRace — параллельные Add/RecentContext/
// TakeLatestUnconsumed/Restore/SnapshotAfter/Consume не дают гонок (-race)
// и не ломают базовые инварианты кольца.
func TestSpeechLogConcurrentRace(t *testing.T) {
	log := NewSpeechLog(8, 500)
	remarks := []string{
		"Can you describe CQRS?",
		"I have five years of experience.",
		"Explain CI/CD pipelines.",
		"What is your approach to testing?",
		"How do you handle race conditions?",
		"Why did you choose Go?",
		"Walk me through your last project.",
		"Where is the diff between your DB and cache?",
	}

	var wg sync.WaitGroup
	for i, text := range remarks {
		wg.Add(1)
		go func(i int, text string) {
			defer wg.Done()
			ts := speechBase.Add(time.Duration(i) * time.Second)

			log.Add(text, ts)
			_ = log.RecentContext(4, 200)

			if taken, ok := log.TakeLatestUnconsumed(); ok {
				log.Restore(taken)
			}
			for _, r := range log.SnapshotAfter(speechBase) {
				_ = log.Consume(r.Seq)
			}
		}(i, text)
	}
	wg.Wait()

	// Детерминированная проверка: все реплики в датированном прошлом,
	// окно «после часа» пусто; кольцо не превысило вместимость.
	if got := log.SnapshotAfter(speechBase.Add(time.Hour)); len(got) != 0 {
		t.Fatalf("SnapshotAfter(+1h) должен быть пуст: %v", got)
	}
	if got := log.SnapshotAfter(time.Time{}); len(got) > 8 {
		t.Fatalf("кольцо capacity=8 превышено: %d unconused-реплик", len(got))
	}
}
