// Package logger defines the session logging interface.
// Implementations persist transcribed text and raw audio chunks
// for post-session review and debugging.
package logger

import "github.com/mastererik/translator/internal/common"

// SessionLogger — интерфейс логирования сессии.
// FileSessionLogger пишет CSV + MP3. NopSessionLogger — заглушка.
type SessionLogger interface {
	// LogText записывает STT-событие в лог.
	LogText(event common.STTEvent) error

	// LogTranslation записывает результат перевода (с подсказками).
	LogTranslation(event common.STTEvent, translation string, answers []string) error

	// SaveAudioChunk добавляет PCM-фрагмент в аудио-файл.
	SaveAudioChunk(channelID string, pcm []byte) error

	// LogDebug записывает отладочное сообщение.
	LogDebug(msg string) error

	// Close завершает логгер, сбрасывает буферы, закрывает файлы.
	Close() error
}
