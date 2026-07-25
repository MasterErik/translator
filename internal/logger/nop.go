// Package logger — логирование сессии.
package logger

import "github.com/mastererik/translator/internal/common"

// NopSessionLogger — заглушка (Null Object) для SessionLogger.
// Все методы возвращают nil, ничего не делая.
// Используется когда логирование отключено (LogDir пуст).
type NopSessionLogger struct{}

// NewNopSessionLogger создаёт заглушку логгера.
func NewNopSessionLogger() *NopSessionLogger {
	return &NopSessionLogger{}
}

func (n *NopSessionLogger) LogText(event common.STTEvent) error { return nil }
func (n *NopSessionLogger) LogTranslation(event common.STTEvent, translation string, answers []string) error {
	return nil
}
func (n *NopSessionLogger) SaveAudioChunk(channelID string, pcm []byte) error { return nil }
func (n *NopSessionLogger) LogDebug(msg string) error                         { return nil }
func (n *NopSessionLogger) Close() error                                      { return nil }

var _ SessionLogger = (*NopSessionLogger)(nil)
