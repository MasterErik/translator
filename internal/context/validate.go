package candidatecontext

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/mastererik/translator/internal/context/stackdet"
)

// Веса факта (enum Fact.Weight): насколько факт важен для интервью-контекста.
const (
	// WeightPrimary — ключевой факт (опыт, summary, текущий проект).
	WeightPrimary = "primary"
	// WeightSecondary — вспомогательный факт (skills, sql-подход, публикации).
	WeightSecondary = "secondary"
	// WeightBackground — фон (образование, публикации как справка).
	WeightBackground = "background"
)

var validWeights = map[string]struct{}{
	WeightPrimary:    {},
	WeightSecondary:  {},
	WeightBackground: {},
}

// periodRe — обязательный формат Fact.Period: год, диапазон лет или "present".
var periodRe = regexp.MustCompile(`^\d{4}(-\d{4}|-present)?$`)

// LintWarning — предупреждение сборки индекса: данные формально валидны, но,
// вероятно, размечены неполно. Сборку не прерывает; подавляется явным
// lint_ignore в маркере факта.
type LintWarning struct {
	File    string
	FactID  string
	Message string
}

// String — человекочитаемое представление для логов и консольного вывода.
func (w LintWarning) String() string {
	return fmt.Sprintf("%s: факт %s: %s", w.File, w.FactID, w.Message)
}

// familyIndex резолвит термины стека в канонические имена семей:
// имя семьи или её член (members/ambiguous) → Family.Name.
type familyIndex struct {
	names    map[string]struct{}
	byMember map[string]string
}

// newFamilyIndex строит резолвер и проверяет саму таблицу семей: пустые name,
// дубликаты семей и токен, заявленный в двух семьях, — ошибка данных, а не
// повод молча выбрать одну из семей.
func newFamilyIndex(families []stackdet.Family) (*familyIndex, error) {
	fi := &familyIndex{
		names:    make(map[string]struct{}, len(families)),
		byMember: make(map[string]string),
	}
	for _, f := range families {
		name := strings.TrimSpace(f.Name)
		if name == "" {
			return nil, fmt.Errorf("tech_families: семья с пустым name")
		}
		if _, dup := fi.names[name]; dup {
			return nil, fmt.Errorf("tech_families: дубликат семьи %q", name)
		}
		fi.names[name] = struct{}{}

		members := make([]string, 0, len(f.Members)+len(f.Ambiguous))
		members = append(members, f.Members...)
		members = append(members, f.Ambiguous...)
		for _, m := range members {
			m = strings.TrimSpace(m)
			if m == "" {
				return nil, fmt.Errorf("tech_families: пустой член семьи %q", name)
			}
			if prev, ok := fi.byMember[m]; ok && prev != name {
				return nil, fmt.Errorf("tech_families: токен %q заявлен в семьях %q и %q", m, prev, name)
			}
			fi.byMember[m] = name
		}
	}
	return fi, nil
}

// resolve возвращает каноническое имя семьи для термина стека.
func (fi *familyIndex) resolve(term string) (string, bool) {
	if fi == nil {
		return "", false
	}
	if _, ok := fi.names[term]; ok {
		return term, true
	}
	name, ok := fi.byMember[term]
	return name, ok
}

// stackLangPlatforms — whitelist D4: в Fact.Stack попадают только языки и
// платформы. БД и инфраструктурные инструменты (mssql, postgresql, oracle,
// kafka, redis, kubernetes, docker и т.п.) живут в keywords и в stack не
// входят, поэтому упоминание такого токена в тексте факта — не повод для lint.
var stackLangPlatforms = map[string]struct{}{
	"java":       {},
	"golang":     {},
	"php":        {},
	"python":     {},
	"typescript": {},
	"vue":        {},
	"delphi":     {},
}

