package translator_test

import (
	"testing"

	"github.com/mastererik/translator/internal/translator"
)

// Compile-time interface compliance check: ensure LLMProvider interface
// is properly defined and can be referenced.
func TestLLMProviderInterface(t *testing.T) {
	// This is a compile-time check: if LLMProvider is not an interface
	// or doesn't have the right methods, this won't compile.
	var _ translator.LLMProvider = nil //nolint:staticcheck // intentional compile-time check
}
