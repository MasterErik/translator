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
// token budget. The profile and the facts have separate budgets.
type Budgeter struct {
	maxTokens        int // budget for facts
	maxProfileTokens int // budget for the profile
}

// NewBudgeter creates a Budgeter with separate token budgets for facts
// (maxTokens) and the profile (maxProfileTokens).
func NewBudgeter(maxTokens, maxProfileTokens int) *Budgeter {
	return &Budgeter{
		maxTokens:        maxTokens,
		maxProfileTokens: maxProfileTokens,
	}
}

// Budget selects prompt-ready candidate context within the configured budgets.
//
// Facts are considered in the order they appear in results (already sorted by
// score — never re-sorted). A fact is included whole when its content fits the
// remaining fact budget; otherwise it is skipped. If results are non-empty but
// no fact fits whole, the top-1 result's content is truncated (UTF-8 safe) to
// the fact budget as a fallback. Empty results yield empty facts; the Budgeter
// never fabricates a result from absent retrieval results.
func (b *Budgeter) Budget(profile string, results []RetrievalResult) CandidateContext {
	out := CandidateContext{
		Profile: b.budgetProfile(profile),
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
