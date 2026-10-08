//go:build cgo

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFatalLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fatal-test.log")

	f, err := openFatalLog(path)
	if err != nil {
		t.Fatalf("openFatalLog: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })

	const ctxMsg = "не удалось загрузить config.yaml"
	werr := errors.New("open config.yaml: file does not exist")

	if err := writeFatalLog(f, ctxMsg, werr); err != nil {
		t.Fatalf("writeFatalLog: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got := string(data)

	if !strings.Contains(got, ctxMsg) {
		t.Errorf("log missing context %q; got:\n%s", ctxMsg, got)
	}
	if !strings.Contains(got, werr.Error()) {
		t.Errorf("log missing error %q; got:\n%s", werr.Error(), got)
	}
	if !strings.Contains(got, "FATAL") {
		t.Errorf("log missing FATAL marker; got:\n%s", got)
	}
}

func TestWriteFatalLogNilFile(t *testing.T) {
	// nil-файл (сбой открытия) не должен паниковать.
	if err := writeFatalLog(nil, "ctx", errors.New("boom")); err != nil {
		t.Fatalf("nil file should be a no-op, got: %v", err)
	}
}

func TestOpenFatalLogCreatesDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "sub", "fatal.log")

	f, err := openFatalLog(path)
	if err != nil {
		t.Fatalf("openFatalLog: %v", err)
	}
	defer func() { _ = f.Close() }()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file created at %s: %v", path, err)
	}
}
