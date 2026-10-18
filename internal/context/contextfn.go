package candidatecontext

import (
	"fmt"
	"os"
	"strings"
)

// SessionContext — контекст интервью, влияющий на retrieval (M7: целевой стек
// интервью). Поле передаётся в retrieval-функцию уже собранным; BuildContextFn
// сам его не вычисляет.
type SessionContext struct {
	TargetStack []string
}

// ContextResult — результат сборки контекста для одного вопроса: строка,
// которая уходит в prompt (Text), и ID фактов, реально попавших в неё.
// FactIDs/HintIDs нужны eval-контуру (LeakRate считается по итоговой выдаче).
//
// HintsDisabled — каталог hints задан и сконфигурирован, но недоступен:
// подсказки отключены на всю сессию (сигнал для индикатора M10).
// Degraded — контекст отсутствует по явной настройке on_load_error=degraded
// (D15): сессия продолжается, но фактов кандидата в prompt нет.
type ContextResult struct {
	Text          string
	FactIDs       []string
	HintIDs       []string
	HintsDisabled bool
	Degraded      bool
}

// Допустимые значения ContextFnConfig.OnLoadError (D15).
const (
	// OnLoadErrorFail — ошибка загрузки candidate context валит старт (default).
	OnLoadErrorFail = "fail"
	// OnLoadErrorDegraded — старт продолжается без фактов кандидата, в prompt
	// уходит явная пометка, лог получает ERROR.
	OnLoadErrorDegraded = "degraded"
)

// Дефолты бюджетов fact-level контекста (совпадают с config.yaml). Нулевое
// значение поля конфига означает «не задано»: Budgeter с maxTokens=0 не
// вмещает ни одного факта и молча отдаёт усечённый top-1 — тихая потеря
// контекста, поэтому BuildContextFn подставляет дефолт с WARN.
const (
	DefaultMaxTokens        = 2000
	DefaultMaxProfileTokens = 150
	// DefaultMaxMatrixTokens — бюджет technical_matrix (M4). Матрица рендерится
	// всегда, но своим бюджетом: бюджет профиля (150) обрезал бы её хвост.
	DefaultMaxMatrixTokens = 100
)

// ContextFnConfig — конфигурация единой сборки контекста. Один и тот же вход
// используют прод-пайплайн, eval и probe (M0: «eval = прод»).
type ContextFnConfig struct {
	// Candidate context — fact-level retrieval.
	Dir              string
	TopK             int
	MinScore         float64
	MaxTokens        int
	MaxProfileTokens int
	MaxMatrixTokens  int

	// Legacy-режим (D14).
	LegacyFile          string // путь к файлу базы CV (передаётся целиком)
	AllowLegacyFallback bool   // разрешать fallback на LegacyFile при сбое Dir

	// Hints — готовые ответы (необязательны, сбой нефатален).
	HintsDir      string
	HintsTopK     int
	HintsMinScore float64

	// OnLoadError — OnLoadErrorFail (default) | OnLoadErrorDegraded.
	OnLoadError string

	// Logf — необязательный лог (WARN/ERROR в sessLog пайплайна). nil-safe.
	Logf func(string)
}

// degradedNotice — строка, которая уходит в prompt вместо фактов кандидата в
// degraded-режиме (D15). Явная пометка обязательна: модель не должна
// домысливать факты кандидата, а оператор видит, что контекст не подключён.
func degradedNotice(cause error) string {
	return "### Candidate context unavailable (degraded mode)\n" +
		"Candidate facts could not be loaded: " + cause.Error() + "\n" +
		"Answer from general knowledge only; do not invent facts about the candidate."
}

