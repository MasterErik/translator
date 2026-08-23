package candidatecontext

import (
	"fmt"
	"strings"
	"unicode"
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

// Normalize приводит строку к нижнему регистру и убирает пробелы по краям.
func (t *Tokenizer) Normalize(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// Tokenize разбивает строку по whitespace и у каждого токена обрезает ведущие
// и замыкающие символы, не являющиеся буквой или цифрой. Внутренние дефисы и
// цифры сохраняются. Пустые (после обрезки) токены отбрасываются.
func (t *Tokenizer) Tokenize(s string) []string {
	fields := strings.Fields(s)
	tokens := make([]string, 0, len(fields))
	for _, f := range fields {
		tok := strings.TrimFunc(f, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		})
		if tok != "" {
			tokens = append(tokens, tok)
		}
	}
	return tokens
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

// Process — полный pipeline: Normalize → Tokenize → RemoveStopWords → Canonicalize.
func (t *Tokenizer) Process(s string) []string {
	return t.Canonicalize(t.RemoveStopWords(t.Tokenize(t.Normalize(s))))
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
