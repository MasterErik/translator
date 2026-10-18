package candidatecontext

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Retriever выполняет fact-level lexical retrieval по уже построенному индексу.
// Вся runtime metadata (title/section tokens, dfMap) строится один раз в
// NewRetriever; Retrieve только считает score по запросу и точечно читает тела
// отобранных фактов (raw source не читается для ranking).
type Retriever struct {
	tokenizer *Tokenizer
	index     *Index
	root      string
	topK      int
	minScore  float64

	// M7a-пороги: 0 отключает соответствующий фильтр.
	contentGateTau    float64
	relativeThreshold float64

	dfMap          map[string]int
	titleTokens    [][]string            // ordered, выровнено с index.Facts
	titleSet       []map[string]struct{} // выровнено с index.Facts
	keywordSet     []map[string]struct{}
	aliasSet       []map[string]struct{}
	contentSet     []map[string]struct{}
	sectionTags    map[string]map[string]struct{}
	sectionSummary map[string]map[string]struct{}
}

// RetrieverOptions — настройки Retriever. Нулевые пороги означают defaults
// (DefaultContentGateTau / DefaultRelativeThreshold); чтобы отключить фильтр,
// передайте отрицательное значение.
type RetrieverOptions struct {
	TopK              int
	MinScore          float64
	ContentGateTau    float64
	RelativeThreshold float64
}

// NewRetriever строит runtime metadata для retrieval. manifest даёт
// global aliases (токенизатор) и metadata секций; index — факты. root —
// каталог, от которого резолвятся Fact.File при точечном чтении.
//
// topK <= 0 — без ограничения числа результатов; minScore < 0 — трактуется
// как 0.
func NewRetriever(manifest *Manifest, index *Index, root string, topK int, minScore float64) (*Retriever, error) {
	return NewRetrieverWithOptions(manifest, index, root, RetrieverOptions{
		TopK:     topK,
		MinScore: minScore,
	})
}

// NewRetrieverWithOptions — NewRetriever с явными порогами M7a.
func NewRetrieverWithOptions(manifest *Manifest, index *Index, root string, opts RetrieverOptions) (*Retriever, error) {
	if manifest == nil {
		return nil, fmt.Errorf("candidatecontext: new retriever: nil manifest")
	}
	if index == nil {
		return nil, fmt.Errorf("candidatecontext: new retriever: nil index")
	}

	tokenizer, err := NewTokenizer(manifest.GlobalAliases)
	if err != nil {
		return nil, fmt.Errorf("candidatecontext: new retriever: %w", err)
	}
	minScore := opts.MinScore
	if minScore < 0 {
		minScore = 0
	}
	gateTau := opts.ContentGateTau
	if gateTau == 0 {
		gateTau = DefaultContentGateTau
	}
	relThreshold := opts.RelativeThreshold
	if relThreshold == 0 {
		relThreshold = DefaultRelativeThreshold
	}

	n := len(index.Facts)
	r := &Retriever{
		tokenizer:         tokenizer,
		index:             index,
		root:              root,
		topK:              opts.TopK,
		minScore:          minScore,
		contentGateTau:    gateTau,
		relativeThreshold: relThreshold,
		dfMap:             make(map[string]int),
		titleTokens:       make([][]string, n),
		titleSet:          make([]map[string]struct{}, n),
		keywordSet:        make([]map[string]struct{}, n),
		aliasSet:          make([]map[string]struct{}, n),
		contentSet:        make([]map[string]struct{}, n),
		sectionTags:       make(map[string]map[string]struct{}, len(manifest.Sections)),
		sectionSummary:    make(map[string]map[string]struct{}, len(manifest.Sections)),
	}

	// Section metadata: flatten tags и summary в множества терминов.
	for _, s := range manifest.Sections {
		r.sectionTags[s.ID] = flattenTags(tokenizer, s.Tags)
		r.sectionSummary[s.ID] = toSet(tokenizer.Process(s.Summary))
	}

	// Fact metadata + document frequency (каждый факт учитывается максимум
	// один раз на термин).
	for i, f := range index.Facts {
		r.titleTokens[i] = tokenizer.Process(f.Title)
		r.titleSet[i] = toSet(r.titleTokens[i])
		r.keywordSet[i] = toSet(f.Keywords)
		r.aliasSet[i] = toSet(f.Aliases)
		r.contentSet[i] = toSet(f.ContentTokens)

		union := make(map[string]struct{})
		for t := range r.keywordSet[i] {
			union[t] = struct{}{}
		}
		for t := range r.titleSet[i] {
			union[t] = struct{}{}
		}
		for t := range r.aliasSet[i] {
			union[t] = struct{}{}
		}
		for t := range r.contentSet[i] {
			union[t] = struct{}{}
		}
		for t := range union {
			r.dfMap[t]++
		}
	}

	return r, nil
}

