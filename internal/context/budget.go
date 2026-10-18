package candidatecontext

import (
	"strings"
	"unicode/utf8"

	"github.com/mastererik/translator/internal/common"
)

// Budgeter budgets prompt-ready candidate context by token estimates.
//
// It knows nothing about minScore, tokenizers, retrieval policy, or threshold
// semantics — it is only responsible for fitting the candidate context into a
// token budget. Profile, technical matrix and facts have separate budgets:
// matrix is a separate manifest field (not part of profile), so sharing the
// profile budget (150 tokens) would silently cut its tail.
type Budgeter struct {
	maxTokens        int // budget for facts
	maxProfileTokens int // budget for the profile
	maxMatrixTokens  int // budget for the technical matrix (M4)
}

// NewBudgeter creates a Budgeter with separate token budgets for facts
// (maxTokens), the profile (maxProfileTokens) and the technical matrix
// (maxMatrixTokens).
func NewBudgeter(maxTokens, maxProfileTokens, maxMatrixTokens int) *Budgeter {
	return &Budgeter{
		maxTokens:        maxTokens,
		maxProfileTokens: maxProfileTokens,
		maxMatrixTokens:  maxMatrixTokens,
	}
}

// Budget selects prompt-ready candidate context within the configured budgets
// without a technical matrix (см. BudgetWithMatrix).
func (b *Budgeter) Budget(profile string, results []RetrievalResult) CandidateContext {
	return b.BudgetWithMatrix(profile, "", results)
}

// BudgetWithMatrix selects prompt-ready candidate context within the configured
// budgets, including the technical matrix (M4).
//
// Facts are considered in the order they appear in results (already sorted by
// score — never re-sorted). A fact is included whole when its content fits the
// remaining fact budget; otherwise it is skipped. If results are non-empty but
// no fact fits whole, the top-1 result's content is truncated (UTF-8 safe) to
// the fact budget as a fallback. Empty results yield empty facts; the Budgeter
// never fabricates a result from absent retrieval results.
//
// Matrix (like profile) is always rendered; it is truncated to its own budget
// on a UTF-8-safe boundary. О факте обрезки сообщает вызывающий (BuildContextFn
// пишет WARN один раз при старте): Budgeter намеренно не знает про лог.
func (b *Budgeter) BudgetWithMatrix(profile, matrix string, results []RetrievalResult) CandidateContext {
	out := CandidateContext{
		Profile: b.budgetProfile(profile),
		Matrix:  truncateToTokens(strings.TrimSpace(matrix), b.maxMatrixTokens),
		Facts:   []RetrievalResult{},
	}

	if len(results) == 0 {
		return out
	}

	remaining := b.maxTokens
	for _, r := range results {
		tk := common.EstimateTokens(r.Content)
		if tk > remaining {
			continue
		}
		out.Facts = append(out.Facts, r)
		remaining -= tk
	}

	if len(out.Facts) == 0 {
		top := results[0]
		top.Content = truncateToTokens(top.Content, b.maxTokens)
		out.Facts = append(out.Facts, top)
	}

	return out
}

// budgetProfile trims the profile to maxProfileTokens if it exceeds the budget.
func (b *Budgeter) budgetProfile(profile string) string {
	return truncateToTokens(profile, b.maxProfileTokens)
}

// truncateToTokens truncates s to at most maxTokens (via the shared token
// estimate) without splitting a UTF-8 rune. When possible it rolls back to the
// last space boundary so a word is not cut in half.
func truncateToTokens(s string, maxTokens int) string {
	if maxTokens <= 0 {
		return ""
	}
	maxBytes := maxTokens * 4 // inverse of EstimateTokens
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	if sp := strings.LastIndexByte(s[:cut], ' '); sp >= 0 {
		cut = sp
	}
	return s[:cut]
}
