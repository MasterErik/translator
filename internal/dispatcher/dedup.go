package dispatcher

import (
	"strings"
	"unicode"
)

// F4 (план fix-plan-2026-11, A6): фаззи-дедуп вопросов.
//
// СTU часто перефразирует вопрос несколько раз подряд (partial → corrected
// final, пересборка фразы): текстовые утилиты ниже распознают «почти тот же
// вопрос» — tokens-Jaccard >= questionDupThreshold после нормализации —
// и route не создаёт для него вторую метку.

// questionDupThreshold — порог дедупа по token-Jaccard (A6: >= 0.75).
const questionDupThreshold = 0.75

// dedupStopTokens — служебные дискурсивные слова, не различающие вопросы
// (A6: стоп-фразы "so", "ok", "right", "yeah"): «So, ok, how do you scale?» и
// «How do you scale?» — один вопрос. Убираются на обоих концах сравнения.
var dedupStopTokens = map[string]struct{}{
	"so":    {},
	"ok":    {},
	"okay":  {},
	"right": {},
	"yeah":  {},
	"well":  {},
}

// questionTokens нормализует вопрос и режет его на значимые токены:
// lowercase, пунктуация → разделители, стоп-токены отбрасываются.
// Пустой результат (одни стоп-слова) — валиден: Jaccard на пустых
// множествах трактуется вызывающим кодом (tokenJaccard возвращает 1.0 для
// двух пустых: одинаковые пустые вопросы считаются дублями).
func questionTokens(text string) []string {
	sanitized := strings.Map(func(r rune) rune {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'':
			return unicode.ToLower(r)
		default:
			return ' '
		}
	}, text)

	tokens := strings.Fields(sanitized)
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if _, stop := dedupStopTokens[t]; stop {
			continue
		}
		out = append(out, t)
	}
	return out
}

// tokenJaccard возвращает коэффициент Жаккара двух множеств токенов
// |A∩B|/|A∪B|. Токены сравниваются как множества (повторы не учитываются).
// Два пустых множества — 1.0 (тривиально совпадают); ровно одно пустое — 0.
func tokenJaccard(a, b []string) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1.0
	}
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	ma := make(map[string]struct{}, len(a))
	for _, t := range a {
		ma[t] = struct{}{}
	}
	mb := make(map[string]struct{}, len(b))
	for _, t := range b {
		mb[t] = struct{}{}
	}
	inter := 0
	for t := range ma {
		if _, in := mb[t]; in {
			inter++
		}
	}
	return float64(inter) / float64(len(ma)+len(mb)-inter)
}

// fuzzyDuplicateQuestion — «почти тот же вопрос»: Jaccard по normalized-токенам
// >= questionDupThreshold.
func fuzzyDuplicateQuestion(a, b string) bool {
	return tokenJaccard(questionTokens(a), questionTokens(b)) >= questionDupThreshold
}
