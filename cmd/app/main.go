//go:build cgo

// Package main — точка входа приложения Translator.
// Связывает все модули: захват аудио, STT, перевод, GioUI-оверлей, логгер.
// Требует CGO для malgo (захват аудио) и GioUI (графический оверлей).
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/mastererik/translator/internal/capture"
	"github.com/mastererik/translator/internal/common"
	"github.com/mastererik/translator/internal/logger"
	"github.com/mastererik/translator/internal/stt"
	"github.com/mastererik/translator/internal/translator"
	"github.com/mastererik/translator/internal/ui"
)

func main() {
	// 1. Загружаем конфигурацию (переменные окружения + .env).
	cfg := common.LoadConfig()

	// 2. Инициализируем структурированный JSON-логгер.
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	slog.Info("translator запускается",
		"deepgram_model", cfg.DeepgramModel,
		"openai_model", cfg.OpenAIModel,
	)

	// 3. Создаём STT-провайдер (Deepgram WebSocket).
	deepgram := stt.NewDeepgramProvider(cfg.DeepgramAPIKey, cfg.DeepgramModel)

	// 4. Создаём LLM-провайдер (OpenAI GPT-4o-mini).
	openaiProv := translator.NewOpenAIProvider(cfg.OpenAIAPIKey, cfg.OpenAIModel)

	// 5. Создаём движок перевода со скользящим окном.
	engine := translator.NewEngine(openaiProv, cfg.WindowSize)

	// 6. Создаём логгер сессии (JSON + PCM-дампы).
	sessLog, err := logger.NewFileSessionLogger(cfg.LogDir)
	if err != nil {
		slog.Error("не удалось создать логгер сессии", "error", err)
		os.Exit(1)
	}

	// 7. Создаём НАСТОЯЩИЙ GioUI-оверлей (прозрачное окно поверх всех окон).
	overlay := ui.NewOverlay(ui.OverlayConfig{
		Title:        "Translator Overlay",
		Width:        800,
		Height:       200,
		FontSize:     18,
		TopZoneRatio: 0.6,
	})

	// 8. Создаём захват аудио (malgo WASAPI: loopback + микрофон).
	audioCapture := capture.NewCapture(capture.CaptureConfig{
		BufferSizeMs: 20,
	})

	// 9. Настраиваем graceful shutdown через сигналы.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// 10. Запускаем STT-провайдер (устанавливает WebSocket-соединение).
	if err := deepgram.Start(ctx); err != nil {
		slog.Error("не удалось запустить STT-провайдер", "error", err)
		os.Exit(1)
	}

	// 11. Запускаем все сервисы в фоновых горутинах.
	go runCapture(ctx, audioCapture, deepgram, sessLog)
	go runSTT(ctx, deepgram, engine, overlay, sessLog)
	go runUI(ctx, overlay)

	// 12. Ждём сигнала завершения.
	slog.Info("translator работает, Ctrl+C для остановки")
	<-ctx.Done()
	slog.Info("завершаем работу...")

	// 13. Корректное завершение в порядке:
	//     a) Захват аудио останавливается через отмену контекста.
	//     b) STT-провайдер закрывает WebSocket.
	if err := deepgram.Stop(); err != nil {
		slog.Warn("ошибка при остановке STT", "error", err)
	}

	//     c) GioUI-оверлей уже остановлен (runUI завершилась по ctx.Done()).

	//     d) Логгер сбрасывает все буферы на диск.
	if err := sessLog.Close(); err != nil {
		slog.Error("ошибка при закрытии логгера", "error", err)
	}

	slog.Info("работа завершена")
}
