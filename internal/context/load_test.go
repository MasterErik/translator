package candidatecontext

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

func TestLoadManifestOK(t *testing.T) {
	p := writeTemp(t, "manifest.json", `{
		"profile": "Erik Ivanov",
		"global_aliases": {"kubernetes": ["k8s"]},
		"sections": [{"id": "experience", "title": "Опыт"}]
	}`)
	m, err := LoadManifest(p)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.Profile != "Erik Ivanov" {
		t.Errorf("Profile: got %q, want %q", m.Profile, "Erik Ivanov")
	}
	if m.GlobalAliases["kubernetes"][0] != "k8s" {
		t.Errorf("GlobalAliases: got %v", m.GlobalAliases)
	}
	if len(m.Sections) != 1 || m.Sections[0].ID != "experience" {
		t.Errorf("Sections: got %v", m.Sections)
	}
}

func TestLoadManifestMissing(t *testing.T) {
	if _, err := LoadManifest(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("LoadManifest: ожидалась ошибка для отсутствующего файла")
	}
}

func TestLoadManifestInvalidJSON(t *testing.T) {
	p := writeTemp(t, "manifest.json", `{not json`)
	if _, err := LoadManifest(p); err == nil {
		t.Fatal("LoadManifest: ожидалась ошибка для невалидного JSON")
	}
}

func TestLoadIndexOK(t *testing.T) {
	p := writeTemp(t, "index.json", `{
		"version": 1,
		"manifest_sha256": "abc",
		"files": [{"path": "sections/a.md", "size": 10, "sha256": "d"}],
		"facts": [{"id": "f1", "section": "s", "file": "sections/a.md", "start": 0, "end": 5}]
	}`)
	idx, err := LoadIndex(p)
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	if idx.Version != 1 || idx.ManifestSHA256 != "abc" {
		t.Errorf("header: got %+v", idx)
	}
	if len(idx.Files) != 1 || len(idx.Facts) != 1 {
		t.Errorf("files/facts: got %+v", idx)
	}
	if idx.Facts[0].ID != "f1" {
		t.Errorf("fact id: got %q", idx.Facts[0].ID)
	}
}

func TestLoadIndexMissing(t *testing.T) {
	if _, err := LoadIndex(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("LoadIndex: ожидалась ошибка для отсутствующего файла")
	}
}

func TestIsIndexVersionCompatible(t *testing.T) {
	if IsIndexVersionCompatible(nil) {
		t.Error("nil index должен быть несовместим")
	}
	if !IsIndexVersionCompatible(&Index{Version: IndexVersion}) {
		t.Error("IndexVersion должен быть совместим")
	}
	if IsIndexVersionCompatible(&Index{Version: IndexVersion + 1}) {
		t.Error("новая версия должна быть несовместима")
	}
	if IsIndexVersionCompatible(&Index{Version: 0}) {
		t.Error("version 0 должна быть несовместима")
	}
}
