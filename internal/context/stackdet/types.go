// Package stackdet — детерминированное определение стека технологий в вопросе
// интервью. Чистые функции без внешних зависимостей и без импорта
// internal/context: вызывающая сторона передаёт уже нормализованные и
// канонизированные токены вопроса, сессионный стек и описание семейств
// технологий (manifest.tech_families).
//
// Задача пакета — развести три разных смысла «стек упомянут»:
//
//   - Penalty: session ∪ однозначные упоминания в вопросе. Всё, что вне этого
//     множества, — чужой стек и штрафуется.
//   - Named: однозначно названные в вопросе стеки. Только они получают бонус.
//   - Exempt: стеки, упомянутые лишь неоднозначно (например, spring у java).
//     Освобождены от штрафа, но без бонуса.
package stackdet

// Family — семейство технологий (язык/платформа) с членами и списком
// неоднозначных членов. Name — канонический идентификатор стека ("php",
// "java", "golang"), тот же, что в Fact.Stack. Display — человекочитаемая
// метка для prompt ("PHP", "Java", "Go").
type Family struct {
	Name      string   `json:"name"`      // каноническое имя: "php", "java", "golang"
	Display   string   `json:"display"`   // "PHP", "Java", "Go" — для меток в prompt
	Members   []string `json:"members"`   // laravel, symfony, composer / spring, hibernate, jvm / golang
	Ambiguous []string `json:"ambiguous"` // члены семьи, которые НЕЛЬЗЯ считать однозначным упоминанием
}

// Sets — результат Detect: три непересекающихся по смыслу множества стеков.
// Все map инициализированы (не nil), даже если пусты.
type Sets struct {
	Penalty map[string]struct{} // session ∪ однозначные упоминания в вопросе → чужой стек штрафуется
	Named   map[string]struct{} // однозначно названные в вопросе стеки → бонус своим
	Exempt  map[string]struct{} // стеки, упомянутые только неоднозначно → освобождение от штрафа без бонуса
}

// Params — коэффициенты множителя. Нулевые поля трактуются как дефолтные
// (см. effective), чтобы caller, передавший Params{}, не получил обнуление
// всего скоринга.
type Params struct {
	Penalty float64 // коэффициент штрафа, дефолт 0.3
	Bonus   float64 // бонус явно названному стеку, дефолт 1.5
	Session float64 // бонус совпадению с сессией, дефолт 1.25
}

// DefaultParams возвращает дефолтные коэффициенты множителя.
func DefaultParams() Params {
	return Params{
		Penalty: 0.3,
		Bonus:   1.5,
		Session: 1.25,
	}
}

// effective подставляет дефолты для нулевых коэффициентов.
func (p Params) effective() Params {
	d := DefaultParams()
	if p.Penalty == 0 {
		p.Penalty = d.Penalty
	}
	if p.Bonus == 0 {
		p.Bonus = d.Bonus
	}
	if p.Session == 0 {
		p.Session = d.Session
	}
	return p
}

// newSets возвращает Sets с инициализированными map.
func newSets() Sets {
	return Sets{
		Penalty: make(map[string]struct{}),
		Named:   make(map[string]struct{}),
		Exempt:  make(map[string]struct{}),
	}
}
