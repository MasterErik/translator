package candidatecontext

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Tokenizer — детерминированный лексический токенизатор для fact-level retrieval.
// Один и тот же Tokenizer используется builder'ом (при построении index.json)
// и runtime retriever'ом, чтобы семантика токенов/канонизации совпадала.
//
// Tokenizer immutable после NewTokenizer: внутренние map не мутируются ни одним
// методом, поэтому он потокобезопасен без мьютексов.
type Tokenizer struct {
	// canonical: alias (вариант написания) → canonical term.
	canonical map[string]string
	// stopWords: lowercase стоп-слова (EN + RU).
	stopWords map[string]struct{}
}

// NewTokenizer разворачивает map "canonical term → варианты" во внутреннюю
// map "alias → canonical" и валидирует mapping. Возвращает ошибку при:
//  1. конфликте: один alias указан в списках двух разных canonical terms;
//  2. цикле: canonical term сам является вариантом другого canonical term;
//  3. невалидном mapping: пустой canonical term или пустой вариант.
//
// nil/пустой aliases — валиден (Tokenizer без канонизации).
func NewTokenizer(aliases map[string][]string) (*Tokenizer, error) {
	// Проход 1: базовые проверки + множество canonical terms.
	canonicalSet := make(map[string]struct{}, len(aliases))
	for term, variants := range aliases {
		if term == "" {
			return nil, fmt.Errorf("candidatecontext: пустой canonical term")
		}
		canonicalSet[term] = struct{}{}
		for _, v := range variants {
			if v == "" {
				return nil, fmt.Errorf("candidatecontext: пустой alias для canonical %q", term)
			}
		}
	}

	// Проход 2: обнаружение циклов (canonical является вариантом другого canonical).
	// Самоссылка (term в собственном списке вариантов) не считается циклом —
	// это no-op канонизация, допустима.
	for term, variants := range aliases {
		for _, v := range variants {
			if _, ok := canonicalSet[v]; ok && v != term {
				return nil, fmt.Errorf("candidatecontext: canonical %q является вариантом canonical %q", v, term)
			}
		}
	}

	// Проход 3: конфликты alias + построение map alias→canonical.
	canonical := make(map[string]string, len(aliases))
	for term, variants := range aliases {
		for _, v := range variants {
			if existing, ok := canonical[v]; ok && existing != term {
				return nil, fmt.Errorf("candidatecontext: alias %q задан для двух canonical: %q и %q", v, existing, term)
			}
			canonical[v] = term
		}
	}

	return &Tokenizer{canonical: canonical, stopWords: defaultStopWords()}, nil
}

// TokenizerVersion — версия ПОВЕДЕНИЯ токенизатора: Normalize, Substitute
// (фразовые подстановки), Tokenize (сплит по "/", "\\", дефисам, edge-trim),
// RemoveStopWords, Canonicalize. Хранится в index.json и входит в проверку
// freshness: индекс без версии или с другой версией считается stale и
// пересобирается (см. CheckIndexFresh).
//
// Любое изменение ожиданий golden-теста (tokenize_test.go) — включая правку
// словаря стоп-слов или таблиц подстановок — ТРЕБУЕТ bump этой константы,
// иначе старые индексы останутся «свежими» с токенами, посчитанными по
// старой семантике.
const TokenizerVersion = "3"

// substituteTargets — точные (не фразовые) подстановки спецтерминов.
// Ключ сравнивается с полем целиком после Normalize, поэтому "asp.net",
// "node.js" и "c++20" не затрагиваются: они не равны ни одному ключу.
// Значения — уже в виде, пригодном для Tokenize (латиница, без пунктуации).
var substituteTargets = map[string]string{
	"c++":  "cpp",
	"c#":   "csharp",
	".net": "dotnet",
}

// goPhraseNext — фразы после "go", при которых "go" означает язык Go.
// Ключ — следующее слово; значение — семантика фразы для ДВУХ предлогов
// (см. goContextPrepositions).
var goPhraseNext = map[string]struct{}{
	"language":      {},
	"developer":     {},
	"microservices": {},
}

