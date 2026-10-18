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
// Score равен Rank (score, по которому факт отсортирован). Raw сохраняет
// прежнюю семантику score без стек-множителя (M7b): с ним сравниваются
// min_score и абсолютный порог подсказок, чтобы бонус не проводил чужой факт
// Breakdown заполняется только при RetrieveOptions.Explain.
//
// Stack, Period и Weight — метки факта из Fact (M9.1). StackDisplay — уже
// разрешённые для prompt человекочитаемые имена стеков (Family.Display, с
// fallback на каноническое имя): Retriever заполняет их при материализации,
// потому что Render не имеет доступа к manifest.TechFamilies. Period хранится
// в исходном формате (2018-2024) и форматируется в Render.
type RetrievalResult struct {
	FactID       string          `json:"fact_id"`
	SectionID    string          `json:"section_id"`
	Score        float64         `json:"score"`
	Content      string          `json:"content"`
	Raw          float64         `json:"raw"`
	Rank         float64         `json:"rank"`
	OutOfTarget  bool            `json:"out_of_target,omitempty"`
	Stack        []string        `json:"stack,omitempty"`
	StackDisplay []string        `json:"stack_display,omitempty"`
	Period       string          `json:"period,omitempty"`
	Weight       string          `json:"weight,omitempty"`
	Breakdown    *ScoreBreakdown `json:"breakdown,omitempty"`
}

// ScoreBreakdown объясняет, из чего сложился результат факта: вклады полей,
// section/phrase бонусы, вердикты гейта и relative threshold, стек-множитель.
type ScoreBreakdown struct {
	Keywords   float64 `json:"keywords"`
	Title      float64 `json:"title"`
	Aliases    float64 `json:"aliases"`
	Content    float64 `json:"content"`
	Section    float64 `json:"section"`
	Phrase     float64 `json:"phrase"`
	FieldScore float64 `json:"field_score"`
	Core       float64 `json:"core"`
	Raw        float64 `json:"raw"`
	Rank       float64 `json:"rank"`
	// ContentRatio — matched_content_IDF / question_IDF: насколько вопрос
	// покрыт телом факта (используется content-only гейтом).
	ContentRatio float64 `json:"content_ratio"`
	Mult         float64 `json:"mult"`
	MultReason   string  `json:"mult_reason,omitempty"`
	Gate         string  `json:"gate,omitempty"`
	Threshold    string  `json:"threshold,omitempty"`
}

// ExplainItem — диагностика одного факта-кандидата: вклады полей, вердикты
// гейта и relative threshold, стек-множитель. Passed == false означает, что
// факт не попал бы в выдачу; RejectedBy называет причину.
type ExplainItem struct {
	FactID     string         `json:"fact_id"`
	SectionID  string         `json:"section_id"`
	Passed     bool           `json:"passed"`
	RejectedBy string         `json:"rejected_by,omitempty"`
	Breakdown  ScoreBreakdown `json:"breakdown"`
}

// CandidateContext — prompt-ready candidate context (profile + technical matrix
// + selected facts). Matrix — отдельное поле manifest (M4): не часть profile,
// чтобы бюджет профиля её не обрезал.
type CandidateContext struct {
	Profile string
	Matrix  string
	Facts   []RetrievalResult
}

// EmptyRetrievalNotice — строка, которая уходит в Render, когда retrieval не
// нашёл ни одного факта (M9.3): модель не должна домысливать проекты.
const EmptyRetrievalNotice = "No relevant facts found — rely on the candidate profile and Technical Matrix; do not invent projects."

// OutOfTargetNotice — пометка факта, чей стек целиком вне целевого стека
// вопроса/сессии (M9.4): использовать только если вопрос прямо о нём.
const OutOfTargetNotice = "OUT OF TARGET STACK — use only if the question is directly about it"

// Render формирует prompt-ready строку: профиль, техническая матрица, затем
// отобранные факты. Перед телом каждого факта печатается метка
// «[Java · 2018–2024 · primary]» (M9.1) и, для фактов вне целевого стека,
// пометка OUT OF TARGET STACK (M9.4). Если релевантных фактов нет, вместо
// фактов печатается EmptyRetrievalNotice (M9.3).
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
	rendered := 0
	for _, f := range c.Facts {
		if f.Content == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		if label := f.Label(); label != "" {
			sb.WriteString(label)
			sb.WriteString("\n")
		}
		if f.OutOfTarget {
			sb.WriteString(OutOfTargetNotice)
			sb.WriteString("\n")
		}
		sb.WriteString(f.Content)
		rendered++
	}
	if rendered == 0 {
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString(EmptyRetrievalNotice)
	}
	return sb.String()
}

// Label возвращает метку факта для prompt: «[Java · 2018–2024 · primary]».
// Stack берётся из StackDisplay (fallback — Stack), период нормализуется
// (en dash вместо дефиса), отсутствующие поля опускаются. Пустая метка — "".
func (r RetrievalResult) Label() string {
	parts := make([]string, 0, 3)
	if stack := joinStack(r.StackDisplay, r.Stack); stack != "" {
		parts = append(parts, stack)
	}
	if period := FormatPeriod(r.Period); period != "" {
		parts = append(parts, period)
	}
	if weight := strings.TrimSpace(r.Weight); weight != "" {
		parts = append(parts, weight)
	}
	if len(parts) == 0 {
		return ""
	}
	return "[" + strings.Join(parts, " · ") + "]"
}

// joinStack склеивает готовые display-имена стеков через запятую; пустые
// элементы опускаются. Если display пуст — используется канонический стек.
func joinStack(display, canonical []string) string {
	names := make([]string, 0, len(display))
	for _, s := range display {
		if s = strings.TrimSpace(s); s != "" {
			names = append(names, s)
		}
	}
	if len(names) == 0 {
		for _, s := range canonical {
			if s = strings.TrimSpace(s); s != "" {
				names = append(names, s)
			}
		}
	}
	return strings.Join(names, ", ")
}

// FormatPeriod нормализует период к виду YYYY–YYYY / YYYY–present / YYYY:
// разделитель-дефис заменяется на en dash. Нераспознанный период
// возвращается как есть, пустой — "".
func FormatPeriod(period string) string {
	p := strings.TrimSpace(period)
	switch {
	case p == "":
		return ""
	case strings.HasSuffix(p, "-present"):
		return strings.TrimSuffix(p, "-present") + "\u2013present"
	case len(p) == 9 && p[4] == '-':
		return p[:4] + "\u2013" + p[5:]
	default:
		return p
	}
}
