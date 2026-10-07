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
	return ui.Answer{Source: source}, false
}