// goContextPrepositions — предлоги, после которых "go" означает язык Go:
// "with go" / "in go" (реальная форма из логов: "what is your experience with
// go?"). Для bare-фразы действует тот же deny-list, что и для go-контекста.
var goContextPrepositions = map[string]struct{}{
	"in":   {},
	"with": {},
}

// goPhraseKeptWords — слова фразы, которые несут самостоятельную предметную
// нагрузку и должны ОСТАТЬСЯ в токенах после подстановки. Замена идёт только
// по слову "go" (go → golang), а не по фразе целиком: иначе фраза
// "go microservices" в факте съедала бы токен "microservices" и вопрос
// "What is your experience with microservices architecture?" переставал
// находить факт (регресс, пойманный golden-тестом M0.1).
// Служебные для фразы слова ("language", "developer") не сохраняются — они
// только добавляли бы шум в индекс.
var goPhraseKeptWords = map[string]struct{}{
	"microservices": {},
}

// goPhraseReplacement собирает replacement для go-фразы: канонический токен
// языка плюс сохранённые предметные слова фразы.
func goPhraseReplacement(next string) string {
	if _, kept := goPhraseKeptWords[next]; kept {
		return goLanguageCanonical + " " + next
	}
	return goLanguageCanonical
}

// goDenyAfter — слова, при которых "go" в go-фразе НЕ означает язык Go:
// "go live", "go ahead", "go through", "go about", "go to". Отдельно
// проверяется дефис: "go-to-market".
var goDenyAfter = map[string]struct{}{
	"to":      {},
	"live":    {},
	"ahead":   {},
	"through": {},
	"about":   {},
}

// goLanguageCanonical — канонический токен языка Go, дающий сработавшую
// подстановку. Это НЕ стоп-слово (в отличие от "go").
const goLanguageCanonical = "golang"

// substituteFlattened — плоский список фразовых (многословных) подстановок,
// отсортированный функцией init. Source — базовая форма в логах, replacement —
// каноническая форма, которая должна попасть в токены.
var substituteFlattened []substitution

// substitution — одна многословная подстановка.
// words — нормализованные слова фразы; replacement — канонический токен.
// bare — фраза засчитывается, только если она кончается на границе
// предложения/строки (для "in go" / "with go": "experience with go?").
type substitution struct {
	source      string
	words       []string
	replacement string
	bare        bool
}

func init() {
	substituteFlattened = buildSubstitutions()
}

// buildSubstitutions собирает фразовые подстановки: go-контекст + ASR-формы.
// Длинные фразы идут первыми — на сырой строке замена идёт слева направо,
// и короткая фраза не должна съесть начало длинной.
func buildSubstitutions() []substitution {
	var out []substitution

	// Go-контекст: "in go language", "with go microservices" и т.д.
	// Фраза эмитится только когда "go" стоит в разрешённом контексте и после
	// него нет deny-list слова/дефиса. Replacement сохраняет предметные слова
	// фразы (см. goPhraseKeptWords), поэтому "go microservices" даёт
	// [golang microservices], а не только [golang].
	for next := range goPhraseNext {
		repl := goPhraseReplacement(next)
		for prep := range goContextPrepositions {
			phrase := prep + " go " + next
			out = append(out, substitution{source: phrase, words: strings.Fields(phrase), replacement: repl})
		}
	}
	out = append(out, substitution{source: "go language", words: []string{"go", "language"}, replacement: goPhraseReplacement("language")})
	out = append(out, substitution{source: "go developer", words: []string{"go", "developer"}, replacement: goPhraseReplacement("developer")})
	out = append(out, substitution{source: "go microservices", words: []string{"go", "microservices"}, replacement: goPhraseReplacement("microservices")})

	// Bare-формы "with go" / "in go": реальный вопрос из логов
	// ("what is your experience with go?"). Идут последними — короткая фраза
	// не должна съесть начало длинной (with go microservices и т.п.).
	for prep := range goContextPrepositions {
		out = append(out, substitution{source: prep + " go", words: []string{prep, "go"}, replacement: goLanguageCanonical, bare: true})
	}

	// ASR-формы (M2.4). Источник — logs/session_*.csv; список наполняется
	// только формами, реально найденными в логах (см. docs/…). Пустая запись
	// здесь означает «в логах форм нет» — тогда словарь пуст.
	out = append(out, asrSubstitutions()...)

	// Длинные фразы первыми; при равенстве — лексикографически для
	// детерминированности.
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].words) != len(out[j].words) {
			return len(out[i].words) > len(out[j].words)
		}
		return out[i].source < out[j].source
	})
	return out
}

