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

	// KeyShiftF2 / KeyShiftF3 — уточнения фазы 2 (план): Shift+F2 прикрепляет
	// последнюю финальную фразу интервьюера уточнением к активной метке,
	// Shift+F3 снимает последнее уточнение. Отдельные Key (а не переиспользование
	// F2/F3) — потому что handleHotkey и WM_HOTKEY мапят id→Key, а Shift+F2 не
	// должен попадать в общий F1–F4/Esc-диапазон навигации.
	KeyShiftF2
	KeyShiftF3
)
