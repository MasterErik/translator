//go:build !windows

package hotkey

import "context"

// Run — no-op на платформах без глобальных hotkeys (не-Windows).
// Возвращается при отмене ctx.
func Run(ctx context.Context, onKey func(Key)) error {
	<-ctx.Done()
	return ctx.Err()
}