// asrSubstitutions возвращает подстановки ASR-форм из реальных логов (M2.4).
//
// Разбор logs/session_*.csv (11 файлов, 1322 записи, из них 177 EndOfTurn) и
// всех 27 файлов каталога logs/ (включая fatal-*.log) не дал НИ ОДНОЙ
// технической ASR-формы из списка плана: postgres*, k8s/kubernetes, spring
// boot, node js, high load, dot net, c plus/c sharp, php, laravel, mysql,
// yii, kafka, redis, docker — 0 вхождений. Единственное техническое
// упоминание — "what is your experience with go?" (покрывается go-контекстом).
//
// Поэтому список пуст: формы не придумываются. Механизм готов — новая форма
// добавляется сюда как substitution{source, words, replacement} и требует
// bump TokenizerVersion.
func asrSubstitutions() []substitution {
	return nil
}

// Normalize приводит строку к нижнему регистру и убирает пробелы по краям.
func (t *Tokenizer) Normalize(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// isWordRune — символ, входящий в слово для границ подстановки: буква или
// цифра (Unicode). Дефис, точка, плюс, решётка, слэш — границы.
func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// Substitute выполняет фразовые подстановки на СЫРОЙ нормализованной строке,
// до Tokenize и edge-trim, по границам слов (граница — не буква и не цифра:
// пробел, пунктуация, дефис).
//
// Правила:
//  1. Точное совпадение поля с спецтермином: c++ → cpp, c# → csharp,
//     .net → dotnet. "asp.net", "node.js", "c++20" не затрагиваются.
//  2. Go-контекст: in go / with go / go language / go developer /
//     go microservices → golang. Deny-list: если сразу после "go" стоит
//     дефис или слово to/live/ahead/through/about — подстановка не
//     срабатывает ("go-to-market", "go live", "to go with the first option").
//  3. ASR-формы из логов (asrSubstitutions).
//
// Заменённый фрагмент обрамляется ОДНИМ пробелом с каждой стороны, чтобы
// каноническая форма не слипалась с соседним словом, но при этом осталась
// отдельным токеном.
func (t *Tokenizer) Substitute(s string) string {
	if s == "" {
		return s
	}
	out := substituteTargetsOnFields(s)
	return strings.TrimSpace(substitutePhrases(out))
}

// substituteTargetsOnFields заменяет спецтермины точным совпадением поля.
//
// Точное совпадение означает: после срезания обычной пунктуации по краям поле
// равно ключу. Знаки, являющиеся ЧАСТЬЮ имени (замыкающие '+' у "c++",
// решётка у "c#", ведущая '.' у ".net"), при обрезке сохраняются, поэтому
// "c++," → "c++" → cpp, но "c++20" или "asp.net" не равны ни одному ключу и
// остаются как есть.
func substituteTargetsOnFields(s string) string {
	fields := strings.Fields(s)
	changed := false
	for i, f := range fields {
		key := termKey(f)
		repl, ok := substituteTargets[key]
		if !ok {
			// Вариант "(.net)": пунктуация вокруг имени с ведущей точкой.
			key = trimOuterPunct(f)
			repl, ok = substituteTargets[key]
		}
		if !ok {
			continue
		}
		fields[i] = repl
		changed = true
	}
	if !changed {
		return s
	}
	return strings.Join(fields, " ")
}

// termKey нормализует поле для точного сравнения со спецтермином: срезает
// пунктуацию по краям, сохраняя замыкающие '+' (часть "c++") и ведущую '.'
// (часть ".net"). Остальные знаки по краям отбрасываются.
func termKey(f string) string {
	f = strings.TrimSpace(f)
	f = strings.TrimLeft(f, "#+")
	left := 0
	for left < len(f) && !isWordRune(rune(f[left])) && f[left] != '.' && f[left] != '+' && f[left] != '#' {
		left++
	}
	f = f[left:]
	right := len(f)
	for right > 0 {
		r, size := utf8.DecodeLastRuneInString(f[:right])
		if isWordRune(r) || r == '+' || r == '#' {
			break
		}
		right -= size
	}
	return f[:right]
}

// trimOuterPunct срезает ПОЛНОСТЬЮ любую пунктуацию по краям, кроме решётки,
// креста и плюса: нужен для форм вида "(.net)" и "[c#]".
func trimOuterPunct(f string) string {
	f = strings.TrimSpace(f)
	f = strings.TrimLeftFunc(f, func(r rune) bool {
		return !isWordRune(r) && r != '.' && r != '+' && r != '#'
	})
	return strings.TrimRightFunc(f, func(r rune) bool {
		return !isWordRune(r) && r != '+' && r != '#'
	})
}

// substitutePhrases применяет фразовые подстановки за ОДИН проход, слева
// направо, с приоритетом самой длинной фразы в позиции (longest match first).
// Один проход, а не N последовательных замен, важен для go-контекста: при
// последовательных проходах короткая фраза ("with go") может сработать на
// месте, где длинная ("with go language") уже предусматривает отдельную
// семантику, а deny-list проверялся бы в другом месте. Каждая замена
// обособляется пробелами.
func substitutePhrases(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	pos := 0
	replaced := false
	for pos < len(s) {
		idx, end, sub, ok := longestPhraseAt(s, pos)
		if !ok {
			break
		}
		b.WriteString(s[pos:idx])
		b.WriteByte(' ')
		b.WriteString(sub.replacement)
		b.WriteByte(' ')
		pos = end
		replaced = true
	}
	if !replaced {
		return s
	}
	b.WriteString(s[pos:])
	return b.String()
}

// longestPhraseAt ищет самое левое и самое длинное совпадение фразы словаря
// начиная с pos. Фразы go-контекста отсекаются здесь же по deny-list — до
// того, как будет выбрана более короткая альтернатива, поэтому
// "with go microservices" всегда даёт golang, а "go ahead" — никогда.
func longestPhraseAt(s string, from int) (start, end int, sub substitution, ok bool) {
	for i := from; i < len(s); i++ {
		// Совпадение обязано начинаться на границе слова.
		if i > 0 && isWordRune(rune(s[i-1])) {
			continue
		}
		for _, cand := range substituteFlattened {
			matchEnd, matched := matchWords(s, i, cand.words)
			if !matched {
				continue
			}
			if matchEnd < len(s) && isWordRune(rune(s[matchEnd])) {
				continue
			}
			if cand.replacement == goLanguageCanonical && phraseDenied(s, i, cand.words) {
				continue
			}
			if cand.bare && !atPhraseBoundary(s, matchEnd) {
				// Bare "in go"/"with go" засчитывается только в конце фразы:
				// "experience with go?" да, "with go-to-market" нет.
				continue
			}
			return i, matchEnd, cand, true
		}
	}
	return 0, 0, substitution{}, false
}

// matchWords проверяет, что начиная с позиции start идут слова words через
// один пробельный разделитель. Возвращает позицию после последнего слова.
func matchWords(s string, start int, words []string) (int, bool) {
	pos := start
	for i, w := range words {
		if i > 0 {
			// Ровно один пробельный разделитель.
			if pos >= len(s) || (s[pos] != ' ' && s[pos] != '	' && s[pos] != '\n') {
				return 0, false
			}
			pos++
		}
		if pos+len(w) > len(s) || s[pos:pos+len(w)] != w {
			return 0, false
		}
		pos += len(w)
	}
	return pos, true
}

// atPhraseBoundary сообщает, что после позиции end начинается новый
// фрагмент: конец строки, пробел или пунктуация, НЕ являющаяся частью
// переносимого слова (дефис запрещён: "go-to-market").
func atPhraseBoundary(s string, end int) bool {
	if end >= len(s) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(s[end:])
	if r == '-' {
		return false
	}
	return !isWordRune(r)
}

// phraseDenied проверяет deny-list go-фразы: символ сразу после "go" (первого
// слова фразы) — дефис, или следующее слово после "go" входит в deny-list.
func phraseDenied(s string, goStart int, words []string) bool {
	afterGo := goStart + len(words[0])
	if afterGo < len(s) && s[afterGo] == '-' {
		return true
	}
	rest := s[afterGo:]
	rest = strings.TrimLeft(rest, " 	\n")
	if rest == "" {
		return false
	}
	word := rest
	if i := strings.IndexFunc(rest, func(r rune) bool { return !isWordRune(r) }); i >= 0 {
		word = rest[:i]
	}
	_, denied := goDenyAfter[word]
	return denied
}

// Tokenize разбивает строку по whitespace, затем каждый фрагмент — по "/" и
// "\\" (PHP/Laravel → php, laravel; TCP/IP → tcp, ip), затем по внутренним
// дефисам: эмитится и целый токен, и его части (high-load → high-load, high,
// load — ASR часто даёт "high load"). У каждого токена обрезаются ведущие и
// замыкающие символы, не являющиеся буквой или цифрой; пустые токены
// отбрасываются ("|", "—", "&" дают пустоту).
func (t *Tokenizer) Tokenize(s string) []string {
	fields := strings.Fields(s)
	tokens := make([]string, 0, len(fields))
	for _, f := range fields {
		for _, part := range strings.FieldsFunc(f, func(r rune) bool {
			return r == '/' || r == '\\'
		}) {
			tokens = appendHyphenParts(tokens, part)
		}
	}
	return tokens
}

// appendHyphenParts добавляет токен и, если внутри есть дефис, его части.
// Целый токен идёт первым; каждая часть дополнительно edge-trim'ится, а
// пустые части отбрасываются.
func appendHyphenParts(tokens []string, raw string) []string {
	whole := trimEdgePunct(raw)
	if whole == "" {
		return tokens
	}
	if !strings.Contains(whole, "-") {
		return append(tokens, whole)
	}
	// Целый токен идёт первым ("high-load"), затем каждая часть ("high",
	// "load"): ASR часто раскалывает составной термин на пробел, поэтому факты
	// с родительным падежом должны матчиться и по частям.
	tokens = append(tokens, whole)
	for _, part := range strings.Split(whole, "-") {
		if part = trimEdgePunct(part); part != "" {
			tokens = append(tokens, part)
		}
	}
	return tokens
}

// trimEdgePunct обрезает ведущие и замыкающие символы, не являющиеся буквой
// или цифрой.
func trimEdgePunct(s string) string {
	return strings.TrimFunc(s, func(r rune) bool { return !isWordRune(r) })
}

// RemoveStopWords отфильтровывает встроенные стоп-слова (EN + RU), сохраняя
// порядок оставшихся токенов. Токены должны быть уже нормализованы (lowercase).
func (t *Tokenizer) RemoveStopWords(tokens []string) []string {
	out := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		if _, ok := t.stopWords[tok]; ok {
			continue
		}
		out = append(out, tok)
	}
	return out
}

