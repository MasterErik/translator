package candidatecontext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mastererik/translator/internal/context/stackdet"
)

// manifestFamilies — manifest с семьями java (kotlin/spring — члены, spring
// неоднозначен) и php (laravel — член).
const manifestFamilies = `{
  "profile": "Erik",
  "global_aliases": {},
  "sections": [{"id": "experience", "title": "Опыт"}],
  "tech_families": [
    {"name": "java", "display": "Java", "members": ["java", "kotlin", "spring"], "ambiguous": ["spring"]},
    {"name": "php", "display": "PHP", "members": ["php", "laravel"]}
  ]
}`

func factMarkerJSON(t *testing.T, body string) string {
	t.Helper()
	return "<!-- fact\n" + body + "\n-->"
}

// TestBuildIndexStackUnknownFamilyFails: стек, не описанный ни в одной семье, —
// ошибка данных, а не молчаливое игнорирование (иначе опечатка в stack
// отключила бы stack-множители для факта).
func TestBuildIndexStackUnknownFamilyFails(t *testing.T) {
	content := factMarkerJSON(t, `{"id": "f1", "section": "experience", "stack": ["rust"]}`) + "\nbody\n"
	dir := buildTree(t, manifestFamilies, map[string]string{"experience.md": content})
	_, err := BuildIndex(mustLoadManifest(t, dir), dir)
	if err == nil {
		t.Fatal("BuildIndex: ожидалась ошибка про стек без семьи")
	}
	if !strings.Contains(err.Error(), "не описан ни в одной семье") {
		t.Fatalf("BuildIndex: неожиданная ошибка: %v", err)
	}
}

// TestBuildIndexStackUnknownFamilyAllowedWithoutFamilies: каталог без
// tech_families и без stack-разметки остаётся валидным (обратная совместимость
// с hints/фикстурами, где семей пока нет).
func TestBuildIndexStackUnknownFamilyAllowedWithoutFamilies(t *testing.T) {
	content := factMarkerJSON(t, `{"id": "f1", "section": "experience"}`) + "\nbody\n"
	dir := buildTree(t, manifestOneSection, map[string]string{"experience.md": content})
	if _, err := BuildIndex(mustLoadManifest(t, dir), dir); err != nil {
		t.Fatalf("BuildIndex без семей и stack: %v", err)
	}
}

// TestBuildIndexStackMemberNormalizedToFamilyName: член семьи в stack
// нормализуется к имени семьи, чтобы стек факта и стек вопроса сравнивались по
// одному ключу ("kotlin" → "java").
func TestBuildIndexStackMemberNormalizedToFamilyName(t *testing.T) {
	content := factMarkerJSON(t, `{"id": "f1", "section": "experience", "stack": ["Kotlin", " spring "]}`) + "\nbody\n"
	dir := buildTree(t, manifestFamilies, map[string]string{"experience.md": content})
	idx, err := BuildIndex(mustLoadManifest(t, dir), dir)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	got := idx.Facts[0].Stack
	if len(got) != 1 || got[0] != "java" {
		t.Errorf("Stack: got %v, want [java] (kotlin/spring — члены java, дубликаты схлопнуты)", got)
	}
}

// TestBuildIndexWeightEnum: weight вне enum — ошибка; пустой weight допустим.
func TestBuildIndexWeightEnum(t *testing.T) {
	bad := factMarkerJSON(t, `{"id": "f1", "section": "experience", "weight": "high"}`) + "\nbody\n"
	dir := buildTree(t, manifestOneSection, map[string]string{"experience.md": bad})
	if _, err := BuildIndex(mustLoadManifest(t, dir), dir); err == nil {
		t.Fatal("weight \"high\": ожидалась ошибка enum")
	} else if !strings.Contains(err.Error(), "не входит в enum") {
		t.Fatalf("неожиданная ошибка: %v", err)
	}

	for _, w := range []string{WeightPrimary, WeightSecondary, WeightBackground} {
		ok := factMarkerJSON(t, `{"id": "f1", "section": "experience", "weight": "`+w+`"}`) + "\nbody\n"
		dir := buildTree(t, manifestOneSection, map[string]string{"experience.md": ok})
		if _, err := BuildIndex(mustLoadManifest(t, dir), dir); err != nil {
			t.Errorf("weight %q: %v", w, err)
		}
	}
}

