package candidatecontext

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// buildTree создаёт временный candidate_context: manifest.json + sections/*.md.
func buildTree(t *testing.T, manifest string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sections"), 0o755); err != nil {
		t.Fatalf("mkdir sections: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, "sections", name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

const manifestOneSection = `{
  "profile": "Erik",
  "global_aliases": {},
  "sections": [{"id": "experience", "title": "Опыт"}]
}`

func TestBuildIndexOffsetsAndBodies(t *testing.T) {
	marker1 := "<!-- fact\n{\n  \"id\": \"f1\",\n  \"section\": \"experience\"\n}\n-->"
	body1 := "\nfirst body line\nsecond body line\n"
	marker2 := "<!-- fact\n{\n  \"id\": \"f2\",\n  \"section\": \"experience\"\n}\n-->"
	body2 := "\nlast body\n"
	content := "header ignored\n" + marker1 + body1 + marker2 + body2

	dir := buildTree(t, manifestOneSection, map[string]string{"experience.md": content})
	idx, err := BuildIndex(mustLoadManifest(t, dir), dir)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	if len(idx.Facts) != 2 {
		t.Fatalf("facts: got %d, want 2", len(idx.Facts))
	}

	wantStart1 := int64(len("header ignored\n" + marker1))
	wantEnd1 := wantStart1 + int64(len(body1))
	wantStart2 := wantEnd1 + int64(len(marker2))
	wantEnd2 := int64(len(content))

	f1, f2 := idx.Facts[0], idx.Facts[1]
	if f1.Start != wantStart1 || f1.End != wantEnd1 {
		t.Errorf("f1 offsets: got [%d,%d], want [%d,%d]", f1.Start, f1.End, wantStart1, wantEnd1)
	}
	if f2.Start != wantStart2 || f2.End != wantEnd2 {
		t.Errorf("f2 offsets: got [%d,%d], want [%d,%d]", f2.Start, f2.End, wantStart2, wantEnd2)
	}
	if got := content[f1.Start:f1.End]; got != body1 {
		t.Errorf("f1 body: got %q, want %q", got, body1)
	}
	if got := content[f2.Start:f2.End]; got != body2 {
		t.Errorf("f2 body: got %q, want %q", got, body2)
	}
	// Text before the first marker is outside any fact.
	if got := content[:f1.Start]; !strings.HasPrefix(got, "header ignored\n") {
		t.Errorf("pre-marker text should be excluded, got prefix %q", got)
	}
}

func TestBuildIndexMarkersParsing(t *testing.T) {
	content := `<!-- fact
{
  "id": "java-head-pricing",
  "section": "experience",
  "title": "Pricing Engine & Market Intelligence",
  "keywords": ["pricing", "kafka"],
  "aliases": ["price engine"]
}
-->
pricing body
<!-- fact {"id": "second", "section": "experience", "title": "T2"} -->
second body
`
	dir := buildTree(t, manifestOneSection, map[string]string{"experience.md": content})
	idx, err := BuildIndex(mustLoadManifest(t, dir), dir)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	if len(idx.Facts) != 2 {
		t.Fatalf("facts: got %d, want 2", len(idx.Facts))
	}

	f1 := idx.Facts[0]
	if f1.ID != "java-head-pricing" {
		t.Errorf("ID: got %q", f1.ID)
	}
	if f1.Section != "experience" {
		t.Errorf("Section: got %q", f1.Section)
	}
	if f1.Title != "Pricing Engine & Market Intelligence" {
		t.Errorf("Title: got %q", f1.Title)
	}
	if !reflect.DeepEqual(f1.Keywords, []string{"pricing", "kafka"}) {
		t.Errorf("Keywords: got %v", f1.Keywords)
	}
	if !reflect.DeepEqual(f1.Aliases, []string{"price", "engine"}) {
		t.Errorf("Aliases: got %v", f1.Aliases)
	}
	if f1.File != "sections/experience.md" {
		t.Errorf("File: got %q", f1.File)
	}

	// Single-line marker must parse too.
	if idx.Facts[1].ID != "second" || idx.Facts[1].Title != "T2" {
		t.Errorf("single-line marker fact: got %+v", idx.Facts[1])
	}
}

func TestBuildIndexCanonicalTokens(t *testing.T) {
	manifest := `{
  "profile": "Erik",
  "global_aliases": {"kubernetes": ["k8s"]},
  "sections": [{"id": "experience"}]
}`
	content := `<!-- fact
{
  "id": "f1",
  "section": "experience",
  "keywords": ["k8s", "docker"],
  "aliases": ["k8s cluster"]
}
-->
we run k8s for kubernetes
`
	dir := buildTree(t, manifest, map[string]string{"experience.md": content})
	idx, err := BuildIndex(mustLoadManifest(t, dir), dir)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	f := idx.Facts[0]
	// alias "k8s" → canonical "kubernetes" in keywords.
	if !reflect.DeepEqual(f.Keywords, []string{"kubernetes", "docker"}) {
		t.Errorf("Keywords: got %v", f.Keywords)
	}
	// alias "k8s cluster" → ["kubernetes", "cluster"].
	if !reflect.DeepEqual(f.Aliases, []string{"kubernetes", "cluster"}) {
		t.Errorf("Aliases: got %v", f.Aliases)
	}
	// body: "we"(stopword) "for"(stopword) removed; k8s → kubernetes.
	wantTokens := []string{"run", "kubernetes", "kubernetes"}
	if !reflect.DeepEqual(f.ContentTokens, wantTokens) {
		t.Errorf("ContentTokens: got %v, want %v", f.ContentTokens, wantTokens)
	}
}

func TestBuildIndexUTF8(t *testing.T) {
	content := `<!-- fact
{
  "id": "f1",
  "section": "experience",
  "title": "Заголовок — Пример"
}
-->
Проверка юникода: Café ☕ naïve 中文
`
	dir := buildTree(t, manifestOneSection, map[string]string{"experience.md": content})
	idx, err := BuildIndex(mustLoadManifest(t, dir), dir)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	f := idx.Facts[0]
	body := content[f.Start:f.End]
	if !strings.Contains(body, "Проверка юникода") {
		t.Errorf("body lost cyrillic: %q", body)
	}
	if f.Title != "Заголовок — Пример" {
		t.Errorf("Title: got %q", f.Title)
	}
	// Start/End are byte offsets, so slicing must reproduce the exact bytes.
	if len(f.ContentTokens) == 0 {
		t.Errorf("ContentTokens unexpectedly empty")
	}
}

func TestBuildIndexFileMetaHashAndSize(t *testing.T) {
	content := `<!-- fact
{
  "id": "f1",
  "section": "experience"
}
-->
body
`
	dir := buildTree(t, manifestOneSection, map[string]string{"experience.md": content})
	idx, err := BuildIndex(mustLoadManifest(t, dir), dir)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	if len(idx.Files) != 1 {
		t.Fatalf("files: got %d, want 1", len(idx.Files))
	}
	meta := idx.Files[0]
	if meta.Path != "sections/experience.md" {
		t.Errorf("Path: got %q", meta.Path)
	}
	if meta.Size != int64(len(content)) {
		t.Errorf("Size: got %d, want %d", meta.Size, len(content))
	}
	sum := sha256.Sum256([]byte(content))
	if meta.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("SHA256 mismatch")
	}
}

func TestBuildIndexManifestSHA256(t *testing.T) {
	manifest := manifestOneSection
	dir := buildTree(t, manifest, map[string]string{"experience.md": "<!-- fact\n{\"id\":\"f1\",\"section\":\"experience\"}\n-->\nbody\n"})
	idx, err := BuildIndex(mustLoadManifest(t, dir), dir)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if idx.ManifestSHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("ManifestSHA256 mismatch")
	}
}

