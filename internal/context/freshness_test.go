package candidatecontext

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const freshManifest = `{
  "profile": "Erik",
  "global_aliases": {"kubernetes": ["k8s"]},
  "sections": [{"id": "experience", "title": "Опыт"}]
}`

const freshSection = "<!-- fact\n{\"id\":\"f1\",\"section\":\"experience\",\"title\":\"T1\"}\n-->\nbody one\n"

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// setupTree создаёт candidate_context (manifest.json + sections/*.md), собирает
// и сохраняет свежий index.json.
func setupTree(t *testing.T) (dir string, manifest *Manifest, index *Index) {
	t.Helper()
	dir = t.TempDir()
	writeFile(t, filepath.Join(dir, "manifest.json"), freshManifest)
	writeFile(t, filepath.Join(dir, "sections", "experience.md"), freshSection)
	manifest = mustLoadManifest(t, dir)
	var err error
	index, err = BuildIndex(manifest, dir)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	if err := SaveIndex(index, filepath.Join(dir, "index.json")); err != nil {
		t.Fatalf("SaveIndex: %v", err)
	}
	return dir, manifest, index
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestCheckIndexFreshFresh(t *testing.T) {
	dir, manifest, index := setupTree(t)
	fresh, reason := CheckIndexFresh(manifest, index, dir)
	if !fresh || reason != "" {
		t.Fatalf("CheckIndexFresh: got (%v, %q), want (true, \"\")", fresh, reason)
	}
}

func TestCheckIndexFreshNilIndex(t *testing.T) {
	_, manifest, _ := setupTree(t)
	fresh, reason := CheckIndexFresh(manifest, nil, t.TempDir())
	if fresh || reason != "index отсутствует" {
		t.Fatalf("CheckIndexFresh: got (%v, %q), want (false, \"index отсутствует\")", fresh, reason)
	}
}

func TestCheckIndexFreshIncompatibleVersion(t *testing.T) {
	dir, manifest, index := setupTree(t)
	index.Version = IndexVersion + 1
	fresh, reason := CheckIndexFresh(manifest, index, dir)
	if fresh || reason != "несовместимая версия index" {
		t.Fatalf("CheckIndexFresh: got (%v, %q), want (false, \"несовместимая версия index\")", fresh, reason)
	}
}

func TestCheckIndexFreshManifestChanged(t *testing.T) {
	dir, _, index := setupTree(t)
	// Изменяем profile в manifest.json на диске.
	writeFile(t, filepath.Join(dir, "manifest.json"),
		strings.Replace(freshManifest, `"Erik"`, `"Erik Ivanov"`, 1))
	fresh, reason := CheckIndexFresh(mustLoadManifest(t, dir), index, dir)
	if fresh || reason != "manifest изменён" {
		t.Fatalf("CheckIndexFresh: got (%v, %q), want (false, \"manifest изменён\")", fresh, reason)
	}
}

func TestCheckIndexFreshGlobalAliasesChanged(t *testing.T) {
	dir, _, index := setupTree(t)
	// Изменяем global_aliases в manifest.json на диске.
	writeFile(t, filepath.Join(dir, "manifest.json"),
		strings.Replace(freshManifest, `["k8s"]`, `["k8s", "kube"]`, 1))
	fresh, reason := CheckIndexFresh(mustLoadManifest(t, dir), index, dir)
	if fresh || reason != "manifest изменён" {
		t.Fatalf("CheckIndexFresh: got (%v, %q), want (false, \"manifest изменён\")", fresh, reason)
	}
}

func TestCheckIndexFreshManifestUnreadable(t *testing.T) {
	// index валиден по версии, но manifest.json отсутствует.
	index := &Index{Version: IndexVersion, ManifestSHA256: "deadbeef"}
	fresh, reason := CheckIndexFresh(&Manifest{}, index, t.TempDir())
	if fresh || reason != "не удалось прочитать manifest.json" {
		t.Fatalf("CheckIndexFresh: got (%v, %q), want (false, \"не удалось прочитать manifest.json\")", fresh, reason)
	}
}

func TestCheckIndexFreshSourceChanged(t *testing.T) {
	dir, manifest, index := setupTree(t)
	writeFile(t, filepath.Join(dir, "sections", "experience.md"),
		"<!-- fact\n{\"id\":\"f1\",\"section\":\"experience\"}\n-->\nchanged body\n")
	fresh, reason := CheckIndexFresh(manifest, index, dir)
	if fresh || reason != "source-файл sections/experience.md изменён" {
		t.Fatalf("CheckIndexFresh: got (%v, %q), want (false, \"source-файл sections/experience.md изменён\")", fresh, reason)
	}
}

func TestCheckIndexFreshMissingSourceFile(t *testing.T) {
	dir, manifest, index := setupTree(t)
	if err := os.Remove(filepath.Join(dir, "sections", "experience.md")); err != nil {
		t.Fatal(err)
	}
	fresh, reason := CheckIndexFresh(manifest, index, dir)
	if fresh || reason != "отсутствует source-файл sections/experience.md" {
		t.Fatalf("CheckIndexFresh: got (%v, %q), want (false, \"отсутствует source-файл sections/experience.md\")", fresh, reason)
	}
}

func TestCheckIndexFreshNewSection(t *testing.T) {
	dir, manifest, index := setupTree(t)
	// Добавляем section в manifest (in-memory), которой нет в index.Files.
	extended := &Manifest{
		Profile:       manifest.Profile,
		GlobalAliases: manifest.GlobalAliases,
		Sections:      append(append([]Section{}, manifest.Sections...), Section{ID: "skills"}),
	}
	fresh, reason := CheckIndexFresh(extended, index, dir)
	if fresh || reason != "новый source-файл для section skills" {
		t.Fatalf("CheckIndexFresh: got (%v, %q), want (false, \"новый source-файл для section skills\")", fresh, reason)
	}
}

func TestCheckIndexFreshIgnoresExtraFileMeta(t *testing.T) {
	dir, manifest, index := setupTree(t)
	// Лишний FileMeta (нет соответствующей section) не делает index stale.
	index.Files = append(index.Files, FileMeta{Path: "sections/ghost.md", Size: 1, SHA256: "x"})
	fresh, reason := CheckIndexFresh(manifest, index, dir)
	if !fresh || reason != "" {
		t.Fatalf("CheckIndexFresh: got (%v, %q), want (true, \"\")", fresh, reason)
	}
}

func TestLoadCandidateContextFirstRun(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "manifest.json"), freshManifest)
	writeFile(t, filepath.Join(dir, "sections", "experience.md"), freshSection)

	manifest, index, err := LoadCandidateContext(dir)
	if err != nil {
		t.Fatalf("LoadCandidateContext: %v", err)
	}
	if manifest == nil || index == nil {
		t.Fatal("LoadCandidateContext: ожидались оба значения")
	}
	if manifest.Profile != "Erik" {
		t.Errorf("manifest.Profile: got %q", manifest.Profile)
	}
	if len(index.Facts) == 0 {
		t.Error("index.Facts пуст")
	}
	// index.json должен быть создан на диске.
	if _, err := os.Stat(filepath.Join(dir, "index.json")); err != nil {
		t.Errorf("index.json не создан: %v", err)
	}
}

