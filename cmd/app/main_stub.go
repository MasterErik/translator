//go:build !cgo

// Package main — точка входа без CGO (заглушка захвата аудио).
// Использует StubCapture вместо malgo. GioUI оверлей работает.
package main

import (
	"context"
	"fmt"
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
	// 1. Загружаем конфигурацию.
	cfg := common.LoadConfig()

	// 2. Инициализируем структурированный JSON-логгер.
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	slog.Info("translator запускается (stub-захват, без CGO)",
		"deepgram_model", cfg.DeepgramModel,
		"openai_model", cfg.OpenAIModel,
	)

	// 3. STT-провайдер.
	deepgram := stt.NewDeepgramProvider(cfg.DeepgramAPIKey, cfg.DeepgramModel)

	// 4. LLM-провайдер.
	openaiProv := translator.NewOpenAIProvider(cfg.OpenAIAPIKey, cfg.OpenAIModel)

	// 5. Движок перевода.
	engine := translator.NewEngine(openaiProv, cfg.WindowSize)

	// 6. Логгер сессии.
	sessLog, err := logger.NewFileSessionLogger(cfg.LogDir)
	if err != nil {
		slog.Error("не удалось создать логгер сессии", "error", err)
		os.Exit(1)
	}

	// 7. НАСТОЯЩИЙ GioUI-оверлей.
	overlay := ui.NewOverlay(ui.OverlayConfig{
		Title:        "Translator Overlay",
		Width:        800,
		Height:       200,
		FontSize:     18,
		TopZoneRatio: 0.6,
	})

	// 8. Проверяем аудиоустройства (без CGO — только предупреждение).
	devices, err := capture.ListAllDevices()
	if err != nil {
		slog.Warn("список устройств недоступен (без CGO)", "error", err)
	} else {
		slog.Info("доступные аудиоустройства",
			"loopback", devices["loopback"],
			"capture", devices["capture"],
		)
	}

	// Захват-заглушка (тихие PCM-фреймы, 16kHz mono).
	silentFrame := make([]byte, 2560) // 1280 сэмплов × 2 байта (80ms при 16kHz)
	audioCapture := capture.NewStubCapture(
		capture.CaptureConfig{BufferSizeMs: 80},
		silentFrame,
		silentFrame,
		0, // интервал по умолчанию
	)

	// 9. Graceful shutdown.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// 10. Запуск STT.
	if err := deepgram.Start(ctx); err != nil {
		slog.Error("не удалось запустить STT-провайдер", "error", err)
		os.Exit(1)
	}

	// 11. Запуск пайплайна.
	textStream := make(chan common.STTEvent, 16)
	numWorkers := getNumWorkers()

	go runCapture(ctx, audioCapture, deepgram, sessLog, cfg.SaveAudio)
	go runSTT(ctx, deepgram, sessLog, textStream)
	go runDispatch(ctx, textStream, overlay, engine, numWorkers)
	go runUI(ctx, overlay)

	// 12. Ожидание сигнала.
	fmt.Fprintln(os.Stderr, "translator запущен, Ctrl+C для остановки")
	slog.Info("translator работает, Ctrl+C для остановки")
	<-ctx.Done()
	fmt.Fprintln(os.Stderr, "\nзавершаем работу...")
	slog.Info("завершаем работу...")

	// 13. Корректное завершение.
	_ = deepgram.Stop()
	// runUI уже завершилась по ctx.Done().
	if err := sessLog.Close(); err != nil {
		slog.Error("ошибка при закрытии логгера", "error", err)
	}
	slog.Info("работа завершена")
}
