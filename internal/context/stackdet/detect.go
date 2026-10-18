package stackdet

// Detect строит Sets по токенам вопроса, сессионному стеку и семействам
// технологий. questionsTokens — уже нормализованные (lowercase) и
// канонизированные токены; сессия и семейства — списки канонических
// идентификаторов стеков.
//
// Правила:
//   - однозначное упоминание: токен вопроса совпал с Family.Name или с
//     членом семьи, не перечисленным в Family.Ambiguous;
//   - неоднозначный член (spring, swift, ...) не попадает ни в Penalty, ни в
//     Named — только в Exempt для своего семейства, если у семьи нет
//     однозначного упоминания в этом же вопросе;
//   - белый список генерируется из families; ничего не хардкодится.
//
// Входные срезы не мутируются. Пустые входы допустимы и дают пустые Sets.
func Detect(questionTokens, session []string, families []Family) Sets {
	s := newSets()

	// Сессия попадает в штрафное множество целиком: вопрос был про этот
	// стек, значит факты другого стека — чужие.
	for _, st := range session {
		if st == "" {
			continue
		}
		s.Penalty[st] = struct{}{}
	}

	// Индексы семейства: имя → семья, однозначные члены и неоднозначные
	// члены. Ambiguous имеет приоритет над Members: член, попавший в оба
	// списка, считается неоднозначным (правило плана: список неоднозначных
	// расширяется без изменения Members).
	type familyIndex struct {
		name        string
		unambiguous map[string]struct{}
		ambiguous   map[string]struct{}
	}
	byName := make(map[string]*familyIndex, len(families))
	ambiguity := make(map[string]bool) // member → неоднозначен хотя бы в одной семье
	order := make([]string, 0, len(families))
	for i := range families {
		f := &families[i]
		if f.Name == "" {
			continue
		}
		fi := &familyIndex{
			name:        f.Name,
			unambiguous: make(map[string]struct{}, len(f.Members)),
			ambiguous:   make(map[string]struct{}, len(f.Ambiguous)),
		}
		for _, a := range f.Ambiguous {
			if a != "" {
				fi.ambiguous[a] = struct{}{}
			}
		}
		for _, m := range f.Members {
			if m == "" {
				continue
			}
			if _, amb := fi.ambiguous[m]; amb {
				continue
			}
			fi.unambiguous[m] = struct{}{}
		}
		byName[fi.name] = fi
		order = append(order, fi.name)
	}

	// byMember строится отдельным проходом в порядке families: неоднозначные
	// члены регистрируются последними и перетирают однозначную регистрацию
	// того же слова другой семьёй. Неоднозначность — свойство слова, а не
	// семьи: если слово помечено Ambiguous хотя бы где-то, оно не может быть
	// однозначным упоминанием ни для какой семьи (loud-safe).
	byMember := make(map[string]*familyIndex, len(families))
	for _, name := range order {
		fi := byName[name]
		for m := range fi.unambiguous {
			if _, ok := byMember[m]; !ok {
				byMember[m] = fi
			}
		}
	}
	for _, name := range order {
		fi := byName[name]
		for a := range fi.ambiguous {
			byMember[a] = fi
			ambiguity[a] = true
		}
	}

	unambiguousHits := make(map[string]struct{})
	ambiguousHits := make(map[string]struct{})
	for _, tok := range questionTokens {
		if tok == "" {
			continue
		}
		fi, ok := byMember[tok]
		if !ok {
			// Слово вне известных семей — не упоминание стека, штрафа нет.
			continue
		}
		if ambiguity[tok] {
			ambiguousHits[fi.name] = struct{}{}
			continue
		}
		unambiguousHits[fi.name] = struct{}{}
	}

	for name := range unambiguousHits {
		s.Named[name] = struct{}{}
		s.Penalty[name] = struct{}{}
	}
	for name := range ambiguousHits {
		if _, named := unambiguousHits[name]; named {
			// Семейство названо однозначно — Exempt ему не нужен.
			continue
		}
		s.Exempt[name] = struct{}{}
	}
	return s
}