func TestLoadCandidateContextFreshReturnsBoth(t *testing.T) {
	dir, _, _ := setupTree(t)
	manifest, index, err := LoadCandidateContext(dir)
	if err != nil {
		t.Fatalf("LoadCandidateContext: %v", err)
	}
	if manifest == nil || manifest.Profile != "Erik" {
		t.Errorf("manifest: got %+v", manifest)
	}
	if index == nil || len(index.Facts) == 0 {
		t.Errorf("index: got %+v", index)
	}
	if index.Version != IndexVersion {
		t.Errorf("index.Version: got %d", index.Version)
	}
}

func TestLoadCandidateContextStaleRebuilds(t *testing.T) {
	dir, _, _ := setupTree(t)

	newContent := "<!-- fact\n{\"id\":\"f1\",\"section\":\"experience\",\"title\":\"T1\"}\n-->\nbrand new zzz body\n"
	writeFile(t, filepath.Join(dir, "sections", "experience.md"), newContent)

	manifest, index, err := LoadCandidateContext(dir)
	if err != nil {
		t.Fatalf("LoadCandidateContext: %v", err)
	}
	if manifest == nil || index == nil {
		t.Fatal("ожидались оба значения")
	}
	// index должен отражать новое содержимое: SHA/size нового файла.
	if got := index.Files[0].SHA256; got != sha256Hex([]byte(newContent)) {
		t.Errorf("Files[0].SHA256: got %q, want %q", got, sha256Hex([]byte(newContent)))
	}
	if got := index.Files[0].Size; got != int64(len(newContent)) {
		t.Errorf("Files[0].Size: got %d, want %d", got, len(newContent))
	}
	if len(index.Facts) == 0 || !slices.Contains(index.Facts[0].ContentTokens, "zzz") {
		t.Errorf("Facts[0].ContentTokens не отражает новое тело: %v", index.Facts)
	}

	// index.json на диске также обновлён.
	onDisk, err := LoadIndex(filepath.Join(dir, "index.json"))
	if err != nil {
		t.Fatalf("LoadIndex after rebuild: %v", err)
	}
	if onDisk.Files[0].SHA256 != sha256Hex([]byte(newContent)) {
		t.Errorf("on-disk index не обновлён: SHA256 %q", onDisk.Files[0].SHA256)
	}
}

func TestLoadCandidateContextInvalidManifest(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "manifest.json"), `{not json`)
	_, _, err := LoadCandidateContext(dir)
	if err == nil {
		t.Fatal("ожидалась ошибка для невалидного manifest")
	}
	if !strings.Contains(err.Error(), "load manifest") {
		t.Errorf("error %q не содержит \"load manifest\"", err.Error())
	}
}

func TestLoadCandidateContextRebuildFails(t *testing.T) {
	dir, _, _ := setupTree(t)
	// Удаляем source-файл после сборки: index stale → пересборка падает.
	if err := os.Remove(filepath.Join(dir, "sections", "experience.md")); err != nil {
		t.Fatal(err)
	}
	_, _, err := LoadCandidateContext(dir)
	if err == nil {
		t.Fatal("ожидалась ошибка пересборки")
	}
	if !strings.Contains(err.Error(), "rebuild index") {
		t.Errorf("error %q не содержит \"rebuild index\"", err.Error())
	}
}
