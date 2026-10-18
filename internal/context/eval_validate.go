package candidatecontext

import (
	"fmt"
	"regexp"
	"strings"
)

// evalSourceRe — допустимые значения EvalQuery.Source (зеркало
// eval_schema_v2.json): дословная цитата из лога сессии, вопрос из публичного
// списка или сконструированный кейс с указанной причиной.
var evalSourceRe = regexp.MustCompile(`^(transcript:session_[0-9]{4}-[0-9]{2}-[0-9]{2}(_[0-9]{2}-[0-9]{2}-[0-9]{2})?|public_list:.+|synthetic:.+)$`)

// Допустимые значения полей кейса v2 (зеркало eval_schema_v2.json).
var (
	evalCategories = map[string]bool{"negative": true, "neutral": true, "explicit": true, "mixed": true}
	evalLangs      = map[string]bool{"en": true, "ru": true}
	evalSplits     = map[string]bool{"train": true, "holdout": true}
)

// TagNoRelevant помечает кейсы, у которых разметка пуста по замыслу: выдача
// должна быть пуста, и такие кейсы считаются в EmptyRate, а не в Recall.
const TagNoRelevant = "no_relevant"

// LoadFactIDs собирает множество FactID каталога фактов (manifest + sections).
// Индекс пересобирается на месте, как и в проде: валидатор разметки должен
// видеть ровно те ID, которые видит retrieval.
func LoadFactIDs(dir string) (map[string]bool, error) {
	_, index, err := LoadCandidateContext(dir)
	if err != nil {
		return nil, fmt.Errorf("candidatecontext: load fact ids from %s: %w", dir, err)
	}
	ids := make(map[string]bool, len(index.Facts))
	for _, f := range index.Facts {
		ids[f.ID] = true
	}
	return ids, nil
}

// MergeFactIDSets объединяет множества FactID нескольких каталогов и возвращает
// имена пересечений. Один и тот же ID в candidate_context и hints делает
// разметку неоднозначной: непонятно, какой именно факт имелся в виду.
func MergeFactIDSets(sets ...map[string]bool) (map[string]bool, []string) {
	merged := map[string]bool{}
	var duplicates []string
	seen := map[string]bool{}
	for _, set := range sets {
		for id := range set {
			if merged[id] && !seen[id] {
				duplicates = append(duplicates, id)
				seen[id] = true
			}
			merged[id] = true
		}
	}
	return merged, duplicates
}

// ValidateOptions настраивает проверку eval-набора.
type ValidateOptions struct {
	// Split — ожидаемая выборка всех кейсов файла ("train"/"holdout").
	// Пустая строка отключает проверку (legacy-наборы без split).
	Split string
	// KnownFactIDs — все FactID обоих каталогов (cc ∪ hints).
	KnownFactIDs map[string]bool
	// RequireV2 — требовать полноту полей v2-кейса и согласованность
	// category/tags/source. Legacy (frozen) наборы проверяются без этого флага.
	RequireV2 bool
}

// ValidateEvalDataset проверяет разметку набора и возвращает список проблем
// (пустой — набор валиден). Проверки сознательно не «чинят» данные: любая
// неоднозначность разметки — ошибка, а не повод угадать.
func ValidateEvalDataset(ds *EvalDataset, opts ValidateOptions) []error {
	if ds == nil {
		return []error{fmt.Errorf("eval dataset: nil")}
	}
	if len(ds.Queries) == 0 {
		return []error{fmt.Errorf("eval dataset: пустой набор (нет queries)")}
	}

	var errs []error
	seenIDs := map[string]bool{}

	for i, q := range ds.Queries {
		label := q.ID
		if label == "" {
			label = fmt.Sprintf("#%d", i+1)
		}

		if q.ID != "" {
			if seenIDs[q.ID] {
				errs = append(errs, fmt.Errorf("кейс %s: дублирующийся id", label))
			}
			seenIDs[q.ID] = true
		}
		if strings.TrimSpace(q.Question) == "" {
			errs = append(errs, fmt.Errorf("кейс %s: пустой question", label))
		}

		mustNot := toSet(q.MustNotFactIDs)
		for _, id := range q.RelevantFactIDs {
			if _, ok := mustNot[id]; ok {
				errs = append(errs, fmt.Errorf("кейс %s: FactID %q одновременно в relevant и must_not", label, id))
			}
		}
		for _, id := range append(append([]string{}, q.RelevantFactIDs...), q.MustNotFactIDs...) {
			if opts.KnownFactIDs != nil && !opts.KnownFactIDs[id] {
				errs = append(errs, fmt.Errorf("кейс %s: FactID %q отсутствует в индексах candidate_context ∪ hints", label, id))
			}
		}

		if !opts.RequireV2 {
			continue
		}

		if q.ID == "" {
			errs = append(errs, fmt.Errorf("кейс %s: обязательное поле id пусто", label))
		}
		if !evalLangs[q.Lang] {
			errs = append(errs, fmt.Errorf("кейс %s: недопустимый lang %q (ожидается en|ru)", label, q.Lang))
		}
		if !evalCategories[q.Category] {
			errs = append(errs, fmt.Errorf("кейс %s: недопустимая category %q (negative|neutral|explicit|mixed)", label, q.Category))
		}
		if !evalSourceRe.MatchString(q.Source) {
			errs = append(errs, fmt.Errorf("кейс %s: source %q не соответствует схеме (transcript:session_YYYY-MM-DD|public_list:<...>|synthetic:<...>)", label, q.Source))
		}
		if !evalSplits[q.Split] {
			errs = append(errs, fmt.Errorf("кейс %s: недопустимый split %q (train|holdout)", label, q.Split))
		} else if opts.Split != "" && q.Split != opts.Split {
			errs = append(errs, fmt.Errorf("кейс %s: split %q не совпадает с назначением файла (%s)", label, q.Split, opts.Split))
		}
		if q.Category == "negative" && len(q.MustNotFactIDs) == 0 {
			errs = append(errs, fmt.Errorf("кейс %s: category=negative требует непустой must_not (иначе LeakRate не на чем считать)", label))
		}

		hasNoRelevant := false
		for _, tag := range q.Tags {
			if tag == TagNoRelevant {
				hasNoRelevant = true
			}
		}
		switch {
		case hasNoRelevant && len(q.RelevantFactIDs) > 0:
			errs = append(errs, fmt.Errorf("кейс %s: тег %s требует пустой relevant", label, TagNoRelevant))
		case !hasNoRelevant && len(q.RelevantFactIDs) == 0:
			errs = append(errs, fmt.Errorf("кейс %s: пустой relevant требует тег %s", label, TagNoRelevant))
		}

		for _, target := range q.ExpectEffectiveTarget {
			if target != strings.ToLower(target) {
				errs = append(errs, fmt.Errorf("кейс %s: expect_effective_target %q должен быть в нижнем регистре", label, target))
			}
		}
	}

	return errs
}
