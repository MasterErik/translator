package pipeline

// M10: индикатор «hints отключены». Пакет pipeline обязан явно сообщить, что
// подсказки сконфигурированы, но недоступны: строка в логе сессии, WARN в slog
// и поле Pipeline.hintsDisabledReason. Молчаливое отсутствие подсказок выглядит
// как «подсказок нет» — оператор не отличит сбой каталога от штатной работы.

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mastererik/translator/internal/common"
	candidatecontext "github.com/mastererik/translator/internal/context"
)

// -------------------------------------------------------------------------
// Фикстуры каталогов candidate_context / hints
// -------------------------------------------------------------------------

// writeTestFile пишет файл, создавая родительские каталоги.
func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// newCCDir создаёт валидный каталог fact-level candidate context.
func newCCDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "manifest.json"), `{
  "profile": "Erik Ivanov",
  "global_aliases": {},
  "sections": [{"id": "experience", "title": "Опыт"}]
}`)
	writeTestFile(t, filepath.Join(dir, "sections", "experience.md"), `<!-- fact
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

// newHintsDir создаёт валидный каталог hints (формат candidate_context).
func newHintsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "manifest.json"), `{
  "profile": "Hints",
  "global_aliases": {},
  "sections": [{"id": "answers", "title": "Ответы"}]
}`)
	writeTestFile(t, filepath.Join(dir, "sections", "answers.md"), `<!-- fact
{
  "id": "hint-weaknesses",
  "section": "answers",
  "title": "Weaknesses",
  "keywords": ["weakness", "weaknesses"]
}
-->
My main weakness is perfectionism: I prefer to verify assumptions early.
`)
	return dir
}

// newBrokenHintsDir создаёт hints-каталог, падающий на загрузке: manifest
// ссылается на секцию без source-файла (сборка индекса невозможна).
func newBrokenHintsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "manifest.json"),
		`{"profile": "Hints", "sections": [{"id": "answers"}]}`)
	return dir
}

// recordingSessionLogger — SessionLogger, запоминающий LogDebug-строки.
type recordingSessionLogger struct {
	mu     sync.Mutex
	lines  []string
	closed bool
}

func (m *recordingSessionLogger) LogText(common.STTEvent) error { return nil }
func (m *recordingSessionLogger) LogTranslation(common.STTEvent, string, []string) error {
	return nil
}
func (m *recordingSessionLogger) SaveAudioChunk(string, []byte) error { return nil }
func (m *recordingSessionLogger) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}
func (m *recordingSessionLogger) LogDebug(msg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lines = append(m.lines, msg)
	return nil
}

// find возвращает первую строку, содержащую substr.
func (m *recordingSessionLogger) find(substr string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, l := range m.lines {
		if strings.Contains(l, substr) {
			return l, true
		}
	}
	return "", false
}

// captureSlog перенаправляет дефолтный slog-хендлер в буфер на время теста.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// -------------------------------------------------------------------------
// Хелперы: Config и BuildContextFn — тот же вход, что у New
// -------------------------------------------------------------------------

// pipelineConfigWithHints — минимальный валидный профиль Config (все
// обязательные поля заданы), каталоги подставляются аргументами.
func pipelineConfigWithHints(t *testing.T, ccDir, hintsDir, legacyFile string) Config {
	t.Helper()
	commonCfg, err := common.LoadConfigFromYAML(filepath.Join("..", "..", "config.yaml"))
	if err != nil {
		t.Fatalf("LoadConfigFromYAML: %v", err)
	}
	return Config{
		LLMBaseURL:           commonCfg.LLMBaseURL,
		LLMModel:             commonCfg.LLMModel,
		CandidateContext:     common.CandidateContextConfig{Dir: ccDir, TopK: 5},
		CandidateContextFile: legacyFile,
		Hints:                common.HintsConfig{Dir: hintsDir, TopK: 3},
		LLMAPIKey:            dummyLLMKey,
	}
}

// dummyLLMKey — непустой ключ-заглушка: New проверяет только непустоту и в
// сеть не ходит, поэтому фиктивное значение безопасно и не похоже на секрет.
const dummyLLMKey = "not-a-real-key-for-tests"

// buildContextFnForTest повторяет вход BuildContextFn из New (те же поля и
// nil-safe Logf), но без создания STT/LLM/overlay.
func buildContextFnForTest(t *testing.T, cfg Config) func(string, candidatecontext.SessionContext) candidatecontext.ContextResult {
	t.Helper()
	fn, err := candidatecontext.BuildContextFn(candidatecontext.ContextFnConfig{
		Dir:                 cfg.CandidateContext.Dir,
		TopK:                cfg.CandidateContext.TopK,
		MinScore:            cfg.CandidateContext.MinScore,
		MaxTokens:           cfg.CandidateContext.MaxTokens,
		MaxProfileTokens:    cfg.CandidateContext.MaxProfileTokens,
		MaxMatrixTokens:     cfg.CandidateContext.MaxMatrixTokens,
		LegacyFile:          cfg.CandidateContextFile,
		AllowLegacyFallback: cfg.CandidateContext.AllowLegacyFallback,
		HintsDir:            cfg.Hints.Dir,
		HintsTopK:           cfg.Hints.TopK,
		HintsMinScore:       cfg.Hints.MinScore,
		OnLoadError:         cfg.CandidateContext.OnLoadError,
		Logf:                func(string) {},
	})
	if err != nil {
		t.Fatalf("BuildContextFn: %v", err)
	}
	return fn
}

// -------------------------------------------------------------------------
// Юнит-тесты индикатора
// -------------------------------------------------------------------------

// TestDetectHintsDisabled — table-driven: индикатор выставляется только когда
// hints сконфигурирован, но по итогу недоступен.
func TestDetectHintsDisabled(t *testing.T) {
	ccDir := newCCDir(t)
	validHints := newHintsDir(t)
	brokenHints := newBrokenHintsDir(t)
	legacyFile := filepath.Join(t.TempDir(), "cv.md")
	writeTestFile(t, legacyFile, "# CV\nsome facts\n")
	missingHints := filepath.Join(t.TempDir(), "нет-такого-каталога")

	tests := []struct {
		name       string
		hintsDir   string
		candDir    string
		legacyFile string
		wantReason string // подстрока; "" — индикатор не выставляется
	}{
		{
			name:       "hints не сконфигурирован",
			candDir:    ccDir,
			wantReason: "",
		},
		{
			name:       "hints валиден",
			hintsDir:   validHints,
			candDir:    ccDir,
			wantReason: "",
		},
		{
			name:       "каталог hints без source-файла",
			hintsDir:   brokenHints,
			candDir:    ccDir,
			wantReason: "недоступен каталог",
		},
		{
			name:       "каталог hints не существует",
			hintsDir:   missingHints,
			candDir:    ccDir,
			wantReason: "недоступен каталог",
		},
		{
			name:       "legacy-режим: retrieval по вопросу недоступен",
			hintsDir:   validHints,
			candDir:    "",
			legacyFile: legacyFile,
			wantReason: "legacy-режим",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var legacy string
			if tt.candDir == "" {
				legacy = tt.legacyFile
			}
			cfg := pipelineConfigWithHints(t, tt.candDir, tt.hintsDir, legacy)
			fn := buildContextFnForTest(t, cfg)

			got := detectHintsDisabled(cfg, fn)
			if tt.wantReason == "" {
				if got != "" {
					t.Fatalf("индикатор не должен быть выставлен, причина: %q", got)
				}
				return
			}
			if !strings.Contains(got, tt.wantReason) {
				t.Fatalf("причина = %q, ожидалась подстрока %q", got, tt.wantReason)
			}
			// Индикатор обязан согласовываться с флагом ContextResult.
			if !fn("weaknesses", candidatecontext.SessionContext{}).HintsDisabled {
				t.Fatal("ContextResult.HintsDisabled должно быть true при выставленном индикаторе")
			}
		})
	}
}

// TestReportHintsDisabled — все три выхода индикатора получают причину.
func TestReportHintsDisabled(t *testing.T) {
	sessLog := &recordingSessionLogger{}
	buf := captureSlog(t)

	var notified []string
	reportHintsDisabled(sessLog, func(reason string) { notified = append(notified, reason) },
		"недоступен каталог /tmp/hints")

	line, ok := sessLog.find("hints отключены")
	if !ok {
		t.Fatalf("в логе сессии нет строки индикатора; строки: %v", sessLog.lines)
	}
	if !strings.Contains(line, "недоступен каталог /tmp/hints") ||
		!strings.Contains(line, "интервью продолжается без подсказок") {
		t.Fatalf("строка индикатора неполная: %q", line)
	}

	logged := buf.String()
	if !strings.Contains(logged, "level=WARN") ||
		!strings.Contains(logged, "недоступен каталог /tmp/hints") {
		t.Fatalf("slog WARN с причиной отсутствует: %q", logged)
	}

	if len(notified) != 1 || notified[0] != "недоступен каталог /tmp/hints" {
		t.Fatalf("notifier получил %v, ожидалась одна причина", notified)
	}
}

// TestReportHintsDisabled_NilSafe — sessLog/notifier могут отсутствовать
// (Pipeline, собранный в обход New) — паники быть не должно.
func TestReportHintsDisabled_NilSafe(t *testing.T) {
	captureSlog(t)
	reportHintsDisabled(nil, nil, "причина")
}

// TestPipelineNew_HintsDisabledIndicator — сквозной путь через New: невалидный
// каталог hints не валит старт, но индикатор выставлен, а WARN уходит в slog.
func TestPipelineNew_HintsDisabledIndicator(t *testing.T) {
	buf := captureSlog(t)
	var notified []string

	cfg := pipelineConfigWithHints(t, newCCDir(t), newBrokenHintsDir(t), "")
	cfg.LogDir = t.TempDir() // файловый лог сессии: строка видна оператору
	cfg.HintsDisabledNotifier = func(reason string) { notified = append(notified, reason) }

	p, err := New(cfg)
	if err != nil {
		t.Fatalf("New() не должен падать при недоступном hints: %v", err)
	}
	defer func() { _ = p.sessLog.Close() }()

	if p.hintsDisabledReason == "" {
		t.Fatal("hintsDisabledReason пуст — индикатор не выставлен")
	}
	if !strings.Contains(buf.String(), "level=WARN") ||
		!strings.Contains(buf.String(), "hints отключены") {
		t.Fatalf("slog WARN об отключённых hints отсутствует: %q", buf.String())
	}
	if len(notified) != 1 {
		t.Fatalf("notifier вызван %d раз, ожидался 1", len(notified))
	}
}

// TestPipelineNew_HintsEnabledNoIndicator — успешная сборка: индикатор не
// выставлен, строк об отключении нет.
func TestPipelineNew_HintsEnabledNoIndicator(t *testing.T) {
	buf := captureSlog(t)
	notified := 0

	cfg := pipelineConfigWithHints(t, newCCDir(t), newHintsDir(t), "")
	cfg.HintsDisabledNotifier = func(string) { notified++ }

	p, err := New(cfg)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	defer func() { _ = p.sessLog.Close() }()

	if p.hintsDisabledReason != "" {
		t.Fatalf("hintsDisabledReason = %q, ожидалась пустая строка", p.hintsDisabledReason)
	}
	if strings.Contains(buf.String(), "hints отключены") {
		t.Fatalf("slog содержит индикатор при валидном hints: %q", buf.String())
	}
	if notified != 0 {
		t.Fatalf("notifier вызван %d раз при рабочем hints", notified)
	}
}

// TestPipelineNew_HintsNotConfiguredNoIndicator — Dir="" (подсказки не нужны):
// индикатора нет.
func TestPipelineNew_HintsNotConfiguredNoIndicator(t *testing.T) {
	buf := captureSlog(t)

	cfg := pipelineConfigWithHints(t, newCCDir(t), "", "")

	p, err := New(cfg)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	defer func() { _ = p.sessLog.Close() }()

	if p.hintsDisabledReason != "" {
		t.Fatalf("индикатор выставлен без конфигурации hints: %q", p.hintsDisabledReason)
	}
	if strings.Contains(buf.String(), "hints отключены") {
		t.Fatal("строка индикатора появилась без конфигурации hints")
	}
}