func TestBuildIndexWindowsPathSemantics(t *testing.T) {
	dir := buildTree(t, manifestOneSection, map[string]string{"experience.md": "<!-- fact\n{\"id\":\"f1\",\"section\":\"experience\"}\n-->\nbody\n"})
	idx, err := BuildIndex(mustLoadManifest(t, dir), dir)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	for _, f := range idx.Facts {
		if strings.Contains(f.File, "\\") {
			t.Errorf("Fact.File uses backslash: %q", f.File)
		}
		if f.File != "sections/experience.md" {
			t.Errorf("Fact.File: got %q, want sections/experience.md", f.File)
		}
	}
	for _, m := range idx.Files {
		if strings.Contains(m.Path, "\\") {
			t.Errorf("FileMeta.Path uses backslash: %q", m.Path)
		}
	}
}

func TestBuildIndexOrdering(t *testing.T) {
	manifest := `{
  "profile": "Erik",
  "sections": [{"id": "a"}, {"id": "b"}]
}`
	files := map[string]string{
		"a.md": "<!-- fact\n{\"id\":\"a1\",\"section\":\"a\"}\n-->\nA1\n<!-- fact\n{\"id\":\"a2\",\"section\":\"a\"}\n-->\nA2\n",
		"b.md": "<!-- fact\n{\"id\":\"b1\",\"section\":\"b\"}\n-->\nB1\n",
	}
	dir := buildTree(t, manifest, files)
	idx, err := BuildIndex(mustLoadManifest(t, dir), dir)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	want := []string{"a1", "a2", "b1"}
	got := make([]string, 0, len(idx.Facts))
	for _, f := range idx.Facts {
		got = append(got, f.ID)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fact order: got %v, want %v", got, want)
	}
}

