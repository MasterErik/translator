// Package main — точка входа и вспомогательные функции для приложения Translator.
package main

import (
	"context"
	"log/slog"

	"github.com/mastererik/translator/internal/common"
	"github.com/mastererik/translator/internal/logger"
	"github.com/mastererik/translator/internal/stt"
	"github.com/mastererik/translator/internal/ui"
)

// --------------------------------------------------------------------------
// Структуры worker pool
// --------------------------------------------------------------------------

// transJob — задание на перевод, отправляемое от dispatch к worker'у.
type transJob struct {
	event common.STTEvent
}

// transResult — результат перевода, отправляемый от worker'а обратно к dispatch.
type transResult struct {
	translation string
	answers     []string
	isQuestion  bool
	err         error
	event       common.STTEvent // оригинальное событие для логирования
}

// --------------------------------------------------------------------------
// overlay — интерфейс оверлея (реальный GioUI или заглушка для тестов)
// --------------------------------------------------------------------------

// overlay описывает контракт UI-оверлея: приём сообщений и цикл событий.
type overlay interface {
	AddMessage(msg ui.UIMessage)
	GetMessages() []ui.UIMessage
	Run(ctx context.Context) error
	WaitShutdown()
}

// capturer абстрагирует двухканальный захват аудио, чтобы вспомогательные
// функции пайплайна работали как с capture.Capture (реальный, требует CGO),
// так и с capture.StubCapture (заглушка для тестов, без CGO).
type capturer interface {
	Start(ctx context.Context) (<-chan []byte, <-chan []byte, error)
}

// --------------------------------------------------------------------------
// Вспомогательные функции пайплайна
// --------------------------------------------------------------------------

// runCapture запускает двухканальный захват аудио и направляет PCM-данные
// в аудиопоток STT-провайдера и (опционально) логгер сессии.
func runCapture(ctx context.Context, cap capturer, sttProv stt.STTProvider, sessLog logger.SessionLogger, saveAudio bool) {
	loopbackCh, micCh, err := cap.Start(ctx)
	if err != nil {
		slog.Error("не удалось запустить захват аудио", "error", err)
		return
	}

	audioStream := sttProv.AudioStream()

	// Направляем loopback (собеседник).
	go routeAudioChannel(ctx, loopbackCh, audioStream, "speaker", sessLog, saveAudio)

	// Направляем микрофон (ваш голос).
	go routeAudioChannel(ctx, micCh, audioStream, "mic", sessLog, saveAudio)

	<-ctx.Done()
	slog.Info("пайплайн захвата остановлен")
}

// routeAudioChannel читает PCM-фрагменты из src и пересылает в dst.
// Если saveAudio == true, также сохраняет каждый фрагмент в логгер сессии.
func routeAudioChannel(ctx context.Context, src <-chan []byte, dst chan<- []byte, channelID string, sessLog logger.SessionLogger, saveAudio bool) {
	for {
		select {
		case <-ctx.Done():
			return
		case chunk, ok := <-src:
			if !ok {
				return
			}
			// Пересылаем в STT (неблокирующая отправка).
			select {
			case dst <- chunk:
			case <-ctx.Done():
				return
			default:
				// Буфер аудиопотока полон — пропускаем кадр.
			}

			// Сохраняем в лог только если включено (best-effort, неблокирующая).
			if saveAudio {
				if err := sessLog.SaveAudioChunk(channelID, chunk); err != nil {
					slog.Warn("не удалось сохранить аудио-фрагмент", "channel", channelID, "error", err)
				}
			}
		}
	}
}

// runSTT читает транскрипции STTEvent из провайдера и направляет их
// в textStream для центрального dispatch-узла.
// Промежуточные результаты логируются, но не отправляются в UI —
// это делает dispatch.
func runSTT(ctx context.Context, sttProv stt.STTProvider, sessLog logger.SessionLogger, textStream chan<- common.STTEvent) {
	src := sttProv.TextStream()

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-src:
			if !ok {
				return
			}

			if event.Error != nil {
				slog.Warn("ошибка STT-события", "error", event.Error)
				continue
			}

			// Логируем все текстовые события.
			if err := sessLog.LogText(event); err != nil {
				slog.Warn("не удалось записать текст в лог", "error", err)
			}

			if !event.IsFinal {
				slog.Debug("промежуточный транскрипт", "text", event.Text, "channel", event.ChannelID)
			}

			// Отправляем событие в dispatch (неблокирующая).
			select {
			case textStream <- event:
			case <-ctx.Done():
				return
			default:
				slog.Warn("textStream переполнен, событие пропущено", "text", event.Text)
			}
		}
	}
}

// runUI запускает цикл событий оверлея и блокируется до отмены контекста
// или закрытия окна оверлея.
func runUI(ctx context.Context, ov overlay) {
	slog.Info("UI-оверлей запущен")
	if err := ov.Run(ctx); err != nil {
		slog.Info("UI-оверлей остановлен", "reason", err)
	}
}
