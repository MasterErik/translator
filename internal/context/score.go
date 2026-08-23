package candidatecontext

import "math"

// Tuning-веса полей и бонусов (tuning defaults, не архитектурные гарантии).
const (
	keywordsWeight = 5.0
	titleWeight    = 4.0
	aliasesWeight  = 3.0
	contentWeight  = 2.0

	tagsWeight    = 3.0
	summaryWeight = 2.0
	sectionWeight = 0.3

	phraseTitleBonus   = 3.0
	phraseContentBonus = 1.0
	phraseBonusCap     = 6.0
)

// toSet собирает множество терминов из слайса (порядок не важен).
func toSet(tokens []string) map[string]struct{} {
	s := make(map[string]struct{}, len(tokens))
	for _, t := range tokens {
		s[t] = struct{}{}
	}
	return s
}

// idfValue — smoothed inverse document frequency. df — число фактов, в которых
// термин встречается хотя бы раз; df==0 трактуется как редчайший термин
// (отсутствует в dfMap — например, термин из section tags/summary, не
// встречающийся ни в одном факте).
func idfValue(dfMap map[string]int, n int, term string) float64 {
	df := dfMap[term]
	return math.Log((float64(n)+1)/(float64(df)+1)) + 1
}

// sumIDF суммирует idf по терминам из qSet, присутствующим в field.
// Термин учитывается максимум один раз на поле (qSet — множество).
func sumIDF(qSet, field map[string]struct{}, dfMap map[string]int, n int) float64 {
	var s float64
	for term := range qSet {
		if _, ok := field[term]; ok {
			s += idfValue(dfMap, n, term)
		}
	}
	return s
}

// hasBigram сообщает, встречается ли пара (a, b) подряд (contiguous) в tokens.
func hasBigram(tokens []string, a, b string) bool {
	for i := 0; i+1 < len(tokens); i++ {
		if tokens[i] == a && tokens[i+1] == b {
			return true
		}
	}
	return false
}

// bigramHits возвращает число биграмм вопроса, найденных contiguous в tokens.
func bigramHits(tokens, qTokens []string) int {
	if len(qTokens) < 2 {
		return 0
	}
	hits := 0
	for i := 0; i+1 < len(qTokens); i++ {
		if hasBigram(tokens, qTokens[i], qTokens[i+1]) {
			hits++
		}
	}
	return hits
}

// phraseBonusValue вычисляет phrase bonus для одного факта: title-биграммы
// дают +3 каждая, content-биграммы +1, сумма ограничена сверху cap (6).
// keywords/aliases в phrase matching не участвуют.
func phraseBonusValue(titleTokens, contentTokens, qTokens []string) float64 {
	if len(qTokens) < 2 {
		return 0
	}
	titleHits := bigramHits(titleTokens, qTokens)
	contentHits := bigramHits(contentTokens, qTokens)
	bonus := phraseTitleBonus*float64(titleHits) + phraseContentBonus*float64(contentHits)
	if bonus > phraseBonusCap {
		bonus = phraseBonusCap
	}
	return bonus
}

// sectionScores вычисляет sectionScore для всех секций один раз за запрос:
//
//	sectionScore(s) = tagsWeight * Σ idf(term), term ∈ qSet ∩ tags[s]
//	                + summaryWeight * Σ idf(term), term ∈ qSet ∩ summary[s]
func (r *Retriever) sectionScores(qSet map[string]struct{}) map[string]float64 {
	n := len(r.index.Facts)
	out := make(map[string]float64, len(r.sectionTags))
	for id, tags := range r.sectionTags {
		s := tagsWeight * sumIDF(qSet, tags, r.dfMap, n)
		if summary, ok := r.sectionSummary[id]; ok {
			s += summaryWeight * sumIDF(qSet, summary, r.dfMap, n)
		}
		out[id] = s
	}
	return out
}
