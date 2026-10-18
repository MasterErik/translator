package candidatecontext

import (
	"fmt"
	"math"
)

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

// Пороги M7a (defaults; переопределяются через RetrieverOptions).
//
// Оба значения калиброваны на датасетах (legacy train/holdout как gate + v2
// train/holdout), см. .hermes/plans/2026-10-18_candidate-context-precision-v3.1.md:
//
//	tau  0.05..0.15 → Recall как без гейта (0.643 R@5 на v2-train), Leak 0.093;
//	     tau 0.20    → теряется relevant-факт q017 (ratio 0.155), Leak 0.000 на holdout;
//	     поэтому 0.15 — верхняя граница без потери recall.
//	rel  0.60 → Leak 0.093/0.067 без изменений Recall; 0.70 уже режет
//	     legacy-train R@3 (0.867 → 0.800) — регресс gate.
const (
	// DefaultContentGateTau — минимальная доля IDF вопроса, покрытая телом
	// факта, при отсутствии попаданий в keywords/title/aliases. Ниже порога
	// факт считается совпавшим «случайно» и отбрасывается: matched_content_IDF
	// по длинному телу легко набирается на служебных словах.
	DefaultContentGateTau = 0.15
	// DefaultRelativeThreshold — доля лучшего (неоштрафованного) core, ниже
	// которой факт не показывается: absolute min_score не масштабируется под
	// длину вопроса и размер индекса.
	DefaultRelativeThreshold = 0.6
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

// contentGateReason — content-only gate (M7a). Факт с попаданием в
// keywords/title/aliases проходит всегда: это осознанная разметка, и она важнее
// объёма тела. Факт, совпавший только по телу, проходит, если тело покрывает
// вопрос не меньше tau (matched_content_IDF / question_IDF ≥ tau).
// Возвращает пустую строку, если факт прошёл.
func contentGateReason(p scoreParts, ratio float64, tau float64) string {
	if p.Keywords > 0 || p.Title > 0 || p.Aliases > 0 {
		return ""
	}
	if tau <= 0 || ratio >= tau {
		return ""
	}
	return fmt.Sprintf("content_only: ratio %.3f < tau %.2f", ratio, tau)
}

// applyRelativeThreshold помечает кандидатов, чей core ниже доли от лучшего
// core среди неоштрафованных (M7a). Опорный top-1 берётся без section-бонуса:
// sectionScore почти одинаков во всех секциях и сдвигал бы порог вверх.
// Если неоштрафованных кандидатов нет, возвращается nil — выдача пуста.
// Порядок кандидатов сохраняется; вердикт пишется в parts.Threshold.
func applyRelativeThreshold(cands []scoredCandidate, tau float64) []scoredCandidate {
	out := make([]scoredCandidate, len(cands))
	copy(out, cands)

	var ref float64
	for _, c := range out {
		if c.parts.Gate != "" || c.parts.OutOfTarget {
			continue
		}
		if core := c.parts.core(); core > ref {
			ref = core
		}
	}
	if ref <= 0 {
		return nil
	}
	if tau <= 0 {
		return out
	}

	minCore := ref * tau
	for i := range out {
		if out[i].parts.Gate != "" || out[i].parts.OutOfTarget {
			continue
		}
		if out[i].parts.core() < minCore {
			out[i].parts.Threshold = fmt.Sprintf("relative: core %.3f < %.3f (top1 %.3f × %.2f)",
				out[i].parts.core(), minCore, ref, tau)
		}
	}
	return out
}
