package logger_test

import (
	"encoding/json"
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

// newTempLogger создаёт FileSessionLogger в временной директории.
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

// findAudioFile ищет файл с именем, начинающимся на channelID,
// в директории audio/. Возвращает полный путь или пустую строку.
func findAudioFile(t *testing.T, dir, channelID string) string {
	t.Helper()
	audioDir := filepath.Join(dir, "audio")
	entries, err := os.ReadDir(audioDir)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", audioDir, err)
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), channelID) {
			return filepath.Join(audioDir, e.Name())
		}
	}
	return ""
}

// audioFileSize возвращает размер аудио-файла для channelID или 0.
func audioFileSize(t *testing.T, dir, channelID string) int64 {
	t.Helper()
	path := findAudioFile(t, dir, channelID)
	if path == "" {
		return 0
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// generatePCM генерирует срез []byte с PCM-сэмплами (16kHz, mono, 16-bit).
// nSamples — количество сэмплов.
func generatePCM(nSamples int) []byte {
	buf := make([]byte, nSamples*2)
	for i := 0; i < nSamples; i++ {
		// Простой синусоидальный сигнал 440 Гц для тестирования.
		sample := int16(float64(10000) * sin(2*3.14159*440*float64(i)/16000.0))
		buf[i*2] = byte(sample)
		buf[i*2+1] = byte(sample >> 8)
	}
	return buf
}

func sin(x float64) float64 {
	// Простое приближение для избежания импорта math.
	// sin(x) ≈ x - x³/6 + x⁵/120 для малых x, но здесь используем цикличность.
	// Для тестов точность не критична — используем lookup или простейший генератор.
	// Приводим к диапазону [-π, π].
	twoPi := 2 * 3.141592653589793
	for x > 3.141592653589793 {
		x -= twoPi
	}
	for x < -3.141592653589793 {
		x += twoPi
	}
	// sin(x) ≈ x - x³/6 + x⁵/120 - x⁷/5040
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

// TestNewFileSessionLogger_DirectorySetup проверяет, что NewFileSessionLogger
// создаёт log-директорию и session_*.json файл, но НЕ audio/ (ленивое создание).
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

	// audio/ НЕ должно существовать до первой записи.
	audioDir := filepath.Join(dir, "audio")
	if info, err := os.Stat(audioDir); err == nil && info.IsDir() {
		t.Errorf("audio/ at %q should NOT exist before first audio write", audioDir)
	}

	// После записи аудио audio/ должно создаться.
	if err := l.SaveAudioChunk("speaker", []byte{0, 0, 0, 0}); err != nil {
		t.Fatalf("SaveAudioChunk: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if info, err := os.Stat(audioDir); err != nil || !info.IsDir() {
		t.Errorf("expected audio/ at %q after audio write, stat: %v", audioDir, err)
	}
}

// TestLogText_PersistsJSON проверяет, что LogText записывает JSON-строку.
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
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

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

// TestSaveAudioChunk_CreatesAudioFile проверяет, что SaveAudioChunk
// создаёт аудио-файл (.mp3 или .wav) с ненулевым размером.
func TestSaveAudioChunk_CreatesAudioFile(t *testing.T) {
	l, dir := newTempLogger(t)

	pcmData := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06} // 3 сэмпла
	if err := l.SaveAudioChunk("speaker", pcmData); err != nil {
		t.Fatalf("SaveAudioChunk (first): %v", err)
	}

	moreData := []byte{0x07, 0x08, 0x09, 0x0A} // 2 сэмпла
	if err := l.SaveAudioChunk("speaker", moreData); err != nil {
		t.Fatalf("SaveAudioChunk (second): %v", err)
	}

	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Ищем аудио-файл (speaker.mp3 или speaker.wav).
	audioPath := findAudioFile(t, dir, "speaker")
	if audioPath == "" {
		t.Fatalf("no audio file found for channel 'speaker' in %q/audio/", dir)
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

// TestSaveAudioChunk_MultipleChannels проверяет, что разные channelID
// создают разные аудио-файлы.
func TestSaveAudioChunk_MultipleChannels(t *testing.T) {
	l, dir := newTempLogger(t)

	// Минимум 4 байта (2 сэмпла) для валидного PCM.
	if err := l.SaveAudioChunk("speaker", []byte{0xAA, 0x00, 0xBB, 0x00}); err != nil {
		t.Fatalf("SaveAudioChunk(speaker): %v", err)
	}
	if err := l.SaveAudioChunk("mic", []byte{0xCC, 0x00, 0xDD, 0x00}); err != nil {
		t.Fatalf("SaveAudioChunk(mic): %v", err)
	}

	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	for _, ch := range []string{"speaker", "mic"} {
		path := findAudioFile(t, dir, ch)
		if path == "" {
			t.Errorf("expected audio file for %s in audio/", ch)
		} else {
			t.Logf("channel %s: %s", ch, filepath.Base(path))
		}
	}
}

// =========================================================================
// Тесты Close
// =========================================================================

func TestClose_DoubleClose(t *testing.T) {
	l, _ := newTempLogger(t)

	if err := l.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

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

func TestSaveAudioChunk_AfterCloseReturnsError(t *testing.T) {
	l, _ := newTempLogger(t)

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

// TestMP3Encoding проверяет, что 1 секунда PCM (16000 сэмплов)
// даёт валидный аудио-файл с ненулевым размером.
func TestMP3Encoding(t *testing.T) {
	l, dir := newTempLogger(t)

	// 1 секунда моно PCM 16kHz = 16000 сэмплов = 32000 байт.
	pcm := generatePCM(16000)

	// Отправляем чанками по ~20ms (320 сэмплов = 640 байт).
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

	audioPath := findAudioFile(t, dir, "speaker")
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

	// Проверяем, что размер > 0 (файл не пустой).
	t.Logf("Audio file: %s, size: %d bytes (raw PCM was %d bytes)",
		filepath.Base(audioPath), info.Size(), len(pcm))
}

// TestMP3FileSize проверяет, что размер выходного файла существенно
// меньше raw PCM (MP3/WAV сжатие).
func TestMP3FileSize(t *testing.T) {
	l, dir := newTempLogger(t)

	pcm := generatePCM(16000) // 1 секунда, 32000 байт raw
	if err := l.SaveAudioChunk("speaker", pcm); err != nil {
		t.Fatalf("SaveAudioChunk: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	audioPath := findAudioFile(t, dir, "speaker")
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

	// WAV добавляет 44-байтный заголовок, поэтому для WAV размер ≈ rawSize + 44.
	// MP3 должен быть значительно меньше.
	// Для MP3: fileSize < 20% от rawSize (~6400 байт для 1с моно 16kHz).
	// Для WAV: fileSize ≈ rawSize + 44.
	ext := filepath.Ext(audioPath)
	if ext == ".mp3" {
		maxSize := rawSize / 5 // 20%
		if fileSize > maxSize {
			t.Errorf("MP3 file too large: %d > %d (20%% of raw PCM)", fileSize, maxSize)
		}
	} else if ext == ".wav" {
		// WAV ≈ raw PCM + 44-byte header.
		if fileSize < rawSize {
			t.Errorf("WAV file too small: %d < raw PCM %d", fileSize, rawSize)
		}
	}
}

// TestAudioConcurrentWrites проверяет конкуррентную запись speaker и mic —
// должны создаться два аудио-файла.
func TestAudioConcurrentWrites(t *testing.T) {
	l, dir := newTempLogger(t)

	const numChunks = 100
	var wg sync.WaitGroup
	wg.Add(2)

	// speaker writer
	go func() {
		defer wg.Done()
		for i := 0; i < numChunks; i++ {
			_ = l.SaveAudioChunk("speaker", []byte{byte(i), 0, byte(i), 0})
		}
	}()

	// mic writer
	go func() {
		defer wg.Done()
		for i := 0; i < numChunks; i++ {
			_ = l.SaveAudioChunk("mic", []byte{byte(i), 0, byte(i), 0})
		}
	}()

	wg.Wait()

	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	for _, ch := range []string{"speaker", "mic"} {
		path := findAudioFile(t, dir, ch)
		if path == "" {
			t.Errorf("no audio file for channel %s", ch)
		} else {
			info, _ := os.Stat(path)
			t.Logf("channel %s: %s (%d bytes)", ch, filepath.Base(path), info.Size())
			if info.Size() == 0 {
				t.Errorf("audio file for %s is empty", ch)
			}
		}
	}
}

// TestAudioGracefulClose проверяет, что Close() во время активной записи
// не вызывает паники и файл корректен (ненулевой размер).
func TestAudioGracefulClose(t *testing.T) {
	l, dir := newTempLogger(t)

	var wg sync.WaitGroup
	wg.Add(1)

	// Пишем непрерывно в фоне.
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			if err := l.SaveAudioChunk("speaker", []byte{byte(i), 0, byte(i), 0}); err != nil {
				return // логгер закрыт
			}
		}
	}()

	// Даём немного времени для начала записи, затем закрываем.
	time.Sleep(10 * time.Millisecond)

	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	wg.Wait()

	audioPath := findAudioFile(t, dir, "speaker")
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
