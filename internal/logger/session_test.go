package logger_test

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mastererik/translator/internal/common"
	"github.com/mastererik/translator/internal/logger"
)

// =========================================================================
// Вспомогательные функции
// =========================================================================

// newTempLogger создаёт FileSessionLogger в временной директории (saveAudio=false).
func newTempLogger(t *testing.T) (*logger.FileSessionLogger, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "translator-logger-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	l, err := logger.NewFileSessionLogger(dir, false)
	if err != nil {
		t.Fatalf("NewFileSessionLogger(%q): %v", dir, err)
	}
	return l, dir
}

// newTestLoggerAudio создаёт логгер с saveAudio=true.
func newTestLoggerAudio(t *testing.T) (*logger.FileSessionLogger, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "translator-logger-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	l, err := logger.NewFileSessionLogger(dir, true)
	if err != nil {
		t.Fatalf("NewFileSessionLogger: %v", err)
	}
	return l, dir
}

// findAudioFile ищет файл с именем, начинающимся на channelID,
// в директории dir. Возвращает полный путь или пустую строку.
func findAudioFile(t *testing.T, dir, channelID string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", dir, err)
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), channelID) && !strings.HasSuffix(e.Name(), ".csv") {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

// generatePCM генерирует срез []byte с PCM-сэмплами (16kHz, mono, 16-bit).
// nSamples — количество сэмплов. Амплитуда 10000 — гарантированно выше порога VAD.
func generatePCM(nSamples int) []byte {
	buf := make([]byte, nSamples*2)
	for i := 0; i < nSamples; i++ {
		sample := int16(float64(10000) * sin(2*3.14159*440*float64(i)/16000.0))
		buf[i*2] = byte(sample)
		buf[i*2+1] = byte(sample >> 8)
	}
	return buf
}

func sin(x float64) float64 {
	twoPi := 2 * 3.141592653589793
	for x > 3.141592653589793 {
		x -= twoPi
	}
	for x < -3.141592653589793 {
		x += twoPi
	}
	x2 := x * x
	return x * (1 - x2*(1.0/6.0-x2*(1.0/120.0-x2/5040.0)))
}

// entryNames возвращает список имён из DirEntry.
func entryNames(entries []os.DirEntry) []string {
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	return names
}

// =========================================================================
// Тесты директорий и базового поведения
// =========================================================================

func TestNewFileSessionLogger_DirectorySetup(t *testing.T) {
	dir, err := os.MkdirTemp("", "translator-logger-dir-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(dir)

	l, err := logger.NewFileSessionLogger(dir, true)
	if err != nil {
		t.Fatalf("NewFileSessionLogger: %v", err)
	}
	defer l.Close()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", dir, err)
	}

	var csvFound bool
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "session_") && strings.HasSuffix(e.Name(), ".csv") {
			csvFound = true
			break
		}
	}
	if !csvFound {
		t.Errorf("expected session_*.csv file in %q, got: %v", dir, entryNames(entries))
	}

	// session_*.mp3/wav НЕ должно существовать до первой записи.
	audioPath := findAudioFile(t, dir, "session_")
	if audioPath != "" {
		t.Errorf("session_*.mp3 should NOT exist before first audio write, found: %s", audioPath)
	}

	// После записи аудио файл должен создаться.
	// Шлём >= speechFrames (3) громких фреймов чтобы VAD-светофор включился.
	loudPCM := generatePCM(1280)
	for i := 0; i < 5; i++ {
		if err := l.SaveAudioChunk("speaker", loudPCM); err != nil {
			t.Fatalf("SaveAudioChunk: %v", err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	audioPath = findAudioFile(t, dir, "session_")
	if audioPath == "" {
		t.Errorf("expected session_*.mp3/wav in %q after audio write", dir)
	}
}

// =========================================================================
// Тесты CSV
// =========================================================================

func TestLogText_PersistsCSV(t *testing.T) {
	l, dir := newTestLoggerAudio(t)

	ts := time.Date(2026, 7, 21, 12, 0, 0, 123456789, time.UTC)
	event := common.STTEvent{
		Text:      "hello world",
		Event:     common.EventEndOfTurn,
		ChannelID: "speaker",
		Timestamp: ts,
	}

	if err := l.LogText(event); err != nil {
		t.Fatalf("LogText: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	var csvPath string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "session_") && strings.HasSuffix(e.Name(), ".csv") {
			csvPath = filepath.Join(dir, e.Name())
			break
		}
	}
	if csvPath == "" {
		t.Fatal("no session CSV file found")
	}

	f, err := os.Open(csvPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if len(rows) < 2 {
		t.Fatalf("expected header + 1 data row, got %d rows", len(rows))
	}

	const (
		colText      = 3
		colChannel   = 1
		colTimestamp = 0
	)

	row := rows[1]
	if row[colText] != "hello world" {
		t.Errorf("Text = %q, want %q", row[colText], "hello world")
	}
	if row[colChannel] != "s" {
		t.Errorf("Channel = %q, want %q", row[colChannel], "s")
	}
	if row[colTimestamp] != "2026-07-21T12:00:00.123" {
		t.Errorf("Timestamp = %q", row[colTimestamp])
	}
}

func TestLogTranslation_PersistsCSV(t *testing.T) {
	l, dir := newTestLoggerAudio(t)

	ts := time.Date(2026, 7, 22, 17, 17, 12, 804000000, time.UTC)
	event := common.STTEvent{
		Text:      "Hello world",
		Event:     common.EventEndOfTurn,
		ChannelID: "speaker",
		Timestamp: ts,
	}

	// Сначала LogText (пустые translation/answers).
	if err := l.LogText(event); err != nil {
		t.Fatalf("LogText: %v", err)
	}
	// Затем LogTranslation (заполняет translation/answers).
	if err := l.LogTranslation(event, "Привет мир", []string{"run containers", "isolate apps"}); err != nil {
		t.Fatalf("LogTranslation: %v", err)
	}

	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	var csvPath string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "session_") && strings.HasSuffix(e.Name(), ".csv") {
			csvPath = filepath.Join(dir, e.Name())
			break
		}
	}
	if csvPath == "" {
		t.Fatal("no session CSV file found")
	}

	f, err := os.Open(csvPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if len(rows) < 3 {
		t.Fatalf("expected header + 2 data rows, got %d rows", len(rows))
	}

	// Первая строка: LogText (пустые translation/answers).
	row1 := rows[1]
	if row1[4] != "" || row1[5] != "" {
		t.Errorf("row1 translation/answers should be empty, got %q / %q", row1[4], row1[5])
	}

	// Вторая строка: LogTranslation (заполнены).
	row2 := rows[2]
	if row2[4] != "Привет мир" {
		t.Errorf("translation = %q, want %q", row2[4], "Привет мир")
	}
	if row2[5] != "run containers;isolate apps" {
		t.Errorf("answers = %q, want %q", row2[5], "run containers;isolate apps")
	}
}

// =========================================================================
// Тесты аудио
// =========================================================================

func TestSaveAudioChunk_CreatesAudioFile(t *testing.T) {
	l, dir := newTestLoggerAudio(t)

	// Шлём >= speechFrames (3) громких фреймов для VAD-светофора.
	loudPCM := generatePCM(1280)
	for i := 0; i < 5; i++ {
		if err := l.SaveAudioChunk("speaker", loudPCM); err != nil {
			t.Fatalf("SaveAudioChunk (frame %d): %v", i, err)
		}
	}

	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	audioPath := findAudioFile(t, dir, "session_")
	if audioPath == "" {
		t.Fatalf("no audio file found for channel 'speaker' in %q", dir)
	}

	info, err := os.Stat(audioPath)
	if err != nil {
		t.Fatalf("Stat(%q): %v", audioPath, err)
	}
	if info.Size() == 0 {
		t.Errorf("audio file %q is empty", audioPath)
	}

	t.Logf("audio file: %s (%d bytes)", filepath.Base(audioPath), info.Size())
}

func TestSaveAudioChunk_MultipleChannels(t *testing.T) {
	l, dir := newTestLoggerAudio(t)

	loudPCM := generatePCM(1280)
	for i := 0; i < 5; i++ {
		if err := l.SaveAudioChunk("speaker", loudPCM); err != nil {
			t.Fatalf("SaveAudioChunk(speaker): %v", err)
		}
	}
	for i := 0; i < 5; i++ {
		if err := l.SaveAudioChunk("mic", loudPCM); err != nil {
			t.Fatalf("SaveAudioChunk(mic): %v", err)
		}
	}

	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	for _, ch := range []string{"speaker", "mic"} {
		path := findAudioFile(t, dir, "session_") // все пишутся в один файл
		if path == "" {
			t.Errorf("expected audio file in %q", dir)
		} else {
			t.Logf("channel %s: %s", ch, filepath.Base(path))
		}
	}
}

// =========================================================================
// Тесты Close
// =========================================================================

func TestClose_DoubleClose(t *testing.T) {
	l, _ := newTestLoggerAudio(t)

	if err := l.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestLogText_AfterCloseReturnsError(t *testing.T) {
	l, _ := newTestLoggerAudio(t)

	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	err := l.LogText(common.STTEvent{Text: "should fail"})
	if err == nil {
		t.Error("expected error from LogText after Close, got nil")
	}
}

func TestSaveAudioChunk_AfterCloseReturnsError(t *testing.T) {
	l, _ := newTestLoggerAudio(t)

	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	err := l.SaveAudioChunk("speaker", []byte{1, 2, 3, 4})
	if err == nil {
		t.Error("expected error from SaveAudioChunk after Close, got nil")
	}
}

// =========================================================================
// Тесты MP3/WAV кодирования
// =========================================================================

func TestMP3Encoding(t *testing.T) {
	l, dir := newTestLoggerAudio(t)

	pcm := generatePCM(16000) // 1 секунда 16kHz mono

	chunkSize := 640 // 20ms at 16kHz
	for offset := 0; offset < len(pcm); offset += chunkSize {
		end := offset + chunkSize
		if end > len(pcm) {
			end = len(pcm)
		}
		if err := l.SaveAudioChunk("speaker", pcm[offset:end]); err != nil {
			t.Fatalf("SaveAudioChunk at offset %d: %v", offset, err)
		}
	}

	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	audioPath := findAudioFile(t, dir, "session_")
	if audioPath == "" {
		t.Fatal("no audio file found")
	}

	info, err := os.Stat(audioPath)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}

	if info.Size() == 0 {
		t.Error("audio file is empty — encoding failed")
	}

	t.Logf("Audio file: %s, size: %d bytes (raw PCM was %d bytes)",
		filepath.Base(audioPath), info.Size(), len(pcm))
}

func TestMP3FileSize(t *testing.T) {
	l, dir := newTestLoggerAudio(t)

	pcm := generatePCM(16000) // 1 секунда, 32000 байт raw
	// Разбиваем на чанки чтобы VAD-светофор включился (>=3 фрейма).
	chunkBytes := 1280 * 2
	for offset := 0; offset < len(pcm); offset += chunkBytes {
		end := offset + chunkBytes
		if end > len(pcm) {
			end = len(pcm)
		}
		if err := l.SaveAudioChunk("speaker", pcm[offset:end]); err != nil {
			t.Fatalf("SaveAudioChunk at offset %d: %v", offset, err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	audioPath := findAudioFile(t, dir, "session_")
	if audioPath == "" {
		t.Fatal("no audio file found")
	}

	info, err := os.Stat(audioPath)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}

	rawSize := int64(len(pcm))
	fileSize := info.Size()

	t.Logf("Raw PCM: %d bytes, encoded: %d bytes (%.1f%%)",
		rawSize, fileSize, float64(fileSize)/float64(rawSize)*100)

	ext := filepath.Ext(audioPath)
	if ext == ".mp3" {
		maxSize := rawSize / 5 // 20%
		if fileSize > maxSize {
			t.Errorf("MP3 file too large: %d > %d (20%% of raw PCM)", fileSize, maxSize)
		}
	} else if ext == ".wav" {
		if fileSize < rawSize {
			t.Errorf("WAV file too small: %d < raw PCM %d", fileSize, rawSize)
		}
	}
}

func TestAudioConcurrentWrites(t *testing.T) {
	l, dir := newTestLoggerAudio(t)

	const numChunks = 100
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		loudPCM := generatePCM(1280)
		for i := 0; i < numChunks; i++ {
			_ = l.SaveAudioChunk("speaker", loudPCM)
		}
	}()

	go func() {
		defer wg.Done()
		loudPCM := generatePCM(1280)
		for i := 0; i < numChunks; i++ {
			_ = l.SaveAudioChunk("mic", loudPCM)
		}
	}()

	wg.Wait()

	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	path := findAudioFile(t, dir, "session_")
	if path == "" {
		t.Errorf("no audio file")
	} else {
		info, _ := os.Stat(path)
		t.Logf("audio: %s (%d bytes)", filepath.Base(path), info.Size())
		if info.Size() == 0 {
			t.Errorf("audio file is empty")
		}
	}
}

func TestAudioGracefulClose(t *testing.T) {
	l, dir := newTestLoggerAudio(t)

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		loudPCM := generatePCM(1280)
		for i := 0; i < 500; i++ {
			if err := l.SaveAudioChunk("speaker", loudPCM); err != nil {
				return
			}
		}
	}()

	time.Sleep(10 * time.Millisecond)

	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	wg.Wait()

	audioPath := findAudioFile(t, dir, "session_")
	if audioPath == "" {
		t.Fatal("no audio file found after Close during write")
	}

	info, err := os.Stat(audioPath)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size() == 0 {
		t.Error("audio file is empty after graceful close")
	}

	t.Logf("Graceful close: %s (%d bytes)", filepath.Base(audioPath), info.Size())
}

// =========================================================================
// Тесты LogDebug
// =========================================================================

func TestLogDebug_PersistsCSV(t *testing.T) {
	l, dir := newTestLoggerAudio(t)

	if err := l.LogDebug("test debug message"); err != nil {
		t.Fatalf("LogDebug: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	var csvPath string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "session_") && strings.HasSuffix(e.Name(), ".csv") {
			csvPath = filepath.Join(dir, e.Name())
			break
		}
	}
	if csvPath == "" {
		t.Fatal("no session CSV file found")
	}

	f, err := os.Open(csvPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if len(rows) < 2 {
		t.Fatalf("expected header + 1 data row, got %d rows", len(rows))
	}

	row := rows[1]
	// DEBUG event type should be in column 2 (event).
	if row[2] != "DEBUG" {
		t.Errorf("event = %q, want DEBUG", row[2])
	}
	if row[3] != "test debug message" {
		t.Errorf("text = %q, want %q", row[3], "test debug message")
	}
}

// =========================================================================
// Тесты SaveAudioChunk при saveAudio=false
// =========================================================================

func TestSaveAudioChunk_SaveAudioDisabled(t *testing.T) {
	l, dir := newTempLogger(t) // saveAudio=false

	// Запись аудио должна вернуть nil (без ошибки) и не создать файл.
	pcm := generatePCM(1280)
	for i := 0; i < 5; i++ {
		if err := l.SaveAudioChunk("speaker", pcm); err != nil {
			t.Fatalf("SaveAudioChunk (saveAudio=false): %v", err)
		}
	}

	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Проверяем что аудио-файл не создался.
	audioPath := findAudioFile(t, dir, "session_")
	if audioPath != "" {
		t.Errorf("audio file should NOT exist when saveAudio=false, found: %s", audioPath)
	}
}

// =========================================================================
// Тесты LogTranslation с nil answers
// =========================================================================

func TestLogTranslation_NilAnswers(t *testing.T) {
	l, dir := newTestLoggerAudio(t)

	event := common.STTEvent{
		Text:      "hello",
		Event:     common.EventEndOfTurn,
		ChannelID: "speaker",
		Timestamp: time.Now(),
	}

	// LogTranslation с nil answers — не должно паниковать.
	if err := l.LogTranslation(event, "привет", nil); err != nil {
		t.Fatalf("LogTranslation with nil answers: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var csvPath string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "session_") && strings.HasSuffix(e.Name(), ".csv") {
			csvPath = filepath.Join(dir, e.Name())
			break
		}
	}
	f, _ := os.Open(csvPath)
	defer f.Close()
	r := csv.NewReader(f)
	rows, _ := r.ReadAll()
	if len(rows) >= 2 {
		row := rows[1]
		if row[5] != "" {
			t.Errorf("answers column should be empty for nil answers, got %q", row[5])
		}
	}
}

// =========================================================================
// Тесты VAD: проверка порога речи/тишины
// =========================================================================

func TestSaveAudioChunk_SilenceNotSaved(t *testing.T) {
	l, dir := newTestLoggerAudio(t)

	// Шлём тихий PCM (все нули = тишина).
	silentPCM := make([]byte, 2560) // 80ms at 16kHz mono = 0 amplitude
	for i := 0; i < 20; i++ {
		if err := l.SaveAudioChunk("speaker", silentPCM); err != nil {
			t.Fatalf("SaveAudioChunk (silent): %v", err)
		}
	}

	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Аудио-файл НЕ должен создаться, потому что VAD не пропустил тишину.
	audioPath := findAudioFile(t, dir, "session_")
	if audioPath != "" {
		t.Errorf("audio file should NOT exist for silent PCM (VAD should reject), found: %s", audioPath)
	}
}