// BuildContextFn собирает цепочку retrieval → budget → render → hints и
// возвращает функцию «вопрос → ContextResult». Одна реализация для прода и eval.
//
// Семантика ошибок (соответствует D14/D15):
//   - сбой загрузки каталога Dir без AllowLegacyFallback — ошибка (старт падает);
//   - AllowLegacyFallback=true: читается LegacyFile; нечитаемый файл — тоже
//     ошибка (никаких «пустых строк с nil-ошибкой»);
//   - OnLoadError=degraded — вместо ошибки возвращается функция, отдающая
//     контекст без фактов с явной пометкой (Degraded=true) и ERROR в лог;
//   - сбой hints нефатален: WARN/ERROR в лог, подсказки выключены на сессию
//     (ContextResult.HintsDisabled=true), факты кандидата продолжают работать.
func BuildContextFn(cfg ContextFnConfig) (func(question string, s SessionContext) ContextResult, error) {
	logf := cfg.Logf
	if logf == nil {
		logf = func(string) {}
	}

	onLoadError := strings.ToLower(strings.TrimSpace(cfg.OnLoadError))
	if onLoadError == "" {
		onLoadError = OnLoadErrorFail
	}
	if onLoadError != OnLoadErrorFail && onLoadError != OnLoadErrorDegraded {
		return nil, fmt.Errorf("candidatecontext: on_load_error: %q (ожидается %q или %q)",
			cfg.OnLoadError, OnLoadErrorFail, OnLoadErrorDegraded)
	}

	// failOrDegraded решает судьбу сбоя загрузки контекста по OnLoadError.
	failOrDegraded := func(cause error) (func(string, SessionContext) ContextResult, error) {
		if onLoadError != OnLoadErrorDegraded {
			return nil, cause
		}
		logf(fmt.Sprintf("ERROR: candidate context недоступен (%v) — режим on_load_error=degraded, сессия без фактов кандидата", cause))
		text := degradedNotice(cause)
		return func(string, SessionContext) ContextResult {
			return ContextResult{Text: text, Degraded: true}
		}, nil
	}

	// Hints грузятся до основного режима: их сбой нефатален (M0.4c).
	hintsDisabled := false
	hints := (*hintSource)(nil)
	if cfg.HintsDir != "" {
		h, err := newHintSource(cfg.HintsDir, cfg.HintsTopK, cfg.HintsMinScore)
		if err != nil {
			logf(fmt.Sprintf("ERROR: hints недоступны (%v) — подсказки отключены", err))
			hintsDisabled = true
		} else {
			hints = h
		}
	}

	factIDsOf := func(cc CandidateContext) []string {
		if len(cc.Facts) == 0 {
			return nil
		}
		ids := make([]string, 0, len(cc.Facts))
		for _, f := range cc.Facts {
			ids = append(ids, f.FactID)
		}
		return ids
	}

	// Legacy-режим: каталог Dir не задан.
	if cfg.Dir == "" {
		if cfg.LegacyFile == "" {
			// Источник не сконфигурирован вовсе: пустой контекст.
			return func(string, SessionContext) ContextResult {
				return ContextResult{HintsDisabled: hintsDisabled}
			}, nil
		}
		text, err := readLegacyFile(cfg.LegacyFile)
		if err != nil {
			return failOrDegraded(fmt.Errorf("candidatecontext: legacy candidate context: %w", err))
		}
		if hints != nil {
			// Hints требуют retrieval по вопросу — в legacy-режиме они выключены.
			logf("WARN: hints задан, но candidate context работает в legacy-режиме — hints отключены")
			hints = nil
			hintsDisabled = true
		}
		disabled := hintsDisabled
		return func(string, SessionContext) ContextResult {
			return ContextResult{Text: text, HintsDisabled: disabled}
		}, nil
	}

	// Fact-level режим.
	//
	// Нулевые бюджеты — не «без лимита», а тихая потеря контекста: Budgeter
	// с maxTokens=0 не вмещает ни одного факта и отдаёт усечённый top-1.
	// Поэтому 0 → дефолт с WARN, отрицательное значение → ошибка старта.
	if cfg.MaxTokens < 0 || cfg.MaxProfileTokens < 0 || cfg.MaxMatrixTokens < 0 {
		return nil, fmt.Errorf("candidatecontext: бюджеты контекста не могут быть отрицательными (max_tokens=%d, max_profile_tokens=%d, max_matrix_tokens=%d)",
			cfg.MaxTokens, cfg.MaxProfileTokens, cfg.MaxMatrixTokens)
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = DefaultMaxTokens
		logf(fmt.Sprintf("WARN: candidate_context.max_tokens не задан — используется %d (0 отдал бы в prompt только усечённый top-1)", DefaultMaxTokens))
	}
	if cfg.MaxProfileTokens == 0 {
		cfg.MaxProfileTokens = DefaultMaxProfileTokens
		logf(fmt.Sprintf("WARN: candidate_context.max_profile_tokens не задан — используется %d", DefaultMaxProfileTokens))
	}
	if cfg.MaxMatrixTokens == 0 {
		cfg.MaxMatrixTokens = DefaultMaxMatrixTokens
		logf(fmt.Sprintf("WARN: candidate_context.max_matrix_tokens не задан — используется %d", DefaultMaxMatrixTokens))
	}

	retriever, budgeter, manifest, err := newContextRetriever(cfg)
	if err != nil {
		cause := fmt.Errorf("candidatecontext: fact-level candidate context: %w", err)

		if cfg.AllowLegacyFallback && cfg.LegacyFile != "" {
			text, readErr := readLegacyFile(cfg.LegacyFile)
			if readErr == nil {
				logf(fmt.Sprintf("WARN: fact-level candidate context недоступен (%v), fallback на legacy файл", cause))
				if hints != nil {
					hints = nil
					hintsDisabled = true
				}
				disabled := hintsDisabled
				return func(string, SessionContext) ContextResult {
					return ContextResult{Text: text, HintsDisabled: disabled}
				}, nil
			}
			// M0.4b: fallback разрешён, но файл нечитаем — молчаливой пустой
			// строки быть не должно.
			return failOrDegraded(fmt.Errorf("%w; legacy fallback %s: %v", cause, cfg.LegacyFile, readErr))
		}

		return failOrDegraded(cause)
	}

	if matrix := strings.TrimSpace(manifest.TechnicalMatrix); matrix != "" {
		if truncateToTokens(matrix, cfg.MaxMatrixTokens) != matrix {
			logf(fmt.Sprintf("WARN: technical_matrix манифеста (%d символов) превышает max_matrix_tokens=%d — в prompt уйдёт обрезанная матрица",
				len(matrix), cfg.MaxMatrixTokens))
		}
	}

	return func(question string, s SessionContext) ContextResult {
		// s (SessionContext) участвует в ранжировании начиная с M7b
		// (стек-множители); на этапе M0 контракт уже зафиксирован.
		_ = s
		budgeted := budgeter.BudgetWithMatrix(manifest.Profile, manifest.TechnicalMatrix, retriever.Retrieve(question))
		out := ContextResult{
			Text:          budgeted.Render(),
			FactIDs:       factIDsOf(budgeted),
			HintsDisabled: hintsDisabled,
		}
		if hints != nil {
			results := hints.Retrieve(question)
			hintText := joinHintBodies(results)
			if hintText != "" {
				out.Text = AppendHint(out.Text, hintText)
				out.HintIDs = resultIDs(results)
			}
		}
		return out
	}, nil
}

