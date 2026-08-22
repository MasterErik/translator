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
)