// Canonicalize заменяет каждый токен, присутствующий в map alias→canonical,
// на canonical term; остальные токены остаются как есть. Порядок сохраняется.
func (t *Tokenizer) Canonicalize(tokens []string) []string {
	out := make([]string, len(tokens))
	for i, tok := range tokens {
		if canon, ok := t.canonical[tok]; ok {
			out[i] = canon
		} else {
			out[i] = tok
		}
	}
	return out
}

// Process — полный pipeline:
// Normalize → Substitute → Tokenize → RemoveStopWords → Canonicalize.
//
// Порядок обязателен: Substitute работает на нормализованной сырой строке
// (токенов ещё нет), поэтому подстановки видят границы слов до edge-trim.
func (t *Tokenizer) Process(s string) []string {
	normalized := t.Normalize(s)
	substituted := t.Substitute(normalized)
	return t.Canonicalize(t.RemoveStopWords(t.Tokenize(substituted)))
}

// defaultStopWords возвращает множество стоп-слов на английском и русском:
// артикли, местоимения, предлоги, союзы, вспомогательные глаголы и частотные
// частицы. Все слова в нижнем регистре (токены перед фильтрацией нормализованы).
func defaultStopWords() map[string]struct{} {
	words := []string{
		// English — артикли.
		"a", "an", "the",
		// English — местоимения и определители.
		"i", "me", "my", "mine", "myself", "we", "our", "ours", "ourselves",
		"you", "your", "yours", "yourself", "yourselves",
		"he", "him", "his", "himself", "she", "her", "hers", "herself",
		"it", "its", "itself", "they", "them", "their", "theirs", "themselves",
		"this", "that", "these", "those", "who", "whom", "whose", "which",
		"what", "when", "where", "why", "how", "whoever", "whatever", "whichever",
		"all", "any", "some", "such", "each", "few", "more", "most", "other",
		"another", "own", "same", "no", "none", "several", "many", "much", "both",
		"either", "neither",
		// English — предлоги.
		"about", "above", "across", "after", "against", "along", "among", "around",
		"at", "before", "behind", "below", "beneath", "beside", "between", "beyond",
		"by", "down", "during", "except", "for", "from", "in", "inside", "into",
		"near", "of", "off", "on", "onto", "out", "over", "past", "per", "through",
		"to", "toward", "towards", "under", "until", "up", "upon", "via", "with",
		"within", "without",
		// English — союзы.
		"and", "but", "or", "nor", "so", "yet", "whether", "while", "although",
		"though", "if", "unless", "because", "since", "as", "than",
		// English — вспомогательные и модальные глаголы.
		"am", "is", "are", "was", "were", "be", "been", "being",
		"have", "has", "had", "having", "do", "does", "did", "doing",
		"will", "would", "shall", "should", "can", "could", "may", "might",
		"must", "ought",
		// English — частотные частицы/наречия.
		"not", "only", "also", "just", "very", "too", "then", "there", "here",
		"once", "again", "further", "furthermore", "yes",
		// English — двусмысленные языковые имена (матчатся бытовыми словами;
		// канонизация «in go» → golang — на этапе Substitute, см. Task 2b плана).
		"go",
		// Русский — предлоги.
		"в", "во", "на", "с", "со", "по", "о", "об", "от", "до", "из", "за",
		"над", "под", "при", "про", "без", "для", "к", "ко", "у", "через",
		"из-за", "из-под", "около", "между", "перед", "после", "против", "кроме",
		"ради", "среди", "благодаря", "вместо", "вдоль", "вокруг",
		// Русский — союзы.
		"и", "а", "но", "да", "или", "либо", "что", "чтобы", "если", "хотя",
		"пока", "так", "как", "тоже", "также", "зато", "однако", "ни", "не",
		"то", "то есть",
		// Русский — местоимения.
		"я", "мы", "ты", "вы", "он", "она", "оно", "они",
		"меня", "мне", "мной", "нас", "нам", "нами",
		"тебя", "тебе", "тобой", "вас", "вам", "вами",
		"его", "ему", "им", "её", "ей", "ею", "их", "ними",
		"себя", "себе", "собой",
		"мой", "моя", "моё", "мои", "наш", "наша", "наше", "наши",
		"ваш", "ваша", "ваше", "ваши", "свой", "своя", "своё", "свои",
		"этот", "эта", "это", "эти", "тот", "та", "те",
		"такой", "такая", "такое", "такие", "весь", "вся", "всё", "все",
		"кто", "какой", "какая", "какое", "какие", "чей", "чья", "чьё", "чьи",
		"который", "которая", "которое", "которые", "сколько", "столько",
		"никто", "ничто", "некто", "нечто", "кто-то", "что-то", "кое-кто", "кое-что",
		// Русский — вспомогательные глаголы (быть) и связки.
		"быть", "есть", "был", "была", "было", "были",
		"буду", "будешь", "будет", "будем", "будете", "будут", "бы", "б",
		// Русский — частотные частицы.
		"же", "ли", "ль", "уж", "ведь", "вот", "вон", "лишь", "только",
		"даже", "именно", "просто", "разве", "неужели", "пусть", "пускай",
		"давай", "давайте", "мол", "дескать", "типа", "как-то", "нибудь", "таки",
	}

	m := make(map[string]struct{}, len(words))
	for _, w := range words {
		m[w] = struct{}{}
	}
	return m
}