// AppendHint добавляет готовый ответ-подсказку к базовому контексту. Пустая
// подсказка — base возвращается без изменений; иначе подсказка добавляется
// отдельным блоком с явным указанием «использовать как есть».
func AppendHint(base, hint string) string {
	if hint == "" {
		return base
	}
	const header = "Suggested answer (use as is):\n"
	if base == "" {
		return header + hint
	}
	return base + "\n\n" + header + hint
}

// hintSource — загруженный каталог hints с готовым retriever'ом.
type hintSource struct {
	retriever *Retriever
}

// newHintSource загружает каталог hints и строит retriever. Любая ошибка
// возвращается вызывающему — решение о нефатальности принимает BuildContextFn.
func newHintSource(dir string, topK int, minScore float64) (*hintSource, error) {
	manifest, index, err := LoadCandidateContext(dir)
	if err != nil {
		return nil, fmt.Errorf("загрузка hints %s: %w", dir, err)
	}
	retriever, err := NewRetriever(manifest, index, dir, topK, minScore)
	if err != nil {
		return nil, fmt.Errorf("инициализация retriever hints %s: %w", dir, err)
	}
	return &hintSource{retriever: retriever}, nil
}

// Retrieve возвращает готовые ответы hints по вопросу.
func (h *hintSource) Retrieve(question string) []RetrievalResult {
	return h.retriever.Retrieve(question)
}

// joinHintBodies склеивает тела отобранных подсказок (тела фактов, без профиля —
// профиль hints-manifest в подсказке не участвует).
func joinHintBodies(results []RetrievalResult) string {
	if len(results) == 0 {
		return ""
	}
	bodies := make([]string, 0, len(results))
	for _, r := range results {
		bodies = append(bodies, r.Content)
	}
	return strings.Join(bodies, "\n")
}

// resultIDs возвращает FactID результатов в порядке выдачи.
func resultIDs(results []RetrievalResult) []string {
	if len(results) == 0 {
		return nil
	}
	ids := make([]string, 0, len(results))
	for _, r := range results {
		ids = append(ids, r.FactID)
	}
	return ids
}

// newContextRetriever загружает каталог candidate context и собирает
// retriever + budgeter.
func newContextRetriever(cfg ContextFnConfig) (*Retriever, *Budgeter, *Manifest, error) {
	manifest, index, err := LoadCandidateContext(cfg.Dir)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("загрузка candidate context: %w", err)
	}
	retriever, err := NewRetriever(manifest, index, cfg.Dir, cfg.TopK, cfg.MinScore)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("инициализация retriever candidate context: %w", err)
	}
	budgeter := NewBudgeter(cfg.MaxTokens, cfg.MaxProfileTokens, cfg.MaxMatrixTokens)
	return retriever, budgeter, manifest, nil
}

// readLegacyFile читает legacy-файл базы CV целиком.
func readLegacyFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("чтение %s: %w", path, err)
	}
	return string(data), nil
}
