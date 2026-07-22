// Package main — dispatch-узел: центральный select между STT-событиями
// и результатами перевода от worker pool.
package main

import (
	"context"
	"log/slog"

	"github.com/mastererik/translator/internal/common"
	"github.com/mastererik/translator/internal/translator"
	"github.com/mastererik/translator/internal/ui"
)

// runDispatch — центральный select-узел, координирующий поток STT-событий
// и асинхронный перевод.
//
// Параметры:
//   - textStream: канал STT-событий (interim + final) от runSTT.
//   - overlay: UI-оверлей для вывода сообщений.
//   - engine: движок перевода.
//   - numWorkers: количество параллельных worker'ов.
//
// Логика:
//   - interim-события: сразу в UI (предпросмотр).
//   - final-события: отправляются в worker pool через канал jobs.
//   - результаты перевода: приходят из transResults и отправляются в UI.
//
// При отмене контекста закрывает канал jobs (сигнал worker'ам),
// дочитывает оставшиеся результаты из transResults и завершается.
func runDispatch(ctx context.Context, textStream <-chan common.STTEvent, ov overlay, engine *translator.TranslationEngine, numWorkers int) {
	// Создаём канал заданий с буфером, чтобы dispatch не блокировался
	// при отправке final-событий.
	jobs := make(chan transJob, numWorkers*2)

	// Запускаем worker pool.
	transResults := startWorkers(ctx, engine, jobs, numWorkers)

	slog.Info("dispatch-узел запущен", "num_workers", numWorkers)

	for {
		select {
		case <-ctx.Done():
			// Закрываем канал заданий — worker'ы завершатся по мере
			// drain'а оставшихся заданий или по ctx.Done().
			close(jobs)

			// Дочитываем оставшиеся результаты перевода.
			slog.Info("dispatch: drain результатов перевода перед остановкой")
			for tr := range transResults {
				handleTranslationResult(ov, tr)
			}

			slog.Info("dispatch-узел остановлен")
			return

		case event, ok := <-textStream:
			if !ok {
				slog.Info("textStream закрыт, dispatch завершает работу")
				close(jobs)
				for tr := range transResults {
					handleTranslationResult(ov, tr)
				}
				return
			}

			if !event.IsFinal {
				// Промежуточный результат: сразу в UI.
				ov.AddMessage(ui.UIMessage{
					Type:      ui.Translation,
					Text:      event.Text,
					Timestamp: event.Timestamp,
				})
				continue
			}

			// Финальный результат: отправляем в worker pool.
			job := transJob{event: event}
			select {
			case jobs <- job:
				slog.Debug("задание отправлено в worker pool", "text", event.Text)
			case <-ctx.Done():
				return
			default:
				slog.Warn("worker pool переполнен, задание пропущено", "text", event.Text)
			}

		case tr, ok := <-transResults:
			if !ok {
				// transResults закрыт — worker pool остановлен.
				slog.Info("transResults закрыт, dispatch завершает работу")
				return
			}
			handleTranslationResult(ov, tr)
		}
	}
}

// handleTranslationResult обрабатывает результат перевода:
// отправляет перевод в UI и, если обнаружен вопрос, варианты ответов.
func handleTranslationResult(ov overlay, tr transResult) {
	if tr.err != nil {
		slog.Error("ошибка перевода в dispatch", "error", tr.err, "text", tr.event.Text)
		// Показываем оригинальный текст как fallback.
		ov.AddMessage(ui.UIMessage{
			Type:      ui.Translation,
			Text:      "[ошибка перевода] " + tr.event.Text,
			Timestamp: tr.event.Timestamp,
		})
		return
	}

	// Отправляем перевод в оверлей.
	ov.AddMessage(ui.UIMessage{
		Type:      ui.Translation,
		Text:      tr.translation,
		Timestamp: tr.event.Timestamp,
	})

	// Если вопрос — отправляем варианты ответов.
	if tr.isQuestion && len(tr.answers) > 0 {
		ov.AddMessage(ui.UIMessage{
			Type:      ui.AnswerCandidates,
			Text:      tr.event.Text,
			Answers:   tr.answers,
			Timestamp: tr.event.Timestamp,
		})
	}
}
