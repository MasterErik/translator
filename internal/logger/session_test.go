package logger_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mastererik/translator/internal/common"
	"github.com/mastererik/translator/internal/logger"
)

// newTempLogger creates a FileSessionLogger rooted in a temporary directory.
// The caller is responsible for cleaning up the directory.
func newTempLogger(t *testing.T) (*logger.FileSessionLogger, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "translator-logger-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	l, err := logger.NewFileSessionLogger(dir)
	if err != nil {
		t.Fatalf("NewFileSessionLogger(%q): %v", dir, err)
	}
	return l, dir
}

// TestNewFileSessionLogger_DirectorySetup verifies that NewFileSessionLogger
// creates the log directory, a session_*.json file, and an audio/ subdirectory.
func TestNewFileSessionLogger_DirectorySetup(t *testing.T) {
	dir, err := os.MkdirTemp("", "translator-logger-dir-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(dir)

	l, err := logger.NewFileSessionLogger(dir)
	if err != nil {
		t.Fatalf("NewFileSessionLogger: %v", err)
	}
	defer l.Close()

	// Check that the session JSON file exists and matches the expected naming pattern.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", dir, err)
	}

	var jsonFound bool
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "session_") && strings.HasSuffix(e.Name(), ".json") {
			jsonFound = true
			break
		}
	}
	if !jsonFound {
		t.Errorf("expected session_*.json file in %q, got: %v", dir, entryNames(entries))
	}

	// Check that the audio/ subdirectory exists.
	audioDir := filepath.Join(dir, "audio")
	if info, err := os.Stat(audioDir); err != nil || !info.IsDir() {
		t.Errorf("expected audio/ subdirectory at %q, stat: %v", audioDir, err)
	}
}

// TestLogText_PersistsJSON verifies that LogText writes a JSON line to the
// session file containing the expected fields.
func TestLogText_PersistsJSON(t *testing.T) {
	l, dir := newTempLogger(t)

	ts := time.Date(2026, 7, 21, 12, 0, 0, 123456789, time.UTC)
	event := common.STTEvent{
		Text:      "hello world",
		IsFinal:   true,
		ChannelID: "speaker",
		Timestamp: ts,
	}

	if err := l.LogText(event); err != nil {
		t.Fatalf("LogText: %v", err)
	}

	// Close to flush and drain the write queue.
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Find the session JSON file.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	var jsonPath string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "session_") && strings.HasSuffix(e.Name(), ".json") {
			jsonPath = filepath.Join(dir, e.Name())
			break
		}
	}
	if jsonPath == "" {
		t.Fatal("no session JSON file found")
	}

	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Each log entry is a JSON line.
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 JSON line, got %d: %q", len(lines), string(data))
	}

	var entry logger.LogEntry
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	if entry.Text != "hello world" {
		t.Errorf("Text = %q, want %q", entry.Text, "hello world")
	}
	if !entry.IsFinal {
		t.Error("IsFinal = false, want true")
	}
	if entry.ChannelID != "speaker" {
		t.Errorf("ChannelID = %q, want %q", entry.ChannelID, "speaker")
	}
	if entry.Timestamp != "2026-07-21T12:00:00.123456789Z" {
		t.Errorf("Timestamp = %q", entry.Timestamp)
	}
}

// TestSaveAudioChunk_CreatesPCMFile verifies that SaveAudioChunk creates the
// per-channel PCM file and appends raw audio data.
func TestSaveAudioChunk_CreatesPCMFile(t *testing.T) {
	l, dir := newTempLogger(t)

	pcmData := []byte{0x01, 0x02, 0x03, 0x04, 0x05}
	if err := l.SaveAudioChunk("speaker", pcmData); err != nil {
		t.Fatalf("SaveAudioChunk (first): %v", err)
	}

	moreData := []byte{0x06, 0x07, 0x08}
	if err := l.SaveAudioChunk("speaker", moreData); err != nil {
		t.Fatalf("SaveAudioChunk (second): %v", err)
	}

	// Close to flush.
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Verify the PCM file exists and has the expected contents.
	pcmPath := filepath.Join(dir, "audio", "speaker.pcm")
	data, err := os.ReadFile(pcmPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", pcmPath, err)
	}

	expected := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	if len(data) != len(expected) {
		t.Fatalf("PCM file length = %d, want %d", len(data), len(expected))
	}
	for i, b := range data {
		if b != expected[i] {
			t.Errorf("PCM byte[%d] = 0x%02x, want 0x%02x", i, b, expected[i])
		}
	}
}

// TestSaveAudioChunk_MultipleChannels verifies that separate channelIDs
// produce separate PCM files.
func TestSaveAudioChunk_MultipleChannels(t *testing.T) {
	l, dir := newTempLogger(t)

	if err := l.SaveAudioChunk("speaker", []byte{0xAA}); err != nil {
		t.Fatalf("SaveAudioChunk(speaker): %v", err)
	}
	if err := l.SaveAudioChunk("mic", []byte{0xBB}); err != nil {
		t.Fatalf("SaveAudioChunk(mic): %v", err)
	}

	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Both files should exist.
	for _, ch := range []string{"speaker", "mic"} {
		pcmPath := filepath.Join(dir, "audio", ch+".pcm")
		if _, err := os.Stat(pcmPath); err != nil {
			t.Errorf("expected PCM file for %s: %v", ch, err)
		}
	}
}

// TestClose_DoubleClose verifies that calling Close twice does not panic.
func TestClose_DoubleClose(t *testing.T) {
	l, _ := newTempLogger(t)

	if err := l.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	// Reaching here without panic = pass.
}

// TestLogText_AfterCloseReturnsError verifies that LogText returns an error
// after the logger has been closed.
func TestLogText_AfterCloseReturnsError(t *testing.T) {
	l, _ := newTempLogger(t)

	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	err := l.LogText(common.STTEvent{Text: "should fail"})
	if err == nil {
		t.Error("expected error from LogText after Close, got nil")
	}
}

// TestSaveAudioChunk_AfterCloseReturnsError verifies that SaveAudioChunk
// returns an error after the logger has been closed.
func TestSaveAudioChunk_AfterCloseReturnsError(t *testing.T) {
	l, _ := newTempLogger(t)

	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	err := l.SaveAudioChunk("speaker", []byte{1, 2, 3})
	if err == nil {
		t.Error("expected error from SaveAudioChunk after Close, got nil")
	}
}

// entryNames returns a list of entry names from a DirEntry slice (for error messages).
func entryNames(entries []os.DirEntry) []string {
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	return names
}
