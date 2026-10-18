package candidatecontext

// Тесты единой сборки контекста (M0 плана candidate-context-precision-v3.1):
// перенесены из internal/pipeline (candidate_context_test.go, hints_test.go) и
// дополнены характеризующим golden-тестом, семантикой D14/D15 и индикатором
// отключённых hints.

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func noopLog(string) {}

// copyFixture копирует каталог фикстуры в t.TempDir: LoadCandidateContext
// пересобирает индекс рядом с manifest.json, а testdata должен оставаться
// неизменным (в фикстурах индекса нет намеренно).
func copyFixture(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copyFixture(%s): %v", src, err)
	}
	return dst
}

// writeCCDir создаёт валидную структуру candidate_context (manifest + факт).
func writeCCDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "manifest.json"), `{
  "profile": "Erik Ivanov",
  "global_aliases": {},
  "sections": [{"id": "experience", "title": "Опыт"}]
}`)
	writeFile(t, filepath.Join(dir, "sections", "experience.md"), `<!-- fact
{
  "id": "pricing-engine",
  "section": "experience",
  "title": "Pricing Engine",
  "keywords": ["pricing", "kafka"]
}
-->
Built a real-time pricing engine in Go.
`)
	return dir
}

// writeInvalidCCDir создаёт структуру, падающую на сборке индекса: manifest
// ссылается на секцию без source-файла.
func writeInvalidCCDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "manifest.json"), `{"profile": "Erik", "sections": [{"id": "experience"}]}`)
	return dir
}

// writeHintsDir создаёт валидный каталог hints с двумя фактами.
func writeHintsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "manifest.json"), `{
  "profile": "Hints",
  "global_aliases": {},
  "sections": [{"id": "answers", "title": "Ответы"}]
}`)
	writeFile(t, filepath.Join(dir, "sections", "answers.md"), `<!-- fact
{
  "id": "hint-weaknesses",
  "section": "answers",
  "title": "Weaknesses",
  "keywords": ["weakness", "weaknesses"]
}
-->
My main weakness is perfectionism: I prefer to verify assumptions early.
<!-- fact
{
  "id": "hint-relocation",
  "section": "answers",
  "title": "Relocation",
  "keywords": ["relocation", "relocate"]
}
-->
I am open to relocation to any EU country.
`)
	return dir
}

// writeLegacyFile создаёт временный legacy-файл с содержимым.
func writeLegacyFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cv.md")
	writeFile(t, p, content)
	return p
}

// ccConfig — конфиг fact-level режима на переданном каталоге.
func ccConfig(dir string) ContextFnConfig {
	return ContextFnConfig{
		Dir:              dir,
		MaxTokens:        2000,
		MaxProfileTokens: 150,
		MaxMatrixTokens:  100,
		MinScore:         0,
		TopK:             5,
	}
}

// TestBuildContextFnLegacy — legacy путь: Dir="", файл задан (D14/D15 без изменений).
func TestBuildContextFnLegacy(t *testing.T) {
	content := "# Candidate CV\nsome facts\n"
	file := writeLegacyFile(t, content)

	fn, err := BuildContextFn(ContextFnConfig{LegacyFile: file, Logf: noopLog})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := fn("any question", SessionContext{})
	if got.Text != content {
		t.Errorf("legacy context: got %q, want %q", got.Text, content)
	}
	if len(got.FactIDs) != 0 || len(got.HintIDs) != 0 {
		t.Errorf("legacy-режим не должен выдавать ID фактов: %v %v", got.FactIDs, got.HintIDs)
	}
}

// TestBuildContextFnNoSource — ни Dir, ни LegacyFile → пустой контекст без ошибки.
func TestBuildContextFnNoSource(t *testing.T) {
	fn, err := BuildContextFn(ContextFnConfig{Logf: noopLog})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := fn("question", SessionContext{})
	if got.Text != "" {
		t.Errorf("Text: got %q, want empty", got.Text)
	}
	if got.Degraded {
		t.Error("Degraded не должен выставляться без конфигурации")
	}
}

