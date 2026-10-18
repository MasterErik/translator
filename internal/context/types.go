// Package candidatecontext реализует fact-level lexical retrieval для
// candidate context: загрузку manifest/index, построение индекса,
// токенизацию, section-aware scoring, бюджетирование по токенам.
//
// Внимание: имя пакета — candidatecontext (не context), чтобы не
// конфликтовать со стандартной библиотекой "context" у потребителей.
package candidatecontext

import (
	"strings"

	"github.com/mastererik/translator/internal/context/stackdet"
)

// IndexVersion — версия формата index.json. Меняется при несовместимых
// изменениях JSON schema, tokenizer semantics, canonicalization semantics,
// структуры индексируемых полей или алгоритма построения токенов.
// Несовместимая версия приводит к автоматической пересборке индекса.
const IndexVersion = 2

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
// Keywords, Aliases, ContentTokens и Stack в индексе уже canonicalized
// (Stack — тем же Tokenizer'ом, ключ стека совпадает с Family.Name, например
// "golang", а не "go").
// Period — интервал занятости по regex ^\d{4}(-\d{4}|-present)?$;
// Weight — значимость факта (enum, см. M6b).
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
	Stack         []string `json:"stack,omitempty"`
	Period        string   `json:"period,omitempty"`
	Weight        string   `json:"weight,omitempty"`
}

// Manifest — профиль кандидата + глобальные aliases + metadata секций.
// Profile всегда используется в system prompt.
// Sections целиком в prompt не попадают.
// TechFamilies — белый список семейств технологий для stackdet (M3): он же
// служит источником допустимых значений Fact.Stack при валидации (M6b).
// TechnicalMatrix — компактная таблица стек×период, рендерится отдельным
// полем prompt'а с собственным бюджетом max_matrix_tokens (M4).
type Manifest struct {
	Profile         string              `json:"profile"`
	GlobalAliases   map[string][]string `json:"global_aliases"`
	Sections        []Section           `json:"sections"`
	TechFamilies    []stackdet.Family   `json:"tech_families,omitempty"`
	TechnicalMatrix string              `json:"technical_matrix,omitempty"`
}

// Index — runtime retrieval index.
// TokenizerVersion — версия семантики токенизатора (см. TokenizerVersion),
// которой посчитаны все токены индекса. Пустое значение означает индекс,
// собранный до появления версии, и трактуется как stale (см. CheckIndexFresh).
type Index struct {
	Version          int        `json:"version"`
	TokenizerVersion string     `json:"tokenizer_version,omitempty"`
	ManifestSHA256   string     `json:"manifest_sha256"`
	Files            []FileMeta `json:"files"`
	Facts            []Fact     `json:"facts"`
}

// RetrievalResult — результат retrieval одного факта.
// Content — точечно прочитанное содержимое факта (после ranking).
type RetrievalResult struct {
	FactID    string  `json:"fact_id"`
	SectionID string  `json:"section_id"`
	Score     float64 `json:"score"`
	Content   string  `json:"content"`
}

// CandidateContext — prompt-ready candidate context (profile + technical matrix
// + selected facts). Matrix — отдельное поле manifest (M4): не часть profile,
// чтобы бюджет профиля её не обрезал.
type CandidateContext struct {
	Profile string
	Matrix  string
	Facts   []RetrievalResult
}

// Render формирует prompt-ready строку: профиль, техническая матрица, затем
// отобранные факты.
func (c CandidateContext) Render() string {
	var sb strings.Builder
	if c.Profile != "" {
		sb.WriteString("Candidate profile:\n")
		sb.WriteString(c.Profile)
	}
	if c.Matrix != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString("Technical matrix:\n")
		sb.WriteString(c.Matrix)
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
