// Package main — worker pool для асинхронного перевода.
package main

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/mastererik/translator/internal/translator"
)

// startWorkers запускает пул из numWorkers горутин, каждая из которых
// читает задания из jobs, вызывает движок перевода и отправляет результат
// в transResults. Возвращает канал transResults для чтения dispatch-узлом.
//
// Канал transResults автоматически закрывается после завершения ВСЕХ worker'ов.
// Это происходит либо при закрытии канала jobs (все worker'ы выходят при ok=false),
// либо при отмене контекста (worker'ы выходят по ctx.Done()).
func startWorkers(ctx context.Context, engine *translator.TranslationEngine, jobs <-chan transJob, numWorkers int) <-chan transResult {
	transResults := make(chan transResult, numWorkers*2)
	var wg sync.WaitGroup

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go runWorker(ctx, engine, jobs, transResults, i, &wg)
	}

	// Горутина, закрывающая transResults после остановки всех worker'ов.
	go func() {
		wg.Wait()
		close(transResults)
	}()

	return transResults
}

// runWorker — одна горутина worker pool. Читает задания из jobs,
// вызывает engine.ProcessFinalTranscript() и отправляет результат
// в transResults. При отмене контекста корректно завершается.
func runWorker(ctx context.Context, engine *translator.TranslationEngine, jobs <-chan transJob, transResults chan<- transResult, id int, wg *sync.WaitGroup) {
	defer wg.Done()
	slog.Debug("worker запущен", "worker_id", id)

	for {
		select {
		case <-ctx.Done():
			slog.Debug("worker остановлен", "worker_id", id)
			return
		case job, ok := <-jobs:
			if !ok {
				slog.Debug("канал заданий закрыт, worker остановлен", "worker_id", id)
				return
			}

			slog.Debug("worker обрабатывает задание",
				"worker_id", id,
				"text", job.event.Text,
			)

			// Выполняем перевод (блокирующий вызов GPT API).
			result, err := engine.ProcessFinalTranscript(ctx, job.event.Text)

			tr := transResult{
				event: job.event,
			}

			if err != nil {
				slog.Error("перевод не удался",
					"worker_id", id,
					"error", err,
					"text", job.event.Text,
				)
				tr.err = err
			} else {
				tr.translation = result.Translation
				tr.isQuestion = result.IsQuestion
				tr.answers = result.GetAnswers()

				// Если это вопрос — ждём асинхронной генерации подсказок.
				if result.IsQuestion {
					tr.answers = waitForAnswers(ctx, result)
				}
			}

			// Отправляем результат в dispatch (неблокирующая).
			select {
			case transResults <- tr:
			case <-ctx.Done():
				return
			default:
				slog.Warn("transResults переполнен, результат пропущен",
					"worker_id", id,
					"text", job.event.Text,
				)
			}
		}
	}
}

// getNumWorkers возвращает количество worker'ов из переменной окружения
// TRANSLATION_WORKERS. Если переменная не задана или некорректна,
// возвращает значение по умолчанию (3).
func getNumWorkers() int {
	return getEnvInt("TRANSLATION_WORKERS", 3)
}

// getEnvInt читает целочисленную переменную окружения.
// Если переменная не задана или не является числом, возвращает defaultVal.
func getEnvInt(key string, defaultVal int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultVal
}

// waitForAnswers ожидает асинхронной генерации подсказок для вопроса.
// Опрашивает result.GetAnswers() с интервалом 100ms до 10 попыток.
// При отмене контекста возвращает то, что успело сгенерироваться.
func waitForAnswers(ctx context.Context, result *translator.TranslationResult) []string {
	for i := 0; i < 10; i++ {
		answers := result.GetAnswers()
		if len(answers) > 0 {
			return answers
		}
		select {
		case <-ctx.Done():
			return answers
		case <-time.After(100 * time.Millisecond):
		}
	}
	slog.Warn("таймаут генерации подсказок")
	return result.GetAnswers()
}