func TestBuildIndexVersion(t *testing.T) {
	dir := buildTree(t, manifestOneSection, map[string]string{"experience.md": "<!-- fact\n{\"id\":\"f1\",\"section\":\"experience\"}\n-->\nbody\n"})
	idx, err := BuildIndex(mustLoadManifest(t, dir), dir)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	if idx.Version != IndexVersion {
		t.Errorf("Version: got %d, want %d", idx.Version, IndexVersion)
	}
}

func TestBuildIndexErrors(t *testing.T) {
	tests := []struct {
		name     string
		manifest string
		files    map[string]string
		wantSub  string
	}{
		{
			name:     "missing section file",
			manifest: manifestOneSection,
			files:    map[string]string{},
			wantSub:  "отсутствует source-файл для section",
		},
		{
			name:     "no marker in file",
			manifest: manifestOneSection,
			files:    map[string]string{"experience.md": "no markers here\n"},
			wantSub:  "отсутствуют fact markers",
		},
		{
			name:     "marker without id",
			manifest: manifestOneSection,
			files:    map[string]string{"experience.md": "<!-- fact\n{\"section\":\"experience\"}\n-->\nbody\n"},
			wantSub:  "fact marker без id",
		},
		{
			name:     "marker without section",
			manifest: manifestOneSection,
			files:    map[string]string{"experience.md": "<!-- fact\n{\"id\":\"f1\"}\n-->\nbody\n"},
			wantSub:  "fact marker без section",
		},
		{
			name:     "marker section not in manifest",
			manifest: manifestOneSection,
			files:    map[string]string{"experience.md": "<!-- fact\n{\"id\":\"f1\",\"section\":\"nope\"}\n-->\nbody\n"},
			wantSub:  "section \"nope\" отсутствует в manifest",
		},
		{
			name:     "duplicate fact id",
			manifest: manifestOneSection,
			files:    map[string]string{"experience.md": "<!-- fact\n{\"id\":\"f1\",\"section\":\"experience\"}\n-->\nA\n<!-- fact\n{\"id\":\"f1\",\"section\":\"experience\"}\n-->\nB\n"},
			wantSub:  "дубликат fact id",
		},
		{
			name:     "invalid marker JSON",
			manifest: manifestOneSection,
			files:    map[string]string{"experience.md": "<!-- fact\n{not json}\n-->\nbody\n"},
			wantSub:  "парсинг fact marker",
		},
		{
			name:     "unterminated marker",
			manifest: manifestOneSection,
			files:    map[string]string{"experience.md": "<!-- fact\n{\"id\":\"f1\"}\n"},
			wantSub:  "без закрывающего",
		},
		{
			name:     "invalid alias mapping",
			manifest: `{"profile":"Erik","global_aliases":{"a":[""]},"sections":[{"id":"experience"}]}`,
			files:    map[string]string{"experience.md": "<!-- fact\n{\"id\":\"f1\",\"section\":\"experience\"}\n-->\nbody\n"},
			wantSub:  "build index",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := buildTree(t, tt.manifest, tt.files)
			_, err := BuildIndex(mustLoadManifest(t, dir), dir)
			if err == nil {
				t.Fatal("ожидалась ошибка")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("error %q не содержит %q", err.Error(), tt.wantSub)
			}
		})
	}
}

