package candidatecontext

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestGenerateCCFNGolden — временный генератор golden для M0.1.
//
// Запуск: CCFN_UPDATE=1 go test ./internal/context/ -run TestGenerateCCFNGolden -count=1
//
// Осознанная перегенерация допускается только при изменении поведения
// осознанно (M4/M5/M6): каждый такой прогон фиксируется в плане вместе с
// причиной расхождения. Без CCFN_UPDATE тест ничего не делает.
func TestGenerateCCFNGolden(t *testing.T) {
	if os.Getenv("CCFN_UPDATE") != "1" {
		t.Skip("CCFN_UPDATE!=1 — генератор golden не запускается")
	}

	cfg := ccConfig(copyFixture(t, filepath.Join("testdata", "ccfn", "cc")))
	cfg.HintsDir = copyFixture(t, filepath.Join("testdata", "ccfn", "hints"))
	cfg.HintsTopK = 1
	cfg.HintsMinScore = 0

	fn, err := BuildContextFn(cfg)
	if err != nil {
		t.Fatalf("BuildContextFn: %v", err)
	}

	questions := goldenQuestions(t)
	var buf bytes.Buffer
	for i, q := range questions {
		fmt.Fprintf(&buf, "## Q%02d: %s\n", i+1, q)
		buf.WriteString(fn(q, SessionContext{}).Text)
		buf.WriteString("\n--\n")
	}

	target := filepath.Join("testdata", "ccfn_golden.txt")
	old, readErr := os.ReadFile(target)
	if readErr == nil && bytes.Equal(old, buf.Bytes()) {
		t.Logf("golden не изменился (%d байт, %d вопросов)", buf.Len(), len(questions))
		return
	}
	if err := os.WriteFile(target, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("запись golden: %v", err)
	}
	oldLen := 0
	if readErr == nil {
		oldLen = len(old)
	}
	t.Logf("golden перегенерирован: %d → %d байт, вопросов %d", oldLen, buf.Len(), len(questions))
}
