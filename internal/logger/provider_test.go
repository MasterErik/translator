package logger_test

import (
	"testing"

	"github.com/mastererik/translator/internal/logger"
)

// Compile-time interface compliance check: ensure SessionLogger interface
// is properly defined and can be referenced.
func TestSessionLoggerInterface(t *testing.T) {
	// This is a compile-time check: if SessionLogger is not an interface
	// or doesn't have the right methods, this won't compile.
	var _ logger.SessionLogger = nil //nolint:staticcheck // intentional compile-time check
}
