package candidatecontext

import "sort"

// ASR-fuzzy нормализация запроса (F13): токены запроса, отсутствующие в
// словаре индекса, корректируются по Damerau-Levenshtein ≤ 1. Лечит одиночные
// сбойные символы ASR в технических терминах («kubernets» → «kubernetes»,
// «postgresq» → «postgresql»): BM25F остаётся lexical — чинится вход.
//
// Коррекция ТОЛЬКО runtime-side, на токенах запроса: индекс не меняется,
// TokenizerVersion не двигается. Словарь — токены индекса (ключи dfMap:
// union keyword/title/alias/content токенов всех фактов), уже прошедшие
// полный pipeline токенизатора — сравнение идёт в одной семантике.
//
// Ограничение (осознанное): DL ≤ 1 ловит одиночную ошибку символа.
// Многосимвольное искажение («cooberneties») после стемминга расходится со
// словарём дальше, чем на 1, и не корректируется — такие случаи ловит
// dense-ветка гибрида (F13, флаг candidate_context.hybrid).

const (
	// fuzzyMinTokenLen — минимальная длина токена для коррекции: короткие
	// токены дают слишком много ложных кандидатов в радиусе 1
	// («redis»/«resis», «go»/«gо»), польза меньше риска.
	fuzzyMinTokenLen = 5
	// fuzzyMaxDist — максимальное расстояние Damerau-Levenshtein.
	fuzzyMaxDist = 1
)

// fuzzyCorrector — словарная коррекция токенов запроса. Иммутабелен после
// создания, потокобезопасен.
type fuzzyCorrector struct {
	// vocab — отсортированный словарь индекса (для детерминизма выбора
	// кандидата); vocabSet — то же множеством для проверки «в словаре».
	vocab    []string
	vocabSet map[string]struct{}
}

// newFuzzyCorrector строит корректор по словарю токенов индекса.
func newFuzzyCorrector(vocab map[string]struct{}) *fuzzyCorrector {
	if len(vocab) == 0 {
		return nil
	}
	vs := make([]string, 0, len(vocab))
	for t := range vocab {
		vs = append(vs, t)
	}
	sort.Strings(vs)
	return &fuzzyCorrector{vocab: vs, vocabSet: vocab}
}

// Correct возвращает копию токенов с применённой словарной коррекцией:
// токен короче fuzzyMinTokenLen, уже в словаре или не имеющий РОВНО ОДНОГО
// кандидата в радиусе fuzzyMaxDist — остаётся как есть. Неоднозначность
// (два словарных токена в радиусе 1) не корректируется: неверная подмена
// дороже пропущенной. nil-корректор (пустой словарь / выключен опцией)
// возвращает вход как есть.
func (c *fuzzyCorrector) Correct(tokens []string) []string {
	if c == nil || len(tokens) == 0 {
		return tokens
	}
	out := make([]string, len(tokens))
	for i, tok := range tokens {
		out[i] = c.correctToken(tok)
	}
	return out
}

// correctToken корректирует один токен (см. Correct).
func (c *fuzzyCorrector) correctToken(tok string) string {
	if len(tok) < fuzzyMinTokenLen {
		return tok
	}
	if _, ok := c.vocabSet[tok]; ok {
		return tok
	}
	match := ""
	found := false
	for _, v := range c.vocab {
		if withinOneEdit(tok, v) {
			if found && v != match {
				return tok // неоднозначно — не рискуем
			}
			match, found = v, true
		}
	}
	if !found {
		return tok
	}
	return match
}

// withinOneEdit проверяет, что строки различаются не более чем на одну
// правку Damerau-Levenshtein: замена, вставка, удаление или транспозиция
// двух соседних символов. Работает по ранам (UTF-8-безопасно).
func withinOneEdit(a, b string) bool {
	if a == b {
		return true
	}
	ra, rb := []rune(a), []rune(b)
	if len(ra) > len(rb) {
		ra, rb = rb, ra
	}
	// Теперь ra — короче или той же длины, что rb.
	if len(rb)-len(ra) > fuzzyMaxDist {
		return false
	}
	for i := 0; i < len(ra); i++ {
		if ra[i] != rb[i] {
			if len(ra) == len(rb) {
				// Замена: хвосты совпадают.
				if string(ra[i+1:]) == string(rb[i+1:]) {
					return true
				}
				// Транспозиция соседних: ra[i]↔ra[i+1] и хвосты совпадают.
				if i+1 < len(ra) && ra[i] == rb[i+1] && ra[i+1] == rb[i] &&
					string(ra[i+2:]) == string(rb[i+2:]) {
					return true
				}
				return false
			}
			// Вставка в rb (удаление из ra): пропускаем символ rb[i].
			return string(ra[i:]) == string(rb[i+1:])
		}
	}
	// Совпали все символы ra; разница — хвост rb.
	return len(rb)-len(ra) == fuzzyMaxDist
}
