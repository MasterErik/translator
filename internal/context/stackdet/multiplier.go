package stackdet

import (
	"fmt"
	"sort"
	"strings"
)

// Multiplier вычисляет множитель скоринга факта по его стеку.
//
// Порядок правил (первое сработавшее побеждает):
//  1. factStack пуст → 1.0;
//  2. Penalty пусто (ни сессии, ни упоминаний) → 1.0;
//  3. Named непусто и factStack ∩ Named ≠ ∅ → Params.Bonus;
//  4. Named пусто, session-таргет не достигнут, и factStack ⊆ Penalty → Params.Session;
//  5. частичное пересечение с Penalty ∪ Exempt → 1.0;
//  6. иначе → Params.Penalty.
//
// Множества смоделированы независимо: session-таргет — это сессия без
// однозначно названных в вопросе стеков; в Sets он виден как Penalty минус
// Named (Exempt им не пересекается). Если Exempt непусто, правило 4 не
// применяется вовсе: факт этого стека даёт 1.0 по правилу 5.
//
// Правило 3 приоритетнее правила 4: стек, торчащий в факте из сессии, не
// получает сессионный бонус, если вопрос называет другой стек (иначе
// java-факты с общим словом обгоняют php-факт на явный php-вопрос).
//
// reason непуст при любом mult ≠ 1.0; при 1.0 — пустая строка.
func Multiplier(factStack []string, s Sets, p Params) (mult float64, reason string) {
	stack := cleanStack(factStack)
	if len(stack) == 0 {
		return 1.0, ""
	}
	if len(s.Penalty) == 0 {
		return 1.0, ""
	}
	p = p.effective()

	namedHit := intersectAny(stack, s.Named)
	if len(s.Named) > 0 && namedHit != "" {
		return p.Bonus, fmt.Sprintf("bonus: named %s", namedHit)
	}

	// Сессионный бонус: вопрос стек не называет (Named пусто), а весь стек
	// факта целиком входит в сессию. При Session=1.0 (нейтрален, M7b.5) бонуса
	// нет — возвращаем 1.0 без reason (инвариант: mult=1.0 → reason пуст).
	if len(s.Named) == 0 && allIn(stack, s.Penalty) {
		if p.Session == 1.0 {
			return 1.0, ""
		}
		return p.Session, fmt.Sprintf("bonus: session stack %s", formatStack(stack))
	}

	// Частичное пересечение: хотя бы один стек факта штрафной или
	// освобождённый — факт не считается полностью чужим. Сюда же попадает
	// Named пусто при непустом Exempt (session-таргет без бонуса).
	if _, hit := firstIn(stack, s.Penalty); hit {
		return 1.0, ""
	}
	if _, hit := firstIn(stack, s.Exempt); hit {
		return 1.0, ""
	}

	return p.Penalty, fmt.Sprintf("penalty: stack %s ∉ %s",
		formatStack(stack), formatSetKeys(s.Penalty))
}

// IsOutOfTarget сообщает, что стек факта целиком вне штрафного и
// освобождённого множеств: вопрос/сессия называет другие стеки, а этот — чужой.
// Пустой стек факта, пустой Penalty или пустые (после отбрасывания пустых
// строк) элементы → false: судить о «чуждости» не о чем.
//
// Для reference-фактов это пометка в выдаче, для hints — запрет на выдачу
// готового ответа (D8).
func IsOutOfTarget(factStack []string, s Sets) bool {
	stack := cleanStack(factStack)
	if len(stack) == 0 || len(s.Penalty) == 0 {
		return false
	}
	for _, st := range stack {
		if _, ok := s.Penalty[st]; ok {
			return false
		}
		if _, ok := s.Exempt[st]; ok {
			return false
		}
	}
	return true
}

// cleanStack возвращает пустой при пустом входе слайс без пустых элементов,
// сохраняя порядок и не трогая вход.
func cleanStack(factStack []string) []string {
	out := make([]string, 0, len(factStack))
	for _, st := range factStack {
		if st == "" {
			continue
		}
		out = append(out, st)
	}
	return out
}

// intersectAny возвращает первый по порядку стек факта, входящий в set, или "".
func intersectAny(stack []string, set map[string]struct{}) string {
	for _, st := range stack {
		if _, ok := set[st]; ok {
			return st
		}
	}
	return ""
}

// firstIn — то же, что intersectAny, но с булевым результатом.
func firstIn(stack []string, set map[string]struct{}) (string, bool) {
	hit := intersectAny(stack, set)
	return hit, hit != ""
}

// allIn сообщает, что каждый стек факта есть в set.
func allIn(stack []string, set map[string]struct{}) bool {
	for _, st := range stack {
		if _, ok := set[st]; !ok {
			return false
		}
	}
	return true
}

// formatStack форматирует стек факта как "[java, php]" (порядок входа).
func formatStack(stack []string) string {
	return "[" + strings.Join(stack, ", ") + "]"
}

// formatSetKeys форматирует множество стеков как "{java}" в
// детерминированном (отсортированном) порядке.
func formatSetKeys(set map[string]struct{}) string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return "{" + strings.Join(keys, ", ") + "}"
}