func TestBuildIndexManifestMissing(t *testing.T) {
	dir := t.TempDir()
	manifest := &Manifest{
		Profile:       "Erik",
		GlobalAliases: map[string][]string{},
		Sections:      []Section{{ID: "experience"}},
	}
	if _, err := BuildIndex(manifest, dir); err == nil {
		t.Fatal("ожидалась ошибка чтения manifest.json")
	}
}

// mustLoadManifest читает manifest.json из dir (для передачи его содержимого в
// BuildIndex так же, как это делает CLI).
func mustLoadManifest(t *testing.T, dir string) *Manifest {
	t.Helper()
	m, err := LoadManifest(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	return m
}

func TestSaveIndexRoundTrip(t *testing.T) {
	dir := t.TempDir()
	index := &Index{
		Version:        IndexVersion,
		ManifestSHA256: "abc",
		Files:          []FileMeta{{Path: "sections/a.md", Size: 3, SHA256: "d"}},
		Facts:          []Fact{{ID: "f1", Section: "s", File: "sections/a.md", Start: 0, End: 3, ContentTokens: []string{"x"}}},
	}
	path := filepath.Join(dir, "index.json")

	if err := SaveIndex(index, path); err != nil {
		t.Fatalf("SaveIndex: %v", err)
	}
	got, err := LoadIndex(path)
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	if !reflect.DeepEqual(got, index) {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", got, index)
	}

	// Повторный SaveIndex перезаписывает (не оставляет temp).
	index.Version = IndexVersion + 1
	if err := SaveIndex(index, path); err != nil {
		t.Fatalf("SaveIndex (overwrite): %v", err)
	}
	got, err = LoadIndex(path)
	if err != nil {
		t.Fatalf("LoadIndex after overwrite: %v", err)
	}
	if got.Version != IndexVersion+1 {
		t.Errorf("overwrite: got version %d", got.Version)
	}

	assertNoTempFiles(t, dir)
}

func TestSaveIndexNoLeftoverOnError(t *testing.T) {
	dir := t.TempDir()
	// Создаём существующий target, чтобы убедиться, что он не тронут.
	existing := filepath.Join(dir, "index.json")
	if err := os.WriteFile(existing, []byte("ORIGINAL"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Директория назначения не существует → CreateTemp падает.
	badPath := filepath.Join(dir, "nope", "index.json")
	if err := SaveIndex(&Index{Version: IndexVersion}, badPath); err == nil {
		t.Fatal("ожидалась ошибка для несуществующей директории")
	}
	// Существующий target не тронут.
	if got, _ := os.ReadFile(existing); string(got) != "ORIGINAL" {
		t.Errorf("existing target modified: %q", got)
	}
	// Нет leftover temp в исходной директории.
	assertNoTempFiles(t, dir)
}

func TestSaveIndexNil(t *testing.T) {
	if err := SaveIndex(nil, filepath.Join(t.TempDir(), "index.json")); err == nil {
		t.Fatal("ожидалась ошибка для nil index")
	}
}

func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".index-") && strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

// fakeTempFile — тестовая реализация tempFile с инъекцией ошибок write/close.
type fakeTempFile struct {
	name     string
	writeErr error
	closeErr error
	written  []byte
}

func (f *fakeTempFile) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	f.written = append(f.written, p...)
	return len(p), nil
}

func (f *fakeTempFile) Close() error { return f.closeErr }

func (f *fakeTempFile) Name() string { return f.name }

func TestBuildIndexDuplicateSectionID(t *testing.T) {
	manifest := `{
  "profile": "Erik",
  "sections": [{"id": "experience"}, {"id": "experience"}]
}`
	dir := buildTree(t, manifest, map[string]string{})
	if _, err := BuildIndex(mustLoadManifest(t, dir), dir); err == nil {
		t.Fatal("ожидалась ошибка при дубликате section id")
	}
}

func TestBuildIndexEmptySectionID(t *testing.T) {
	manifest := `{
  "profile": "Erik",
  "sections": [{"id": ""}]
}`
	dir := buildTree(t, manifest, map[string]string{})
	if _, err := BuildIndex(mustLoadManifest(t, dir), dir); err == nil {
		t.Fatal("ожидалась ошибка при пустом section id")
	}
}

// TestBuildIndexCRLF проверяет корректную разметку фактов в source-файле с
// CRLF (Windows) line endings в теле факта: offsets — байтовые, body
// воспроизводится без marker'а, ContentTokens не содержат "\r", UTF-8 цел.
func TestBuildIndexCRLF(t *testing.T) {
	marker1 := "<!-- fact\n{\n  \"id\": \"f1\",\n  \"section\": \"experience\"\n}\n-->"
	body1 := "\r\nfirst line\r\nsecond line\r\n"
	marker2 := "<!-- fact\n{\n  \"id\": \"f2\",\n  \"section\": \"experience\"\n}\n-->"
	body2 := "\r\nlast line — café ☕\r\n"
	content := "header\r\n" + marker1 + body1 + marker2 + body2

	dir := buildTree(t, manifestOneSection, map[string]string{"experience.md": content})
	idx, err := BuildIndex(mustLoadManifest(t, dir), dir)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	if len(idx.Facts) != 2 {
		t.Fatalf("facts: got %d, want 2", len(idx.Facts))
	}

	// offsets из реальной байтовой длины fixture (CRLF даёт +1 байт на строку).
	wantStart1 := int64(len([]byte("header\r\n" + marker1)))
	wantEnd1 := wantStart1 + int64(len([]byte(body1)))
	wantStart2 := wantEnd1 + int64(len([]byte(marker2)))
	wantEnd2 := int64(len([]byte(content)))

	f1, f2 := idx.Facts[0], idx.Facts[1]
	if f1.Start != wantStart1 || f1.End != wantEnd1 {
		t.Errorf("f1 offsets: got [%d,%d], want [%d,%d]", f1.Start, f1.End, wantStart1, wantEnd1)
	}
	if f2.Start != wantStart2 || f2.End != wantEnd2 {
		t.Errorf("f2 offsets: got [%d,%d], want [%d,%d]", f2.Start, f2.End, wantStart2, wantEnd2)
	}

	// body без marker'а воспроизводится байт-в-байт.
	if got := content[f1.Start:f1.End]; got != body1 {
		t.Errorf("f1 body: got %q, want %q", got, body1)
	}
	if got := content[f2.Start:f2.End]; got != body2 {
		t.Errorf("f2 body: got %q, want %q", got, body2)
	}
	// Граница между двумя фактами — это marker2 (не попадает ни в один body).
	if got := content[f1.End:f2.Start]; got != marker2 {
		t.Errorf("boundary between facts: got %q, want marker2", got)
	}

	// ContentTokens не содержат "\r".
	for _, f := range []Fact{f1, f2} {
		for _, tok := range f.ContentTokens {
			if strings.Contains(tok, "\r") {
				t.Errorf("ContentTokens содержит \\r: %q", tok)
			}
		}
	}

	// UTF-8 в теле не повреждён (многобайтовые символы сохранены).
	if !strings.Contains(body2, "café") || !strings.Contains(body2, "☕") {
		t.Fatalf("fixture потерял UTF-8: %q", body2)
	}
	if got := content[f2.Start:f2.End]; !strings.Contains(got, "café") {
		t.Errorf("body потерял UTF-8: %q", got)
	}
}

func TestSaveIndexSeamWriteFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "index.json")
	if err := os.WriteFile(target, []byte("ORIGINAL"), 0o644); err != nil {
		t.Fatal(err)
	}
	leftover := filepath.Join(dir, ".index-leftover.tmp")
	if err := os.WriteFile(leftover, []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}

	origCreate := createTempFile
	createTempFile = func(d, pattern string) (tempFile, error) {
		return &fakeTempFile{name: leftover, writeErr: errors.New("write boom")}, nil
	}
	t.Cleanup(func() { createTempFile = origCreate })

	if err := SaveIndex(&Index{Version: IndexVersion}, target); err == nil {
		t.Fatal("ожидалась ошибка write")
	}
	if got, _ := os.ReadFile(target); string(got) != "ORIGINAL" {
		t.Errorf("target изменён: %q", got)
	}
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Errorf("leftover temp не удалён (stat err=%v)", err)
	}
}

func TestSaveIndexSeamCloseFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "index.json")
	if err := os.WriteFile(target, []byte("ORIGINAL"), 0o644); err != nil {
		t.Fatal(err)
	}
	leftover := filepath.Join(dir, ".index-leftover.tmp")
	if err := os.WriteFile(leftover, []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}

	origCreate := createTempFile
	createTempFile = func(d, pattern string) (tempFile, error) {
		return &fakeTempFile{name: leftover, closeErr: errors.New("close boom")}, nil
	}
	t.Cleanup(func() { createTempFile = origCreate })

	if err := SaveIndex(&Index{Version: IndexVersion}, target); err == nil {
		t.Fatal("ожидалась ошибка close")
	}
	if got, _ := os.ReadFile(target); string(got) != "ORIGINAL" {
		t.Errorf("target изменён: %q", got)
	}
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Errorf("leftover temp не удалён (stat err=%v)", err)
	}
}

func TestSaveIndexSeamRenameFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "index.json")
	if err := os.WriteFile(target, []byte("ORIGINAL"), 0o644); err != nil {
		t.Fatal(err)
	}

	origRename := renameFile
	renameFile = func(oldpath, newpath string) error { return errors.New("rename boom") }
	t.Cleanup(func() { renameFile = origRename })

	if err := SaveIndex(&Index{Version: IndexVersion}, target); err == nil {
		t.Fatal("ожидалась ошибка rename")
	}
	if got, _ := os.ReadFile(target); string(got) != "ORIGINAL" {
		t.Errorf("target изменён: %q", got)
	}
	assertNoTempFiles(t, dir)
}

