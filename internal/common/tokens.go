// Package common provides shared types, events, and configuration
// used across all internal packages of the Translator application.
package common

// EstimateTokens returns an estimate of how many tokens s occupies.
//
// Heuristic v1: ~1 token per 4 bytes. This is the single function used to
// size text in tokens for both conversation history and candidate context.
//
// Replaceable implementation: when an accurate tokenizer for the current
// model becomes available, replace it here (single replacement point).
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + 3) / 4
}
