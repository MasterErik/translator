// Package logger defines the session logging interface and its default
// implementation, which persists transcribed text as JSON Lines and
// raw PCM audio chunks to disk.
package logger

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mastererik/translator/internal/common"
)

// logJob is an internal work item sent to the background writer goroutine.
// It carries either a text event (for JSON logging) or raw PCM data
// (for audio dumps).
type logJob struct {
	eventType string          // "text" or "audio"
	event     common.STTEvent // set for text jobs
	channelID string          // set for audio jobs
	pcmData   []byte          // set for audio jobs
}

// LogEntry is the JSON-serialisable record written to the session log file.
// Each line is one LogEntry value.
type LogEntry struct {
	Timestamp   string   `json:"timestamp"`
	ChannelID   string   `json:"channel_id"`
	Text        string   `json:"text"`
	IsFinal     bool     `json:"is_final"`
	Translation string   `json:"translation,omitempty"`
	Answers     []string `json:"answers,omitempty"`
}

// FileSessionLogger implements SessionLogger by writing JSON Lines to a
// timestamped session file and raw PCM audio chunks to per-channel files
// inside an audio/ subdirectory.  All file I/O is serialised through a
// background worker goroutine so that callers never block on disk writes.
type FileSessionLogger struct {
	mu         sync.Mutex
	file       *os.File
	encoder    *json.Encoder
	audioDir   string
	audioFiles map[string]AudioEncoder // keyed by channelID ("speaker", "mic")
	writeCh    chan logJob             // buffered async write queue (size 256)
	done       chan struct{}           // closed when worker goroutine exits
	closed     bool
}

// NewFileSessionLogger creates a new session logger rooted at logDir.
//
// It creates logDir (if missing), opens a timestamped JSON log file
// (session_YYYY-MM-DD_HH-MM-SS.json), creates an audio/ subdirectory,
// and starts the background worker that serialises all writes.
//
// The caller must call Close() to flush buffers and release resources.
func NewFileSessionLogger(logDir string) (*FileSessionLogger, error) {
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("logger: create log dir: %w", err)
	}

	ts := time.Now().Format("2006-01-02_15-04-05")
	logPath := filepath.Join(logDir, "session_"+ts+".json")

	f, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("logger: create session file: %w", err)
	}

	audioDir := filepath.Join(logDir, "audio")
	// Директория audio/ создаётся лениво при первой записи аудио.
	// Не создаём её здесь — если save_audio=false, директория не нужна.

	fsl := &FileSessionLogger{
		file:       f,
		encoder:    json.NewEncoder(f),
		audioDir:   audioDir,
		audioFiles: make(map[string]AudioEncoder),
		writeCh:    make(chan logJob, 256),
		done:       make(chan struct{}),
	}

	go fsl.backgroundWorker()

	return fsl, nil
}

// LogText records an STT transcription event.  It sends the event to the
// background worker via a non-blocking channel send.  Returns an error if
// the logger has already been closed.
func (fsl *FileSessionLogger) LogText(event common.STTEvent) error {
	fsl.mu.Lock()
	defer fsl.mu.Unlock()

	if fsl.closed {
		return fmt.Errorf("logger: LogText on closed session logger")
	}

	select {
	case fsl.writeCh <- logJob{
		eventType: "text",
		event:     event,
	}:
		return nil
	default:
		return fmt.Errorf("logger: write queue full, dropping text event")
	}
}

// SaveAudioChunk appends raw PCM data to the per-channel audio file.
// The file for channelID is opened lazily on the first call.
// The send to the background worker is non-blocking.
// Returns an error if the logger has already been closed.
func (fsl *FileSessionLogger) SaveAudioChunk(channelID string, pcm []byte) error {
	fsl.mu.Lock()
	defer fsl.mu.Unlock()

	if fsl.closed {
		return fmt.Errorf("logger: SaveAudioChunk on closed session logger")
	}

	select {
	case fsl.writeCh <- logJob{
		eventType: "audio",
		channelID: channelID,
		pcmData:   pcm,
	}:
		return nil
	default:
		return fmt.Errorf("logger: write queue full, dropping audio chunk")
	}
}

// Close signals the background worker to drain its queue, flushes all
// buffered data, closes every open file handle, and waits for the worker
// goroutine to exit.  It is safe to call Close multiple times.
func (fsl *FileSessionLogger) Close() error {
	fsl.mu.Lock()
	if fsl.closed {
		fsl.mu.Unlock()
		return nil
	}
	fsl.closed = true
	fsl.mu.Unlock()

	close(fsl.writeCh)
	<-fsl.done

	// Close the main JSON log file.
	if fsl.file != nil {
		fsl.file.Close()
	}

	// Close all lazily-opened audio encoders.
	for _, enc := range fsl.audioFiles {
		enc.Close()
	}

	return nil
}

// backgroundWorker is the single goroutine that serialises all file I/O.
// It reads jobs from writeCh: text jobs are encoded as JSON and written
// to the session log file; audio jobs are appended to per-channel PCM files
// (opened lazily on first use).  The worker exits when writeCh is closed
// and drained.
func (fsl *FileSessionLogger) backgroundWorker() {
	defer close(fsl.done)

	for job := range fsl.writeCh {
		switch job.eventType {
		case "text":
			entry := LogEntry{
				Timestamp: job.event.Timestamp.Format(time.RFC3339Nano),
				ChannelID: job.event.ChannelID,
				Text:      job.event.Text,
				IsFinal:   job.event.IsFinal,
			}
			if err := fsl.encoder.Encode(entry); err != nil {
				// Best-effort logging; we can't surface this error
				// without blocking the caller or introducing
				// another channel.
				_, _ = fmt.Fprintf(os.Stderr,
					"logger: failed to encode log entry: %v\n", err)
			}
		case "audio":
			enc, ok := fsl.audioFiles[job.channelID]
			if !ok {
				// Создаём audio/ директорию лениво при первой записи.
				if err := os.MkdirAll(fsl.audioDir, 0755); err != nil {
					_, _ = fmt.Fprintf(os.Stderr,
						"logger: failed to create audio dir: %v\n", err)
					continue
				}
				enc = newAudioEncoder(fsl.audioDir, job.channelID)
				fsl.audioFiles[job.channelID] = enc
			}
			// Конвертируем []byte → []int16 (little-endian, 2 байта на сэмпл).
			samples := bytesToInt16(job.pcmData)
			if err := enc.Write(samples); err != nil {
				_, _ = fmt.Fprintf(os.Stderr,
					"logger: failed to write audio chunk for %s: %v\n",
					job.channelID, err)
			}
		}
	}
}

// bytesToInt16 конвертирует срез PCM-байт (little-endian, 2 байта на сэмпл)
// в срез int16-сэмплов. Неполный последний байт игнорируется.
func bytesToInt16(pcm []byte) []int16 {
	n := len(pcm) / 2
	samples := make([]int16, n)
	for i := 0; i < n; i++ {
		samples[i] = int16(binary.LittleEndian.Uint16(pcm[i*2:]))
	}
	return samples
}
