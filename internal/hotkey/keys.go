// Package hotkey предоставляет глобальные функциональные клавиши (F1–F4, Esc)
// для управления генерацией ответов поверх прозрачного overlay.
package hotkey

// Key — идентификаторы функциональных клавиш.
type Key int

const (
	KeyF1 Key = iota
	KeyF2
	KeyF3
	KeyF4
	KeyEsc

	// KeyShiftF2 / KeyShiftF3 — дублируют навигацию F2/F3 (Shift+F2 = Prev,
	// Shift+F3 = Next): удобно переключать активную метку с модификатором.
	// Отдельные Key (а не переиспользование F2/F3) — потому что handleHotkey и
	// WM_HOTKEY мапят id→Key, а Shift-комбинации несут modShift и отличаются от
	// чистых F2/F3.
	KeyShiftF2
	KeyShiftF3
)
