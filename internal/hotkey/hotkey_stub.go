//go:build !windows

package hotkey

import "context"

// Run — no-op на платформах без глобальных hotkeys (не-Windows).
// Возвращается при отмене ctx.
//
// Контракт возврата: nil при штатной отмене ctx — единый с windows-версией,
// чтобы pipeline не трактовал завершение по ctx как ошибку (B-9).
func Run(ctx context.Context, onKey func(Key)) error {
	<-ctx.Done()
	return nil
}