func TestSaveIndexSeamSuccessAndRestore(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "index.json")
	index := &Index{
		Version: IndexVersion,
		Facts:   []Fact{{ID: "f1", Section: "s", File: "sections/s.md", ContentTokens: []string{"x"}}},
	}

	// 1) Ломаем rename — сохранение падает.
	origRename := renameFile
	renameFile = func(oldpath, newpath string) error { return errors.New("rename boom") }
	if err := SaveIndex(index, target); err == nil {
		renameFile = origRename
		t.Fatal("ожидалась ошибка rename")
	}
	renameFile = origRename // восстановление seam'а

	// 2) После восстановления повторный SaveIndex работает.
	if err := SaveIndex(index, target); err != nil {
		t.Fatalf("SaveIndex after restore: %v", err)
	}
	got, err := LoadIndex(target)
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	if got.Version != IndexVersion || len(got.Facts) != 1 || got.Facts[0].ID != "f1" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	assertNoTempFiles(t, dir)
}

// TestNestedFactSourcePath проверяет, что fact source в подкаталоге
// sections/<id>/... с путём через "/" в JSON корректно резолвится через
// filepath.Join + filepath.FromSlash и читается.
func TestNestedFactSourcePath(t *testing.T) {
	dir := t.TempDir()
	rel := "sections/experience/deep/nested.md"
	abs := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "pricing engine built on kafka streams"
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	manifest := &Manifest{
		Profile:       "Erik",
		GlobalAliases: map[string][]string{},
		Sections:      []Section{{ID: "experience"}},
	}
	index := &Index{
		Version: IndexVersion,
		Facts: []Fact{{
			ID:       "f1",
			Section:  "experience",
			File:     rel,
			Start:    0,
			End:      int64(len(body)),
			Title:    "Pricing Engine",
			Keywords: []string{"pricing"},
		}},
	}

	// Путь с "/" сохраняется в JSON как есть (не конвертируется в "\\").
	indexPath := filepath.Join(dir, "index.json")
	if err := SaveIndex(index, indexPath); err != nil {
		t.Fatalf("SaveIndex: %v", err)
	}
	raw, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"file": "`+rel+`"`) {
		t.Errorf("JSON не содержит nested path с \"/\": %s", raw)
	}

	loaded, err := LoadIndex(indexPath)
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	if loaded.Facts[0].File != rel {
		t.Errorf("File после round-trip: got %q, want %q", loaded.Facts[0].File, rel)
	}

	// Резолвится через filepath.Join и читается через Retriever.
	r, err := NewRetriever(manifest, loaded, dir, 1, 0)
	if err != nil {
		t.Fatalf("NewRetriever: %v", err)
	}
	results := r.Retrieve("pricing")
	if len(results) != 1 {
		t.Fatalf("results: got %d, want 1", len(results))
	}
	if results[0].Content != body {
		t.Errorf("content: got %q, want %q", results[0].Content, body)
	}
}
