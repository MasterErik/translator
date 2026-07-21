// Package main — точка входа и вспомогательные функции для приложения Translator.
package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/mastererik/translator/internal/logger"
	"github.com/mastererik/translator/internal/stt"
	"github.com/mastererik/translator/internal/translator"
	"github.com/mastererik/translator/internal/ui"
)

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
// в аудиопоток STT-провайдера и логгер сессии.
func runCapture(ctx context.Context, cap capturer, sttProv stt.STTProvider, sessLog logger.SessionLogger) {
	loopbackCh, micCh, err := cap.Start(ctx)
	if err != nil {
		slog.Error("не удалось запустить захват аудио", "error", err)
		return
	}

	audioStream := sttProv.AudioStream()

	// Направляем loopback (собеседник).
	go routeAudioChannel(ctx, loopbackCh, audioStream, "speaker", sessLog)

	// Направляем микрофон (ваш голос).
	go routeAudioChannel(ctx, micCh, audioStream, "mic", sessLog)

	<-ctx.Done()
	slog.Info("пайплайн захвата остановлен")
}

// routeAudioChannel читает PCM-фрагменты из src и пересылает в dst.
// Также сохраняет каждый фрагмент в логгер сессии.
func routeAudioChannel(ctx context.Context, src <-chan []byte, dst chan<- []byte, channelID string, sessLog logger.SessionLogger) {
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

			// Сохраняем в лог (best-effort, неблокирующая).
			if err := sessLog.SaveAudioChunk(channelID, chunk); err != nil {
				slog.Warn("не удалось сохранить аудио-фрагмент", "channel", channelID, "error", err)
			}
		}
	}
}

// runSTT читает транскрипции STTEvent из провайдера и направляет их:
//   - Промежуточные (interim) — в оверлей для быстрого предпросмотра.
//   - Финальные (final) — через движок перевода, в логгер и оверлей.
func runSTT(ctx context.Context, sttProv stt.STTProvider, engine *translator.TranslationEngine, ov overlay, sessLog logger.SessionLogger) {
	textStream := sttProv.TextStream()

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-textStream:
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
				// Промежуточный результат: показываем предпросмотр.
				ov.AddMessage(ui.UIMessage{
					Type:      ui.Translation,
					Text:      event.Text,
					Timestamp: event.Timestamp,
				})
				continue
			}

			// Финальный результат: переводим и классифицируем.
			result, err := engine.ProcessFinalTranscript(ctx, event.Text)
			if err != nil {
				slog.Error("перевод не удался", "error", err, "text", event.Text)
				continue
			}

			// Отправляем перевод в оверлей.
			ov.AddMessage(ui.UIMessage{
				Type:      ui.Translation,
				Text:      result.Translation,
				Timestamp: event.Timestamp,
			})

			// Если фраза — вопрос, ждём асинхронной генерации подсказок.
			if result.IsQuestion {
				resultCopy := result
				eventCopy := event
				go func() {
					for i := 0; i < 10; i++ {
						if len(resultCopy.Answers) > 0 {
							ov.AddMessage(ui.UIMessage{
								Type:      ui.AnswerCandidates,
								Text:      eventCopy.Text,
								Answers:   resultCopy.Answers,
								Timestamp: eventCopy.Timestamp,
							})
							return
						}
						select {
						case <-ctx.Done():
							return
						case <-time.After(100 * time.Millisecond):
						}
					}
					slog.Warn("таймаут генерации подсказок", "question", eventCopy.Text)
				}()
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