// frameworkTokens — члены семей-языков, которые сами по себе не язык и не
// платформа (ORM/миграции/framework'и): по D4 в stack их нет, поэтому в тексте
// факта они не опознаются как заявление о стеке. Упоминание такого слова не
// отменяет предупреждение о языке, если язык в факте тоже назван.
var frameworkTokens = map[string]struct{}{
	"hibernate": {},
	"mybatis":   {},
	"liquibase": {},
	"yii2":      {},
	"laravel":   {},
	"symfony":   {},
	"gunicorn":  {},
	"winapi":    {},
}

// isStackLangPlatform сообщает, является ли каноническое имя семьи языком или
// платформой из whitelist D4.
func isStackLangPlatform(family string) bool {
	_, ok := stackLangPlatforms[family]
	return ok
}

// isFrameworkToken сообщает, что токен — член семьи-языка, но сам по себе не
// язык/платформа (hibernate, liquibase, laravel, ...).
func isFrameworkToken(tok string) bool {
	_, ok := frameworkTokens[tok]
	return ok
}

// lintStacklessToken возвращает токены семейства-языка/платформы (whitelist D4),
// встретившиеся в тексте факта без указанного stack: такое слово читается как
// заявление о стеке, и без разметки retrieval не сможет ни дать бонус, ни
// оштрафовать факт. Остальные семьи и члены (БД, инфраструктура, ORM/framework)
// в stack по D4 не относятся, поэтому их токены предупреждения не вызывают.
func (fi *familyIndex) lintStacklessToken(tokens []string) string {
	if fi == nil || len(fi.byMember) == 0 {
		return ""
	}
	found := make([]string, 0, 4)
	seen := make(map[string]struct{}, 4)
	for _, tok := range tokens {
		name, ok := fi.byMember[tok]
		if !ok || !isStackLangPlatform(name) || isFrameworkToken(tok) {
			continue
		}
		if _, dup := seen[tok]; dup {
			continue
		}
		seen[tok] = struct{}{}
		found = append(found, tok)
	}
	if len(found) == 0 {
		return ""
	}
	sort.Strings(found)
	return strings.Join(found, ", ")
}

// validateFact проверяет поля факта и нормализует Stack к каноническим именам
// семей (member → Family.Name). Возвращает lint-предупреждение, если у факта
// пустой stack, но в тексте есть токены семей-языков/платформ (whitelist D4),
// и lint_ignore не выставлен. Токены БД/инструментов предупреждения не дают:
// по D4 они в stack не входят.
func (fi *familyIndex) validateFact(f Fact, lintIgnore string, titleTokens []string) ([]string, *LintWarning, error) {
	if f.Weight != "" {
		if _, ok := validWeights[f.Weight]; !ok {
			return nil, nil, fmt.Errorf("факт %s: weight %q не входит в enum (%s|%s|%s)",
				f.ID, f.Weight, WeightPrimary, WeightSecondary, WeightBackground)
		}
	}
	if f.Period != "" && !periodRe.MatchString(f.Period) {
		return nil, nil, fmt.Errorf("факт %s: period %q не соответствует ^\\d{4}(-\\d{4}|-present)?$", f.ID, f.Period)
	}

	stack := make([]string, 0, len(f.Stack))
	seen := make(map[string]struct{}, len(f.Stack))
	for _, term := range f.Stack {
		name, ok := fi.resolve(term)
		if !ok {
			return nil, nil, fmt.Errorf("факт %s: стек %q не описан ни в одной семье tech_families", f.ID, term)
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		stack = append(stack, name)
	}

	if len(stack) == 0 && strings.TrimSpace(lintIgnore) == "" {
		tokens := make([]string, 0, len(titleTokens)+len(f.ContentTokens))
		tokens = append(tokens, titleTokens...)
		tokens = append(tokens, f.ContentTokens...)
		if hit := fi.lintStacklessToken(tokens); hit != "" {
			return stack, &LintWarning{
				FactID: f.ID,
				Message: fmt.Sprintf("токен языка/платформы (%s) в тексте факта при пустом stack — "+
					"разметить stack или подавить через lint_ignore", hit),
			}, nil
		}
	}
	return stack, nil, nil
}
