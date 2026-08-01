package logger

import (
	"encoding/binary"
	"encoding/csv"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mastererik/translator/internal/common"
)

type logJob struct {
	eventType   string
	event       common.STTEvent
	pcmData     []byte
	translation string
	answers     []string
	debugMsg    string
}

// VAD-светофор: старт после 3 фреймов речи, стоп после 5 сек тишины.
const (
	speechThreshold = 500 // |sample| > 500 из 32767 → речь
	speechFrames    = 3   // 240ms речи подряд для старта записи
	silenceFrames   = 10  // 800ms тишины для остановки (10 × 80ms)
)

// FileSessionLogger — CSV + MP3 аудио в одном каталоге.
type FileSessionLogger struct {
	mu        sync.Mutex
	file      *os.File
	writer    *csv.Writer
	sessionTS string
	logDir    string
	audioEnc  AudioEncoder
	writeCh   chan logJob
	done      chan struct{}
	closed    bool
	saveAudio bool

	// VAD-светофор
	speechCount  int
	silenceCount int
	isSpeaking   bool
}

func NewFileSessionLogger(logDir string, saveAudio bool) (*FileSessionLogger, error) {
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("logger: %w", err)
	}
	ts := time.Now().Format("2006-01-02_15-04-05")
	logPath := filepath.Join(logDir, "session_"+ts+".csv")
	f, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("logger: %w", err)
	}
	w := csv.NewWriter(f)
	w.Write([]string{"timestamp", "channel", "event", "text", "translation", "answers"})
	w.Flush()

	fsl := &FileSessionLogger{
		file: f, writer: w, sessionTS: ts, logDir: logDir,
		writeCh: make(chan logJob, 64), done: make(chan struct{}),
		saveAudio: saveAudio,
	}
	slog.Info("логгер сессии создан", "csv", logPath, "save_audio", saveAudio)
	go fsl.backgroundWorker()
	return fsl, nil
}

func (fsl *FileSessionLogger) LogText(e common.STTEvent) error {
	fsl.mu.Lock()
	defer fsl.mu.Unlock()
	if fsl.closed {
		return fmt.Errorf("logger: closed")
	}
	return fsl.send(logJob{eventType: "text", event: e})
}

func (fsl *FileSessionLogger) LogTranslation(e common.STTEvent, tr string, ans []string) error {
	fsl.mu.Lock()
	defer fsl.mu.Unlock()
	if fsl.closed {
		return fmt.Errorf("logger: closed")
	}
	return fsl.send(logJob{eventType: "translation", event: e, translation: tr, answers: ans})
}

func (fsl *FileSessionLogger) SaveAudioChunk(_ string, pcm []byte) error {
	if !fsl.saveAudio {
		return nil
	}
	fsl.mu.Lock()
	defer fsl.mu.Unlock()
	if fsl.closed {
		return fmt.Errorf("logger: closed")
	}
	return fsl.send(logJob{eventType: "audio", pcmData: pcm})
}

func (fsl *FileSessionLogger) LogDebug(msg string) error {
	fsl.mu.Lock()
	defer fsl.mu.Unlock()
	if fsl.closed {
		return fmt.Errorf("logger: closed")
	}
	return fsl.send(logJob{eventType: "debug", debugMsg: msg})
}

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

	if fsl.writer != nil {
		fsl.writer.Flush()
	}
	if fsl.file != nil {
		fsl.file.Close()
	}
	if fsl.audioEnc != nil {
		fsl.audioEnc.Close()
	}
	slog.Info("логгер сессии закрыт")
	return nil
}

func (fsl *FileSessionLogger) send(job logJob) error {
	select {
	case fsl.writeCh <- job:
		return nil
	default:
		return fmt.Errorf("logger: очередь переполнена")
	}
}

func (fsl *FileSessionLogger) backgroundWorker() {
	defer close(fsl.done)
	for job := range fsl.writeCh {
		switch job.eventType {
		case "text":
			fsl.writer.Write([]string{fmtTS(job.event.Timestamp), ch(job.event.ChannelID), job.event.Event, job.event.Text, "", ""})
		case "translation":
			fsl.writer.Write([]string{fmtTS(job.event.Timestamp), ch(job.event.ChannelID), job.event.Event, job.event.Text, job.translation, strings.Join(job.answers, ";")})
			fsl.writer.Flush()
		case "debug":
			fsl.writer.Write([]string{time.Now().Format("2006-01-02T15:04:05.000"), "-", "DEBUG", job.debugMsg, "", ""})
		case "audio":
			if !fsl.vadPass(job.pcmData) {
				continue
			}
			if fsl.audioEnc == nil {
				fsl.audioEnc = newAudioEncoder(fsl.logDir, "session_"+fsl.sessionTS)
			}
			fsl.audioEnc.Write(bytesToInt16(job.pcmData))
		}
	}
}

func ch(id string) string {
	if len(id) > 0 {
		return string(id[0])
	}
	return "?"
}
func fmtTS(t time.Time) string { return t.Format("2006-01-02T15:04:05.000") }
func bytesToInt16(pcm []byte) []int16 {
	n := len(pcm) / 2
	s := make([]int16, n)
	for i := range n {
		s[i] = int16(binary.LittleEndian.Uint16(pcm[i*2:]))
	}
	return s
}

// vadPass — VAD-светофор: старт после 3 фреймов речи, стоп после ~5 сек тишины.
// Внутри разговора пишем всё (паузы до 5 сек не режутся).
func (fsl *FileSessionLogger) vadPass(pcm []byte) bool {
	if hasSpeech(pcm) {
		fsl.speechCount++
		fsl.silenceCount = 0
		if fsl.speechCount >= speechFrames {
			fsl.isSpeaking = true
		}
		return fsl.isSpeaking
	}
	if !fsl.isSpeaking {
		fsl.speechCount = 0
		return false
	}
	// isSpeaking && тишина — считаем паузу.
	fsl.silenceCount++
	fsl.speechCount = 0
	if fsl.silenceCount >= silenceFrames {
		fsl.isSpeaking = false
		return false
	}
	return true
}

// hasSpeech проверяет PCM-фрейм на наличие речи по пиковой амплитуде.
func hasSpeech(pcm []byte) bool {
	for i := 0; i < len(pcm)-1; i += 2 {
		s := int16(binary.LittleEndian.Uint16(pcm[i:]))
		if s > speechThreshold || s < -speechThreshold {
			return true
		}
	}
	return false
}
