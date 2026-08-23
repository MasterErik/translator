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

	dfMap          map[string]int
	titleTokens    [][]string            // ordered, выровнено с index.Facts
	titleSet       []map[string]struct{} // выровнено с index.Facts
	keywordSet     []map[string]struct{}
	aliasSet       []map[string]struct{}
	contentSet     []map[string]struct{}
	sectionTags    map[string]map[string]struct{}
	sectionSummary map[string]map[string]struct{}
}

// NewRetriever строит runtime metadata для retrieval. manifest даёт
// global aliases (токенизатор) и metadata секций; index — факты. root —
// каталог, от которого резолвятся Fact.File при точечном чтении.
//
// topK <= 0 — без ограничения числа результатов; minScore < 0 — трактуется
// как 0.
func NewRetriever(manifest *Manifest, index *Index, root string, topK int, minScore float64) (*Retriever, error) {
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
	if minScore < 0 {
		minScore = 0
	}

	n := len(index.Facts)
	r := &Retriever{
		tokenizer:      tokenizer,
		index:          index,
		root:           root,
		topK:           topK,
		minScore:       minScore,
		dfMap:          make(map[string]int),
		titleTokens:    make([][]string, n),
		titleSet:       make([]map[string]struct{}, n),
		keywordSet:     make([]map[string]struct{}, n),
		aliasSet:       make([]map[string]struct{}, n),
		contentSet:     make([]map[string]struct{}, n),
		sectionTags:    make(map[string]map[string]struct{}, len(manifest.Sections)),
		sectionSummary: make(map[string]map[string]struct{}, len(manifest.Sections)),
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

// Retrieve возвращает отобранные факты в порядке убывания score (tie-break —
// FactID ASC). Пустой вопрос или вопрос без значимых терминов → nil. Тела
// фактов читаются точечно ТОЛЬКО после ranking; факты, чей raw source не
// удалось прочитать, пропускаются.
func (r *Retriever) Retrieve(question string) []RetrievalResult {
	trimmed := strings.TrimSpace(question)
	if trimmed == "" {
		return nil
	}
	qTokens := r.tokenizer.Process(trimmed)
	if len(qTokens) == 0 {
		return nil
	}
	qSet := toSet(qTokens)

	sectionScores := r.sectionScores(qSet)

	type ranked struct {
		fact  Fact
		score float64
	}
	var rankedFacts []ranked
	for i, f := range r.index.Facts {
		score := r.scoreFact(i, sectionScores[f.Section], qSet, qTokens)
		if score == 0 {
			continue
		}
		if r.minScore > 0 && score < r.minScore {
			continue
		}
		rankedFacts = append(rankedFacts, ranked{fact: f, score: score})
	}

	sort.Slice(rankedFacts, func(i, j int) bool {
		if rankedFacts[i].score != rankedFacts[j].score {
			return rankedFacts[i].score > rankedFacts[j].score
		}
		return rankedFacts[i].fact.ID < rankedFacts[j].fact.ID
	})

	if r.topK > 0 && len(rankedFacts) > r.topK {
		rankedFacts = rankedFacts[:r.topK]
	}

	results := make([]RetrievalResult, 0, len(rankedFacts))
	for _, rf := range rankedFacts {
		content, ok := r.readFactContent(rf.fact)
		if !ok {
			continue
		}
		results = append(results, RetrievalResult{
			FactID:    rf.fact.ID,
			SectionID: rf.fact.Section,
			Score:     rf.score,
			Content:   content,
		})
	}
	return results
}

// scoreFact считает combined score одного факта для запроса:
//
//	factScore = keywords*Σ idf(kw) + title*Σ idf(title) + aliases*Σ idf(alias)
//	          + content*Σ idf(content)
//	combined  = factScore + sectionBonus + phraseBonus
func (r *Retriever) scoreFact(i int, sectionScore float64, qSet map[string]struct{}, qTokens []string) float64 {
	n := len(r.index.Facts)
	factScore := keywordsWeight*sumIDF(qSet, r.keywordSet[i], r.dfMap, n) +
		titleWeight*sumIDF(qSet, r.titleSet[i], r.dfMap, n) +
		aliasesWeight*sumIDF(qSet, r.aliasSet[i], r.dfMap, n) +
		contentWeight*sumIDF(qSet, r.contentSet[i], r.dfMap, n)

	// Нет fact-level совпадения (keywords/title/aliases/content) — факт не
	// должен всплывать только за счёт section metadata (tags/summary). Phrase
	// bonus не ломается: он требует overlap в title/content, что уже даёт
	// factScore > 0.
	if factScore == 0 {
		return 0
	}

	sectionBonus := sectionScore * sectionWeight
	phraseBonus := phraseBonusValue(r.titleTokens[i], r.index.Facts[i].ContentTokens, qTokens)
	return factScore + sectionBonus + phraseBonus
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