// TestBuildIndexPeriodFormat: period проверяется по regex; пустой допустим.
func TestBuildIndexPeriodFormat(t *testing.T) {
	for _, tc := range []struct {
		period string
		ok     bool
	}{
		{"2024", true},
		{"2019-2022", true},
		{"2026-present", true},
		{"", true},
		{"2019-22", false},
		{"с 2019", false},
		{"2019 - 2022", false},
	} {
		marker := `{"id": "f1", "section": "experience"`
		if tc.period != "" {
			marker += `, "period": "` + tc.period + `"`
		}
		marker += "}"
		dir := buildTree(t, manifestOneSection, map[string]string{"experience.md": factMarkerJSON(t, marker) + "\nbody\n"})
		_, err := BuildIndex(mustLoadManifest(t, dir), dir)
		if tc.ok && err != nil {
			t.Errorf("period %q: неожиданная ошибка %v", tc.period, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("period %q: ожидалась ошибка формата", tc.period)
		}
	}
}

// TestBuildIndexLintStacklessFamilyToken: токен семейства в тексте факта при
// пустом stack — предупреждение (не ошибка); lint_ignore подавляет его.
func TestBuildIndexLintStacklessFamilyToken(t *testing.T) {
	content := factMarkerJSON(t, `{"id": "no-stack", "section": "experience", "title": "PHP legacy"}`) +
		"\nПереписал сервис с PHP на Java, попутно тронул laravel-легаси.\n" +
		factMarkerJSON(t, `{"id": "suppressed", "section": "experience", "title": "Kotlin service", "lint_ignore": "осознанное исключение"}`) +
		"\nРазработка на Kotlin.\n" +
		factMarkerJSON(t, `{"id": "marked", "section": "experience", "stack": ["java"]}`) + "\nJava.\n"

	dir := buildTree(t, manifestFamilies, map[string]string{"experience.md": content})
	idx, warns, err := BuildIndexWithLint(mustLoadManifest(t, dir), dir)
	if err != nil {
		t.Fatalf("BuildIndexWithLint: %v", err)
	}
	if len(idx.Facts) != 3 {
		t.Fatalf("facts: got %d, want 3", len(idx.Facts))
	}
	if len(warns) != 1 {
		t.Fatalf("warnings: got %d (%v), want 1 (только no-stack)", len(warns), warns)
	}
	if warns[0].FactID != "no-stack" {
		t.Errorf("warning.FactID: got %q, want no-stack", warns[0].FactID)
	}
	if warns[0].File != "sections/experience.md" {
		t.Errorf("warning.File: got %q", warns[0].File)
	}
	for _, tok := range []string{"php", "laravel"} {
		if !strings.Contains(warns[0].Message, tok) {
			t.Errorf("warning.Message %q не содержит токен %q", warns[0].Message, tok)
		}
	}
}

// TestBuildIndexMarkerRejectsUnknownField: опечатка в поле маркера — ошибка
// (DisallowUnknownFields), иначе "weights" молча терялся бы.
func TestBuildIndexMarkerRejectsUnknownField(t *testing.T) {
	content := factMarkerJSON(t, `{"id": "f1", "section": "experience", "weights": "primary"}`) + "\nbody\n"
	dir := buildTree(t, manifestOneSection, map[string]string{"experience.md": content})
	_, err := BuildIndex(mustLoadManifest(t, dir), dir)
	if err == nil {
		t.Fatal("неизвестное поле маркера: ожидалась ошибка")
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
}

// TestLoadManifestRejectsUnknownField: то же для manifest.json.
func TestLoadManifestRejectsUnknownField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	body := `{"profile": "Erik", "sections": [], "tech_familites": []}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	_, err := LoadManifest(path)
	if err == nil {
		t.Fatal("опечатка tech_familites: ожидалась ошибка")
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
}

// TestNewFamilyIndexValidation: пустые name/члены и токен, заявленный в двух
// семьях, — ошибка таблицы семей (иначе резолвер молча выбрал бы одну из них).
func TestNewFamilyIndexValidation(t *testing.T) {
	tests := []struct {
		name      string
		families  []stackdet.Family
		wantInErr string
	}{
		{
			name:      "пустой name",
			families:  []stackdet.Family{{Name: " ", Members: []string{"x"}}},
			wantInErr: "пустым name",
		},
		{
			name: "дубликат семьи",
			families: []stackdet.Family{
				{Name: "java", Members: []string{"java"}},
				{Name: "java", Members: []string{"jvm"}},
			},
			wantInErr: "дубликат семьи",
		},
		{
			name: "пустой член",
			families: []stackdet.Family{
				{Name: "java", Members: []string{"java", ""}},
			},
			wantInErr: "пустой член семьи",
		},
		{
			name: "токен в двух семьях",
			families: []stackdet.Family{
				{Name: "java", Members: []string{"java", "spring"}},
				{Name: "php", Members: []string{"spring"}},
			},
			wantInErr: "заявлен в семьях",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newFamilyIndex(tc.families)
			if err == nil {
				t.Fatalf("ожидалась ошибка (%s)", tc.wantInErr)
			}
			if !strings.Contains(err.Error(), tc.wantInErr) {
				t.Fatalf("err = %v, want содержит %q", err, tc.wantInErr)
			}
		})
	}
}

// TestFamilyIndexResolve: имя семьи, член и неоднозначный член резолвятся в
// имя семьи; посторонний токен — нет.
func TestFamilyIndexResolve(t *testing.T) {
	fi, err := newFamilyIndex([]stackdet.Family{
		{Name: "java", Display: "Java", Members: []string{"java", "kotlin"}, Ambiguous: []string{"spring"}},
		{Name: "golang", Display: "Go", Members: []string{"golang"}},
	})
	if err != nil {
		t.Fatalf("newFamilyIndex: %v", err)
	}
	for term, want := range map[string]string{"java": "java", "kotlin": "java", "spring": "java", "golang": "golang"} {
		got, ok := fi.resolve(term)
		if !ok || got != want {
			t.Errorf("resolve(%q) = (%q, %v), want (%q, true)", term, got, ok, want)
		}
	}
	if _, ok := fi.resolve("go"); ok {
		t.Error("resolve(\"go\"): стоп-слово не должно резолвиться")
	}
}

// TestLoadCandidateContextSurvivesLint: lint-предупреждения не мешают загрузке —
// каталог с неполной разметкой собирается и сохраняется.
func TestLoadCandidateContextSurvivesLint(t *testing.T) {
	content := factMarkerJSON(t, `{"id": "no-stack", "section": "experience"}`) + "\nPHP и Java в одном проекте.\n"
	dir := buildTree(t, manifestFamilies, map[string]string{"experience.md": content})
	_, idx, err := LoadCandidateContext(dir)
	if err != nil {
		t.Fatalf("LoadCandidateContext: %v", err)
	}
	if len(idx.Facts) != 1 {
		t.Fatalf("facts: got %d, want 1", len(idx.Facts))
	}
	if _, err := os.Stat(filepath.Join(dir, "index.json")); err != nil {
		t.Fatalf("index.json не сохранён: %v", err)
	}
}