// flattenTags объединяет Process(tag) по всем Tags секции в множество терминов.
func flattenTags(tokenizer *Tokenizer, tags []string) map[string]struct{} {
	s := make(map[string]struct{})
	for _, tag := range tags {
		for _, t := range tokenizer.Process(tag) {
			s[t] = struct{}{}
		}
	}
	return s
}

// RetrieveOptions — опции одного вызова retrieval.
type RetrieveOptions struct {
	Session SessionContext
	// Explain заполняет Breakdown: диагностика. На ранжирование не влияет.
	Explain bool
}

// Retrieve возвращает отобранные факты в порядке убывания score (tie-break —
// FactID ASC). Пустой вопрос или вопрос без значимых терминов → nil. Тела
// фактов читаются точечно ТОЛЬКО после ranking; факты, чей raw source не
// удалось прочитать, пропускаются.
func (r *Retriever) Retrieve(question string) []RetrievalResult {
	return r.RetrieveWith(question, RetrieveOptions{})
}

// RetrieveWith — Retrieve с опциями сессии/explain. Порядок обработки факта:
// сырые вклады полей → content-only gate → section/phrase бонусы →
// стек-множитель (M7b) → relative threshold → topK.
func (r *Retriever) RetrieveWith(question string, opts RetrieveOptions) []RetrievalResult {
	cands := r.collect(question, opts)
	if len(cands) == 0 {
		return nil
	}

	var passed []scoredCandidate
	// Gate/threshold проставляются в collect/applyRelativeThreshold; факт
	// проходит только если оба вердикта пусты.
	cands = applyRelativeThreshold(cands, r.relativeThreshold)
	for _, c := range cands {
		if c.parts.Gate == "" && c.parts.Threshold == "" {
			passed = append(passed, c)
		}
	}
	if r.topK > 0 && len(passed) > r.topK {
		passed = passed[:r.topK]
	}
	if len(passed) == 0 {
		return nil
	}

	results := make([]RetrievalResult, 0, len(passed))
	for _, c := range passed {
		content, ok := r.readFactContent(c.fact)
		if !ok {
			continue
		}
		results = append(results, r.result(c, content, opts))
	}
	return results
}

// ExplainQuestion возвращает диагностику всех фактов, у которых есть
// fact-level совпадение с вопросом — включая отброшенные гейтом и порогом.
// Только для инструментов калибровки и тестов: поведение ранжирования
// совпадает с RetrieveWith.
func (r *Retriever) ExplainQuestion(question string, opts RetrieveOptions) []ExplainItem {
	opts.Explain = true
	cands := r.collect(question, opts)
	if len(cands) == 0 {
		return nil
	}
	cands = applyRelativeThreshold(cands, r.relativeThreshold)

	items := make([]ExplainItem, 0, len(cands))
	for _, c := range cands {
		reason := c.parts.Gate
		if reason == "" {
			reason = c.parts.Threshold
		}
		items = append(items, ExplainItem{
			FactID:     c.fact.ID,
			SectionID:  c.fact.Section,
			Passed:     reason == "",
			RejectedBy: reason,
			Breakdown:  r.breakdown(c),
		})
	}
	return items
}

