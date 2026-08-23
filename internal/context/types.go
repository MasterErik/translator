// Package candidatecontext реализует fact-level lexical retrieval для
// candidate context: загрузку manifest/index, построение индекса,
// токенизацию, section-aware scoring, бюджетирование по токенам.
//
// Внимание: имя пакета — candidatecontext (не context), чтобы не
// конфликтовать со стандартной библиотекой "context" у потребителей.
package candidatecontext

import "strings"

// IndexVersion — версия формата index.json. Меняется при несовместимых
// изменениях JSON schema, tokenizer semantics, canonicalization semantics,
// структуры индексируемых полей или алгоритма построения токенов.
// Несовместимая версия приводит к автоматической пересборке индекса.
const IndexVersion = 1

// FileMeta описывает один source-файл candidate context.
// Path — относительный путь с разделителем "/" (независимо от ОС).
type FileMeta struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Section — metadata секции для section-aware scoring.
// Fact.Section обязан ссылаться на существующий Manifest.Sections[].ID.
type Section struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Summary string   `json:"summary"`
	Tags    []string `json:"tags"`
}

// Fact — атомарная единица retrieval.
// Start/End — byte offsets тела факта внутри File (int64).
// Keywords, Aliases и ContentTokens в индексе уже canonicalized.
type Fact struct {
	ID            string   `json:"id"`
	Section       string   `json:"section"`
	File          string   `json:"file"`
	Start         int64    `json:"start"`
	End           int64    `json:"end"`
	Title         string   `json:"title"`
	Keywords      []string `json:"keywords"`
	Aliases       []string `json:"aliases"`
	ContentTokens []string `json:"content_tokens"`
}

// Manifest — профиль кандидата + глобальные aliases + metadata секций.
// Profile всегда используется в system prompt.
// Sections целиком в prompt не попадают.
type Manifest struct {
	Profile       string              `json:"profile"`
	GlobalAliases map[string][]string `json:"global_aliases"`
	Sections      []Section           `json:"sections"`
}

// Index — runtime retrieval index.
type Index struct {
	Version        int        `json:"version"`
	ManifestSHA256 string     `json:"manifest_sha256"`
	Files          []FileMeta `json:"files"`
	Facts          []Fact     `json:"facts"`
}

// RetrievalResult — результат retrieval одного факта.
// Content — точечно прочитанное содержимое факта (после ranking).
type RetrievalResult struct {
	FactID    string  `json:"fact_id"`
	SectionID string  `json:"section_id"`
	Score     float64 `json:"score"`
	Content   string  `json:"content"`
}

// CandidateContext — prompt-ready candidate context (profile + selected facts).
type CandidateContext struct {
	Profile string
	Facts   []RetrievalResult
}

// Render формирует prompt-ready строку: профиль, затем отобранные факты.
func (c CandidateContext) Render() string {
	var sb strings.Builder
	if c.Profile != "" {
		sb.WriteString("Candidate profile:\n")
		sb.WriteString(c.Profile)
	}
	for _, f := range c.Facts {
		if f.Content == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString(f.Content)
	}
	return sb.String()
}
