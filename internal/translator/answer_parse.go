package translator

import (
	"strings"

	"github.com/mastererik/translator/internal/ui"
)

// ParseAnswer splits a bilingual hint line produced by the LLM into its source
// and target parts. The expected format is:
//
//	<SRC_TAG>: <answer in source> | <TGT_TAG>: <translation in target>
//
// where the tags are derived from the configured languages via
// strings.ToUpper(lang) (e.g. "EN", "RU", "DE"). The separator is the literal
// "| <TGT_TAG>:". The returned bool reports whether a target part was found:
// false means the target separator is absent and the whole trimmed line is
// returned as Source with an empty Target (the caller must not lose text).
func ParseAnswer(s, srcTag, tgtTag string) (ui.Answer, bool) {
	sep := "| " + tgtTag + ":"
	parts := strings.SplitN(s, sep, 2)
	source := strings.TrimSpace(parts[0])
	if len(parts) == 2 {
		return ui.Answer{Source: source, Target: strings.TrimSpace(parts[1])}, true
	}
	// Fallback: LLM при регенерации/деградации формата может написать тег цели
	// на отдельной строке («EN: …\nRU: …») или отделить его пробелом без «|».
	// Делим по тегу цели, чтобы русская часть не сливалась с английской в Source
	// (иначе весь ответ красится одним цветом).
	for _, delim := range []string{"\n" + tgtTag + ":", " " + tgtTag + ":"} {
		if idx := strings.Index(s, delim); idx >= 0 {
			return ui.Answer{
				Source: strings.TrimSpace(s[:idx]),
				Target: strings.TrimSpace(s[idx+len(delim):]),
			}, true
		}
	}
	return ui.Answer{Source: source}, false
}