// scoredCandidate — факт с разложением score (включая отброшенные).
type scoredCandidate struct {
	fact  Fact
	parts scoreParts
	ratio float64
}

// collect считает кандидатов по вопросу: все факты с ненулевым fact-level
// совпадением. Отброшенные гейтом/min_score остаются в списке с заполненным
// parts.Gate — их видит explain, но не RetrieveWith. Список отсортирован по
// rankScore убыв. (tie-break — FactID ASC).
func (r *Retriever) collect(question string, opts RetrieveOptions) []scoredCandidate {
	trimmed := strings.TrimSpace(question)
	if trimmed == "" {
		return nil
	}
	qTokens := r.tokenizer.Process(trimmed)
	if len(qTokens) == 0 {
		return nil
	}
	qSet := toSet(qTokens)

	n := len(r.index.Facts)
	var questionIDF float64
	for term := range qSet {
		questionIDF += idfValue(r.dfMap, n, term)
	}
	sectionScores := r.sectionScores(qSet)

	cands := make([]scoredCandidate, 0, n)
	for i, f := range r.index.Facts {
		parts := r.factParts(i, sectionScores[f.Section], qSet, qTokens, opts)
		if parts.fieldScore() == 0 {
			// Нет fact-level совпадения — факт не всплывает за счёт section
			// metadata (tags/summary).
			continue
		}
		var ratio float64
		if questionIDF > 0 {
			ratio = parts.Content / contentWeight / questionIDF
		}
		if reason := contentGateReason(parts, ratio, r.contentGateTau); reason != "" {
			parts.Gate = reason
		} else if r.minScore > 0 && parts.raw() < r.minScore {
			parts.Gate = fmt.Sprintf("min_score: raw %.3f < %.3f", parts.raw(), r.minScore)
		}
		cands = append(cands, scoredCandidate{fact: f, parts: parts, ratio: ratio})
	}

	sort.Slice(cands, func(i, j int) bool {
		if cands[i].parts.rankScore() != cands[j].parts.rankScore() {
			return cands[i].parts.rankScore() > cands[j].parts.rankScore()
		}
		return cands[i].fact.ID < cands[j].fact.ID
	})
	return cands
}

// factParts считает разложение score факта:
//
//	field      = keywords*Σ idf(kw) + title*Σ idf(title) + aliases*Σ idf(alias)
//	           + content*Σ idf(content)
//	section    = sectionScore * sectionWeight
//	phrase     = phrase bonus (биграммы title/content)
//	core       = (field + phrase) * mult        — сопоставимая часть
//	rankScore  = core + section
//	raw        = field + section + phrase       — score без стек-множителя
func (r *Retriever) factParts(i int, sectionScore float64, qSet map[string]struct{}, qTokens []string, opts RetrieveOptions) scoreParts {
	n := len(r.index.Facts)
	parts := scoreParts{
		Keywords: keywordsWeight * sumIDF(qSet, r.keywordSet[i], r.dfMap, n),
		Title:    titleWeight * sumIDF(qSet, r.titleSet[i], r.dfMap, n),
		Aliases:  aliasesWeight * sumIDF(qSet, r.aliasSet[i], r.dfMap, n),
		Content:  contentWeight * sumIDF(qSet, r.contentSet[i], r.dfMap, n),
		Section:  sectionScore * sectionWeight,
		Phrase:   phraseBonusValue(r.titleTokens[i], r.index.Facts[i].ContentTokens, qTokens),
	}
	parts.Mult, parts.MultReason, parts.OutOfTarget = r.stackMultiplier(i, opts)
	return parts
}

// stackMultiplier — стек-множитель факта (M7b). В M7a не применяется: вся
// выдача идёт с множителем 1. Реализация M7b использует session.TargetStack,
// стек факта и stackdet.
func (r *Retriever) stackMultiplier(_ int, _ RetrieveOptions) (float64, string, bool) {
	return 1, "", false
}