// TestBuildContextFnFactLevel — факт-режим: профиль + релевантный факт, ID факта
// доступны для eval (LeakRate считается по итоговой выдаче).
func TestBuildContextFnFactLevel(t *testing.T) {
	dir := writeCCDir(t)
	fn, err := BuildContextFn(ccConfig(dir))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := fn("pricing engine kafka", SessionContext{})
	if !strings.Contains(got.Text, "Erik Ivanov") {
		t.Errorf("rendered context должен содержать профиль, got %q", got.Text)
	}
	if !strings.Contains(got.Text, "pricing engine") {
		t.Errorf("rendered context должен содержать факт, got %q", got.Text)
	}
	if len(got.FactIDs) == 0 || got.FactIDs[0] != "pricing-engine" {
		t.Errorf("FactIDs: got %v, want [pricing-engine ...]", got.FactIDs)
	}
}

// TestBuildContextFnDirPriorityOverLegacyFile — при Dir и legacy файле выигрывает
// fact-level режим (legacy-файл даже не читается).
func TestBuildContextFnDirPriorityOverLegacyFile(t *testing.T) {
	cfg := ccConfig(writeCCDir(t))
	cfg.LegacyFile = writeLegacyFile(t, "legacy candidate context")
	cfg.AllowLegacyFallback = true

	fn, err := BuildContextFn(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := fn("pricing engine kafka", SessionContext{})
	if strings.Contains(got.Text, "legacy candidate context") {
		t.Errorf("legacy-файл не должен использоваться при рабочем Dir, got %q", got.Text)
	}
	if !strings.Contains(got.Text, "Erik Ivanov") {
		t.Errorf("rendered context должен содержать профиль, got %q", got.Text)
	}
}

// TestBuildContextFnFallbackRequiresOptIn — D14: сбой загрузки каталога валит
// старт, если allow_legacy_fallback не включён; с ним читается legacy-файл.
func TestBuildContextFnFallbackRequiresOptIn(t *testing.T) {
	badDir := writeInvalidCCDir(t)
	content := "legacy candidate context"
	legacy := writeLegacyFile(t, content)

	// (a) fallback не разрешён — ошибка.
	cfg := ccConfig(badDir)
	cfg.LegacyFile = legacy
	if _, err := BuildContextFn(cfg); err == nil {
		t.Fatal("ожидалась ошибка: Dir сломан, allow_legacy_fallback выключен")
	}

	// (b) fallback разрешён — legacy-файл целиком.
	cfg.AllowLegacyFallback = true
	fn, err := BuildContextFn(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := fn("q", SessionContext{}); got.Text != content {
		t.Errorf("fallback: got %q, want %q", got.Text, content)
	}
}

// TestBuildContextFnLegacyReadFailureFailsStart — M0.4b: нечитаемый файл —
// ошибка старта, а не тихая пустая строка (поведение до M0).
func TestBuildContextFnLegacyReadFailureFailsStart(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.md")

	if _, err := BuildContextFn(ContextFnConfig{LegacyFile: missing, Logf: noopLog}); err == nil {
		t.Fatal("ожидалась ошибка при нечитаемом legacy-файле")
	}

	// То же при разрешённом fallback: причина ошибки не теряется.
	cfg := ccConfig(writeInvalidCCDir(t))
	cfg.LegacyFile = missing
	cfg.AllowLegacyFallback = true
	if _, err := BuildContextFn(cfg); err == nil {
		t.Fatal("ожидалась ошибка: fallback разрешён, но файл нечитаем")
	}
}

// TestBuildContextFnDegraded — D15: on_load_error=degraded не роняет старт,
// выдаёт явную пометку и не выдаёт фактов.
func TestBuildContextFnDegraded(t *testing.T) {
	var logs []string
	cfg := ccConfig(writeInvalidCCDir(t))
	cfg.OnLoadError = OnLoadErrorDegraded
	cfg.Logf = func(msg string) { logs = append(logs, msg) }

	fn, err := BuildContextFn(cfg)
	if err != nil {
		t.Fatalf("degraded-режим не должен возвращать ошибку: %v", err)
	}
	got := fn("pricing engine kafka", SessionContext{})
	if !got.Degraded {
		t.Error("Degraded должен быть выставлен")
	}
	if len(got.FactIDs) != 0 {
		t.Errorf("degraded-режим не должен выдавать факты: %v", got.FactIDs)
	}
	if !strings.Contains(got.Text, "degraded") {
		t.Errorf("degraded-текст должен содержать явную пометку, got %q", got.Text)
	}
	var hasError bool
	for _, l := range logs {
		if strings.HasPrefix(l, "ERROR:") {
			hasError = true
		}
	}
	if !hasError {
		t.Errorf("degraded-режим должен писать ERROR в лог, got %v", logs)
	}
}

// TestBuildContextFnInvalidOnLoadError — неизвестное значение ловится сразу.
func TestBuildContextFnInvalidOnLoadError(t *testing.T) {
	cfg := ccConfig(writeCCDir(t))
	cfg.OnLoadError = "oops"
	if _, err := BuildContextFn(cfg); err == nil {
		t.Fatal("ожидалась ошибка для неизвестного on_load_error")
	}
}

// TestBuildContextFnHintsDisabledOnBrokenDir — M0.4c: сбой hints нефатален,
// факты кандидата продолжают работать, флаг отключения выставлен.
func TestBuildContextFnHintsDisabledOnBrokenDir(t *testing.T) {
	cfg := ccConfig(writeCCDir(t))
	cfg.HintsDir = t.TempDir() // без manifest.json
	var logs []string
	cfg.Logf = func(msg string) { logs = append(logs, msg) }

	fn, err := BuildContextFn(cfg)
	if err != nil {
		t.Fatalf("сбой hints не должен быть фатальным: %v", err)
	}
	got := fn("pricing engine kafka", SessionContext{})
	if !got.HintsDisabled {
		t.Error("HintsDisabled должен быть выставлен")
	}
	if !strings.Contains(got.Text, "Erik Ivanov") {
		t.Errorf("факты кандидата должны работать без hints, got %q", got.Text)
	}
}

// TestBuildContextFnHintsMatchAndMiss — подсказка по keywords и промах.
func TestBuildContextFnHintsMatchAndMiss(t *testing.T) {
	cfg := ccConfig(writeCCDir(t))
	cfg.HintsDir = writeHintsDir(t)
	cfg.HintsTopK = 1
	cfg.HintsMinScore = 0

	fn, err := BuildContextFn(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tests := []struct {
		name       string
		question   string
		wantSubstr string
		wantEmpty  bool
	}{
		{name: "match по keywords", question: "What is your biggest weakness?", wantSubstr: "perfectionism"},
		{name: "match по второму факту", question: "Are you open to relocation?", wantSubstr: "relocation to any EU country"},
		{name: "посторонний вопрос", question: "quantum chromodynamics lattice", wantEmpty: true},
		{name: "пустой вопрос", question: "", wantEmpty: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := fn(tc.question, SessionContext{})
			if tc.wantEmpty {
				if len(got.HintIDs) != 0 {
					t.Errorf("HintIDs: got %v, want empty", got.HintIDs)
				}
				if strings.Contains(got.Text, "Suggested answer") {
					t.Errorf("подсказка не должна появляться: %q", got.Text)
				}
				return
			}
			if !strings.Contains(got.Text, "Suggested answer (use as is):") {
				t.Errorf("ожидался блок подсказки, got %q", got.Text)
			}
			if !strings.Contains(got.Text, tc.wantSubstr) {
				t.Errorf("got %q, want substring %q", got.Text, tc.wantSubstr)
			}
			if len(got.HintIDs) == 0 {
				t.Error("HintIDs должны содержать показанный hint-факт")
			}
		})
	}
}

// TestAppendHint — склейка базового контекста и подсказки (перенесено из pipeline).
func TestAppendHint(t *testing.T) {
	const header = "Suggested answer (use as is):"

	tests := []struct {
		name     string
		base     string
		hint     string
		want     string
		contains []string
	}{
		{name: "пустой hint возвращает base без изменений", base: "base answer", hint: "", want: "base answer"},
		{name: "пустой hint и пустой base", base: "", hint: "", want: ""},
		{name: "непустой hint при пустом base начинается с заголовка", base: "", hint: "hint body", want: header + "\nhint body", contains: []string{header, "hint body"}},
		{name: "оба непустые содержат base и hint", base: "base answer", hint: "hint body", want: "base answer\n\n" + header + "\nhint body", contains: []string{"base answer", header, "hint body"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := AppendHint(tc.base, tc.hint)
			if got != tc.want {
				t.Errorf("AppendHint(%q, %q): got %q, want %q", tc.base, tc.hint, got, tc.want)
			}
			for _, sub := range tc.contains {
				if !strings.Contains(got, sub) {
					t.Errorf("AppendHint(%q, %q): got %q, want substring %q", tc.base, tc.hint, got, sub)
				}
			}
			if tc.hint == "" && got != tc.base {
				t.Errorf("пустой hint не должен менять base: got %q, want %q", got, tc.base)
			}
		})
	}
}

// TestBuildContextFnZeroBudgetsFallBackToDefaults — нулевые бюджеты в конфиге
// означают «не задано»: Budgeter с maxTokens=0 отдал бы в prompt только
// усечённый top-1 (тихая потеря контекста). Проверяем, что включился дефолт и
// что оператор увидел WARN.
func TestBuildContextFnZeroBudgetsFallBackToDefaults(t *testing.T) {
	var logs []string
	cfg := ContextFnConfig{
		Dir:  writeCCDir(t),
		TopK: 5,
		Logf: func(msg string) { logs = append(logs, msg) },
	}

	fn, err := BuildContextFn(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := fn("pricing engine kafka", SessionContext{})
	if len(got.FactIDs) == 0 || got.FactIDs[0] != "pricing-engine" {
		t.Errorf("FactIDs: got %v, want [pricing-engine]", got.FactIDs)
	}
	if !strings.Contains(got.Text, "Built a real-time pricing engine") {
		t.Errorf("тело факта должно попасть в контекст целиком, got %q", got.Text)
	}

	var warned int
	for _, l := range logs {
		if strings.HasPrefix(l, "WARN:") {
			warned++
		}
	}
	if warned != 3 {
		t.Errorf("ожидались WARN про все три бюджета (max_tokens, max_profile_tokens, max_matrix_tokens), got %v", logs)
	}
	if !containsLog(logs, fmt.Sprintf("%d", DefaultMaxMatrixTokens)) {
		t.Errorf("в WARN нет дефолта матрицы %d: %v", DefaultMaxMatrixTokens, logs)
	}
}

// containsLog — есть ли в логах подстрока (хелпер для проверок WARN).
func containsLog(logs []string, sub string) bool {
	for _, l := range logs {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

// TestBuildContextFnNegativeBudgetsFail — отрицательный бюджет это опечатка в
// конфиге, а не «без лимита»: старт должен падать.
func TestBuildContextFnNegativeBudgetsFail(t *testing.T) {
	cfg := ccConfig(writeCCDir(t))
	cfg.MaxTokens = -1
	if _, err := BuildContextFn(cfg); err == nil {
		t.Fatal("ожидалась ошибка при отрицательном max_tokens")
	}

	cfg = ccConfig(writeCCDir(t))
	cfg.MaxProfileTokens = -1
	if _, err := BuildContextFn(cfg); err == nil {
		t.Fatal("ожидалась ошибка при отрицательном max_profile_tokens")
	}

	cfg = ccConfig(writeCCDir(t))
	cfg.MaxMatrixTokens = -1
	if _, err := BuildContextFn(cfg); err == nil {
		t.Fatal("ожидалась ошибка при отрицательном max_matrix_tokens")
	}
}

// TestBuildContextFnTruncatedMatrixWarns — M4: обрезка technical_matrix не
// должна быть молчаливой. Матрица рендерится всегда (в т.ч. на вопрос без
// технических терминов) и обрезается со WARN при старте.
func TestBuildContextFnTruncatedMatrixWarns(t *testing.T) {
	dir := writeCCDir(t)
	matrix := strings.Repeat("golang | 2021-2024 | backend\n", 40) // сильно больше 100 токенов
	writeFile(t, filepath.Join(dir, "manifest.json"), fmt.Sprintf(`{
  "profile": "Erik Ivanov",
  "global_aliases": {},
  "sections": [{"id": "experience", "title": "Опыт"}],
  "technical_matrix": %q
}`, matrix))

	var logs []string
	cfg := ccConfig(dir)
	cfg.Logf = func(msg string) { logs = append(logs, msg) }

	fn, err := BuildContextFn(cfg)
	if err != nil {
		t.Fatalf("BuildContextFn: %v", err)
	}
	if !containsLog(logs, "technical_matrix") {
		t.Errorf("ожидался WARN об обрезке technical_matrix, got %v", logs)
	}

	got := fn("привет, как дела", SessionContext{})
	if !strings.Contains(got.Text, "Technical matrix:") {
		t.Errorf("матрица должна рендериться всегда, got %q", got.Text)
	}
	if strings.Contains(got.Text, matrix) {
		t.Errorf("матрица не обрезана по бюджету: %q", got.Text)
	}

	// Короткая матрица проходит целиком и без WARN.
	shortDir := writeCCDir(t)
	writeFile(t, filepath.Join(shortDir, "manifest.json"), `{
  "profile": "Erik Ivanov",
  "global_aliases": {},
  "sections": [{"id": "experience", "title": "Опыт"}],
  "technical_matrix": "golang | 2021-2024"
}`)
	var shortLogs []string
	shortCfg := ccConfig(shortDir)
	shortCfg.Logf = func(msg string) { shortLogs = append(shortLogs, msg) }
	shortFn, err := BuildContextFn(shortCfg)
	if err != nil {
		t.Fatalf("BuildContextFn(short): %v", err)
	}
	if containsLog(shortLogs, "technical_matrix") {
		t.Errorf("короткая матрица не должна давать WARN: %v", shortLogs)
	}
	if out := shortFn("привет", SessionContext{}).Text; !strings.Contains(out, "golang | 2021-2024") {
		t.Errorf("короткая матрица должна попасть в контекст целиком: %q", out)
	}
}

// TestBuildContextFnGolden — M0.1: вывод BuildContextFn побайтно совпадает с
// golden-файлом, снятым ДО рефакторинга (pipeline.resolveCandidateContext +
// resolveHints + appendHint) на 25 вопросах: 23 из frozen legacy set и 2 probe.
func TestBuildContextFnGolden(t *testing.T) {
	cfg := ccConfig(copyFixture(t, filepath.Join("testdata", "ccfn", "cc")))
	cfg.HintsDir = copyFixture(t, filepath.Join("testdata", "ccfn", "hints"))
	cfg.HintsTopK = 1
	cfg.HintsMinScore = 0

	fn, err := BuildContextFn(cfg)
	if err != nil {
		t.Fatalf("BuildContextFn: %v", err)
	}

	questions := goldenQuestions(t)
	if len(questions) != 25 {
		t.Fatalf("ожидалось 25 вопросов, получено %d", len(questions))
	}

	var buf bytes.Buffer
	for i, q := range questions {
		fmt.Fprintf(&buf, "## Q%02d: %s\n", i+1, q)
		buf.WriteString(fn(q, SessionContext{}).Text)
		buf.WriteString("\n--\n")
	}

	goldenPath := filepath.Join("testdata", "ccfn_golden.txt")
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("чтение golden %s: %v", goldenPath, err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		for i, q := range questions {
			got := strings.SplitN(buf.String(), "\n--\n", i+1)[i]
			w := ""
			parts := strings.SplitN(string(want), "\n--\n", i+1)
			if i < len(parts) {
				w = parts[i]
			}
			if got != w {
				t.Fatalf("golden расходится на Q%02d (%s):\n--- got ---\n%s\n--- want ---\n%s", i+1, q, got, w)
			}
		}
		t.Fatal("golden расходится, но различий по вопросам не найдено (хвост файла)")
	}
}

// goldenQuestions — 23 вопроса legacy set (train+holdout) плюс 2 probe-вопроса
// приёмки M7 (тот же список, что использовался при снятии golden).
func goldenQuestions(t *testing.T) []string {
	t.Helper()
	var questions []string
	for _, path := range []string{
		filepath.Join("testdata", "train.json"),
		filepath.Join("testdata", "holdout.json"),
	} {
		ds, err := LoadEvalDataset(path)
		if err != nil {
			t.Fatalf("LoadEvalDataset(%s): %v", path, err)
		}
		for _, q := range ds.Queries {
			questions = append(questions, q.Question)
		}
	}
	questions = append(questions,
		"How did you go about designing the ingestion pipeline?",
		"And how did you do it in PHP, with the billing service?",
	)
	return questions
}
