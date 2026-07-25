// Package logger defines the session logging interface and its default
// implementation, which persists transcribed text as JSON Lines and
// encoded audio (MP3 via go-lame, WAV fallback via beep/wav) to disk.
package logger

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/wav"
	"github.com/sunicy/go-lame"
)

// AudioEncoder — интерфейс для кодирования PCM-сэмплов в аудио-формат.
// Write принимает срез int16 PCM-сэмплов (16kHz, mono) и асинхронно
// кодирует их в целевой формат. Close финализирует кодирование,
// дописывает остаточные фреймы и закрывает нижележащий файл.
type AudioEncoder interface {
	Write(pcm []int16) error
	Close() error
}

// mp3Encoder кодирует PCM в MP3 через go-lame (CGO-обёртка libmp3lame).
// Параметры: 16000 Hz, mono, quality=5 (хорошее качество, быстро).
type mp3Encoder struct {
	file   *os.File
	writer *lame.Writer
}

// wavEncoder буферизует PCM-сэмплы в памяти и при Close() кодирует
// весь буфер в WAV через beep/wav.
type wavEncoder struct {
	dir       string
	channelID string
	samples   []int16
}

// newMP3Encoder создаёт MP3-файл с параметрами 16000 Hz, mono, quality=5.
func newMP3Encoder(dir, channelID string) (*mp3Encoder, error) {
	path := filepath.Join(dir, channelID+".mp3")
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("mp3: create file: %w", err)
	}

	wr, err := lame.NewWriter(f)
	if err != nil {
		f.Close()
		os.Remove(path)
		return nil, fmt.Errorf("mp3: create lame writer: %w", err)
	}

	wr.InSampleRate = 16000
	wr.InNumChannels = 1
	wr.OutMode = lame.MODE_MONO
	wr.OutQuality = 5
	wr.OutSampleRate = 16000

	return &mp3Encoder{file: f, writer: wr}, nil
}

// Write конвертирует []int16 в little-endian []byte и передаёт в lame.Writer.
func (e *mp3Encoder) Write(pcm []int16) error {
	buf := make([]byte, len(pcm)*2)
	for i, s := range pcm {
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(s))
	}
	_, err := e.writer.Write(buf)
	return err
}

// Close вызывает lame.Writer.Close() для дописывания финального MP3-фрейма,
// затем закрывает файл.
func (e *mp3Encoder) Close() error {
	if err := e.writer.Close(); err != nil {
		e.file.Close()
		return fmt.Errorf("mp3: close lame writer: %w", err)
	}
	return e.file.Close()
}

// newWAVEncoder создаёт WAV-энкодер, который буферизует сэмплы в памяти
// до вызова Close().
func newWAVEncoder(dir, channelID string) *wavEncoder {
	return &wavEncoder{
		dir:       dir,
		channelID: channelID,
	}
}

// Write добавляет PCM-сэмплы во внутренний буфер.
func (e *wavEncoder) Write(pcm []int16) error {
	e.samples = append(e.samples, pcm...)
	return nil
}

// Close создаёт WAV-файл и кодирует весь накопленный буфер через beep/wav.
// Формат: 16000 Hz, mono, 16-bit PCM.
func (e *wavEncoder) Close() error {
	path := filepath.Join(e.dir, e.channelID+".wav")
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("wav: create file: %w", err)
	}
	defer f.Close()

	format := beep.Format{
		SampleRate:  16000,
		NumChannels: 1,
		Precision:   2,
	}

	streamer := &pcmStreamer{data: e.samples}
	return wav.Encode(f, streamer, format)
}

// pcmStreamer адаптирует срез []int16 (mono) к интерфейсу beep.Streamer.
type pcmStreamer struct {
	data []int16
	pos  int
}

func (s *pcmStreamer) Stream(samples [][2]float64) (n int, ok bool) {
	for i := range samples {
		if s.pos >= len(s.data) {
			return n, false
		}
		samples[i][0] = float64(s.data[s.pos]) / 32768.0
		s.pos++
		n++
	}
	return n, true
}

// Err всегда возвращает nil — pcmStreamer не генерирует ошибок.
func (s *pcmStreamer) Err() error { return nil }

// newAudioEncoder пробует создать MP3-энкодер через go-lame.
// При ошибке (отсутствует libmp3lame, CGO отключён и т.д.) —
// fallback на WAV через beep/wav.
func newAudioEncoder(dir, channelID string) AudioEncoder {
	enc, err := newMP3Encoder(dir, channelID)
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"logger: mp3 encoder unavailable, falling back to wav: %v\n", err)
		return newWAVEncoder(dir, channelID)
	}
	return enc
}