// result материализует кандидата в RetrievalResult (Breakdown — только при
// opts.Explain).
func (r *Retriever) result(c scoredCandidate, content string, opts RetrieveOptions) RetrievalResult {
	res := RetrievalResult{
		FactID:      c.fact.ID,
		SectionID:   c.fact.Section,
		Score:       c.parts.rankScore(),
		Content:     content,
		Raw:         c.parts.raw(),
		Rank:        c.parts.rankScore(),
		OutOfTarget: c.parts.OutOfTarget,
	}
	if opts.Explain {
		b := r.breakdown(c)
		res.Breakdown = &b
	}
	return res
}

// breakdown собирает диагностическое разложение кандидата.
func (r *Retriever) breakdown(c scoredCandidate) ScoreBreakdown {
	p := c.parts
	return ScoreBreakdown{
		Keywords:     p.Keywords,
		Title:        p.Title,
		Aliases:      p.Aliases,
		Content:      p.Content,
		Section:      p.Section,
		Phrase:       p.Phrase,
		FieldScore:   p.fieldScore(),
		Core:         p.core(),
		Raw:          p.raw(),
		Rank:         p.rankScore(),
		ContentRatio: c.ratio,
		Mult:         p.mult(),
		MultReason:   p.MultReason,
		Gate:         p.Gate,
		Threshold:    p.Threshold,
	}
}

// scoreParts — разложение score факта на составляющие (M7a). Поля заполняются
// и без Explain (стоимость — те же вычисления, что и раньше), чтобы gate и
// пороги сравнивали именно те величины, которые видит explain.
type scoreParts struct {
	Keywords float64 // keywordsWeight × Σ idf(совпавшие keywords)
	Title    float64 // titleWeight × Σ idf(совпавшие title-термины)
	Aliases  float64 // aliasesWeight × Σ idf(совпавшие aliases)
	Content  float64 // contentWeight × Σ idf(совпавшие content-термины)

	Section float64 // sectionScore × sectionWeight
	Phrase  float64 // phrase bonus (title/content биграммы)

	// Mult и MultReason — стек-множитель (M7b); в M7a всегда 1.
	Mult       float64
	MultReason string
	// OutOfTarget — факт со стеком вне Penalty, выживший в выдаче (M7b).
	OutOfTarget bool

	// Gate — вердикт content-only гейта: пусто, если факт прошёл.
	Gate string
	// Threshold — вердикт relative threshold: пусто, если факт прошёл.
	Threshold string
}

// fieldScore — сумма совпадений по keywords/title/aliases/content.
func (p scoreParts) fieldScore() float64 {
	return p.Keywords + p.Title + p.Aliases + p.Content
}

// core — «сопоставимая» часть score без section-бонуса: sectionScore — почти
// константа по секциям, и в relative threshold она сдвигала бы порог (M7a).
func (p scoreParts) core() float64 {
	return (p.fieldScore() + p.Phrase) * p.mult()
}

// mult возвращает стек-множитель (1, если множители не применялись).
func (p scoreParts) mult() float64 {
	if p.Mult == 0 {
		return 1
	}
	return p.Mult
}

// raw — score без стек-множителя: ровно то значение, что считала реализация до
// M7 (для min_score и абсолютного порога подсказок).
func (p scoreParts) raw() float64 {
	return p.fieldScore() + p.Section + p.Phrase
}

// rankScore — score для сортировки и relative threshold: без section-бонуса,
// но со стек-множителем.
func (p scoreParts) rankScore() float64 {
	return p.core() + p.Section
}

// readFactContent точечно читает тело факта из raw source (byte offsets) и
// обрезает пробелы. Возвращает ok=false при любой ошибке чтения/валидности
// offsets — такой факт пропускается, но ranking не ломается.
func (r *Retriever) readFactContent(fact Fact) (string, bool) {
	if fact.End < fact.Start {
		return "", false
	}
	path := filepath.Join(r.root, filepath.FromSlash(fact.File))
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()

	sr := io.NewSectionReader(f, fact.Start, fact.End-fact.Start)
	data, err := io.ReadAll(sr)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(data)), true
}
