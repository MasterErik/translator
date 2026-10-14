package dispatcher

// HotkeyAction — действие, приходящее от функциональных клавиш и не связанное
// напрямую с командой генерации. Разделение сделано потому, что Prev/Next не
// запускают генерацию — они только двигают активную метку таймлайна.
type HotkeyAction int

const (
	// ActionDemand (F1) — запросить ответ для активной метки.
	ActionDemand HotkeyAction = iota
	// ActionPrev (F2) — перейти к предыдущей метке таймлайна.
	ActionPrev
	// ActionNext (F3) — перейти к следующей метке таймлайна.
	ActionNext
	// ActionClarify (Shift+F2) — добавить последнюю финальную фразу интервьюера
	// (или слить хвостовой дубль-вопрос) уточнением к активной метке.
	ActionClarify
	// ActionUnclarify (Shift+F3) — снять последнее уточнение активной метки.
	ActionUnclarify
)

// String возвращает строчное представление действия (для логов).
func (a HotkeyAction) String() string {
	switch a {
	case ActionDemand:
		return "demand"
	case ActionPrev:
		return "prev"
	case ActionNext:
		return "next"
	case ActionClarify:
		return "clarify"
	case ActionUnclarify:
		return "unclarify"
	default:
		return "unknown"
	}
}
