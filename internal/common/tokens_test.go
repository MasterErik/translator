package common

import "testing"

func TestEstimateTokensEmpty(t *testing.T) {
	if got := EstimateTokens(""); got != 0 {
		t.Errorf("EstimateTokens(%q) = %d, want 0", "", got)
	}
}

func TestEstimateTokensByteHeuristic(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 0},
		{"one byte", "a", 1},
		{"three bytes", "abc", 1},
		{"four bytes", "abcd", 1},
		{"five bytes", "abcde", 2},
		{"eight bytes", "abcdefgh", 2},
		{"nine bytes", "abcdefghi", 3},
		{"multi-byte runes", "абвг", 2}, // 4 runes x 2 bytes = 8 bytes
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EstimateTokens(tt.in); got != tt.want {
				t.Errorf("EstimateTokens(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestEstimateTokensNonEmptyAlwaysAtLeastOne(t *testing.T) {
	for _, s := range []string{"a", "ab", "abc", "abcd", "абвг", "🙂"} {
		if got := EstimateTokens(s); got < 1 {
			t.Errorf("EstimateTokens(%q) = %d, want >= 1", s, got)
		}
	}
}

func TestEstimateTokensDeterministic(t *testing.T) {
	// Приёмка: детерминированная эвристика, а не точный подсчёт токенов.
	// Повторные вызовы на одном входе обязаны давать одинаковый результат.
	inputs := []string{
		"",
		"a",
		"hello world",
		"Микросервисы и распределённые системы",
		"🙂🙂🙂🙂🙂",
	}
	for _, in := range inputs {
		first := EstimateTokens(in)
		for i := 0; i < 100; i++ {
			if got := EstimateTokens(in); got != first {
				t.Fatalf("EstimateTokens(%q) недетерминирован: %d затем %d", in, first, got)
			}
		}
	}
}

func TestEstimateTokensUnicodeStable(t *testing.T) {
	// Юникод (русский/эмодзи): оценка стабильна и >= 1 для непустого входа.
	tests := []struct {
		name string
		in   string
	}{
		{"russian", "Привет, как дела? Это длинная русская строка."},
		{"emoji", "🚀🔥💡"},
		{"mixed", "Встреча по Kubernetes: 3 pod'а, 2 namespace'а 🙂"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first := EstimateTokens(tt.in)
			if first < 1 {
				t.Errorf("EstimateTokens(%q) = %d, want >= 1", tt.in, first)
			}
			if got := EstimateTokens(tt.in); got != first {
				t.Errorf("EstimateTokens(%q) недетерминирован: %d затем %d", tt.in, first, got)
			}
		})
	}
}

func TestEstimateTokensLongITTextDeterministic(t *testing.T) {
	// Длинный IT-heavy текст: оценка детерминирована и не зависит от числа вызовов.
	in := "Kubernetes cluster with 12 nodes running 400 microservices, " +
		"gRPC service mesh, Prometheus metrics, Grafana dashboards, " +
		"and a NATS JetStream event bus handling 50k messages per second."
	first := EstimateTokens(in)
	for i := 0; i < 50; i++ {
		if got := EstimateTokens(in); got != first {
			t.Fatalf("EstimateTokens недетерминирован: %d затем %d", first, got)
		}
	}
	if first < 1 {
		t.Errorf("EstimateTokens(long text) = %d, want >= 1", first)
	}
}

// TestEstimateTokensNoHiddenState подтверждает контракт «single replacement point»:
// EstimateTokens — единственная точка оценки токенов и для conversation history,
// и для candidate context (см. internal/context/budget.go — NewBudgeter/Budget).
// Функция чистая: результат зависит только от входной строки, скрытого состояния
// нет, поэтому любая последовательность вызовов даёт одинаковые значения.
func TestEstimateTokensNoHiddenState(t *testing.T) {
	history := "question: what is your experience with Go? answer: five years of production experience"
	candidate := "Candidate: backend engineer, 5 years Go, Kubernetes, PostgreSQL"

	// Ожидаемые значения при изолированных вызовах.
	histAlone := EstimateTokens(history)
	candAlone := EstimateTokens(candidate)

	// Перемешиваем последовательность вызовов; результат обязан совпасть.
	for i := 0; i < 20; i++ {
		gotHist := EstimateTokens(history)
		gotCand := EstimateTokens(candidate)
		if gotHist != histAlone {
			t.Fatalf("history: EstimateTokens изменился после перемешивания: %d затем %d", histAlone, gotHist)
		}
		if gotCand != candAlone {
			t.Fatalf("candidate: EstimateTokens изменился после перемешивания: %d затем %d", candAlone, gotCand)
		}
	}
}
