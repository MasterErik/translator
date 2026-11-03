package dispatcher

import (
	"testing"
)

// ── F4/A6: нормализация и фаззи-дедуп вопросов ────────────────────────────

// TestQuestionTokens — таблица нормализации: lowercase, пунктуация → пробелы,
// стоп-токены дискурса отбрасываются.
func TestQuestionTokens(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"пустой", "", []string{}},
		{"стоп-слова и пунктуация", "So, ok — how do you scale?", []string{"how", "do", "you", "scale"}},
		{"lowercase и цифры", "GO-1.22 vs Go 1.22?", []string{"go", "1", "22", "vs", "go", "1", "22"}},
		{"апостроф", "Don't you use Docker?", []string{"don't", "you", "use", "docker"}},
		{"только стоп-слова", "ok, right, yeah", []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := questionTokens(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("questionTokens(%q) = %v, ожидалось %v", c.in, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("questionTokens(%q)[%d] = %q, ожидалось %q", c.in, i, got[i], c.want[i])
				}
			}
		})
	}
}

// TestTokenJaccard — коэффициент Жаккара и краевые случаи.
func TestTokenJaccard(t *testing.T) {
	cases := []struct {
		name string
		a, b []string
		want float64
	}{
		{"оба пустых", nil, nil, 1.0},
		{"только a пустое", nil, []string{"x"}, 0.0},
		{"только b пустое", []string{"x"}, nil, 0.0},
		{"идентичные", []string{"a", "b"}, []string{"a", "b"}, 1.0},
		{"непересекающиеся", []string{"a", "b"}, []string{"c", "d"}, 0.0},
		{"пересечение одно из двух", []string{"a", "b"}, []string{"a", "c"}, 1.0 / 3.0},
		{"повторы не учитываются", []string{"a", "a", "b"}, []string{"a", "b"}, 1.0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := tokenJaccard(c.a, c.b); got != c.want {
				t.Fatalf("tokenJaccard = %v, ожидалось %v", got, c.want)
			}
		})
	}
}

// TestFuzzyDuplicateQuestion — порог 0.75: перефразировки отбрасываются,
// разные вопросы остаются.
func TestFuzzyDuplicateQuestion(t *testing.T) {
	cases := []struct {
		name string
		newQ string
		prev string
		dup  bool
	}{
		{"точный повтор", "How do you scale?", "How do you scale?", true},
		{"вставные стоп-фразы", "So, ok, how do you scale?", "How do you scale?", true},
		{"добавленная деталь", "How do you scale, exactly?", "How do you scale?", true},
		{"порядок слов", "You do scale how?", "How do you scale?", true},
		{"разный вопрос", "What is your experience?", "How do you scale?", false},
		{"похожий, но другой", "What is your work?", "What is your experience?", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := fuzzyDuplicateQuestion(c.newQ, c.prev)
			want := c.dup
			if got != want {
				t.Fatalf("fuzzyDuplicateQuestion(%q, %q) = %v, ожидалось %v", c.newQ, c.prev, got, want)
			}
		})
	}
}

// TestFollowUpFragment — фразы-дополнения всегда детектируются; пересечение
// токенов (>= 0.6) — надёжный fail-safe: ложная склейка разных быстрых
// вопросов хуже пропущенной склейки (см. coalesceFragment).
func TestFollowUpFragment(t *testing.T) {
	main := "Do you have experience with Go?"
	cases := []struct {
		name     string
		fragment string
		prev     string
		want     bool
	}{
		{"начало «or»", "or only since 2019?", main, true},
		{"начало «and»", "And what about latency?", main, true},
		{"начало «what about»", "what about distributed systems?", main, true},
		{"пересечение >= 0.6", "Do you have experience with Go in prod?", main, true},
		{"самостоятельный вопрос", "What is your work?", main, false},
		{"независимый быстрый вопрос", "What is your experience?", "What is your work?", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := followUpFragment(c.fragment, c.prev); got != c.want {
				t.Fatalf("followUpFragment(%q, %q) = %v, ожидалось %v", c.fragment, c.prev, got, c.want)
			}
		})
	}
}

// TestAppendQuestion — дописывание фрагмента: пробел-разделитель, нормализация
// краевых пробелов, no-op для пустых/вытесненных меток.
func TestAppendQuestion(t *testing.T) {
	tl := NewQuestionTimeline()

	if _, ok := tl.AppendQuestion(0, "and the team size?"); ok {
		t.Fatal("AppendQuestion по пустому таймлайну должен быть no-op")
	}

	idx := tl.Add("How do you scale?")

	if _, ok := tl.AppendQuestion(idx, "   "); ok {
		t.Fatal("пустой фрагмент должен быть no-op")
	}

	full, ok := tl.AppendQuestion(idx, "  Or only in regions?  ")
	if !ok {
		t.Fatal("валидный фрагмент отклонён")
	}
	if full != "How do you scale? Or only in regions?" {
		t.Fatalf("full = %q, ожидалось %q", full, "How do you scale? Or only in regions?")
	}

	if full, ok := tl.AppendQuestion(999, "anything?"); ok || full != "" {
		t.Fatalf("AppendQuestion по несуществующей метке = (%q, %v), ожидалось no-op", full, ok)
	}
}
