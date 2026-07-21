//go:build !cgo

// Package main provides a non-CGo entry point that uses the stub capture
// implementation. This is useful for testing the wiring on systems without
// a C toolchain. The real entry point is in main.go (requires cgo).
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
)

func main() {
	// 1. Load configuration.
	cfg := common.LoadConfig()

	// 2. Initialize structured JSON logger.
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	slog.Info("translator starting (stub capture, no CGo)",
		"deepgram_model", cfg.DeepgramModel,
		"openai_model", cfg.OpenAIModel,
	)

	// 3. Create STT provider.
	deepgram := stt.NewDeepgramProvider(cfg.DeepgramAPIKey, cfg.DeepgramModel)

	// 4. Create LLM provider.
	openaiProv := translator.NewOpenAIProvider(cfg.OpenAIAPIKey, cfg.OpenAIModel)

	// 5. Create translation engine.
	engine := translator.NewEngine(openaiProv, cfg.WindowSize)

	// 6. Create session logger.
	sessLog, err := logger.NewFileSessionLogger(cfg.LogDir)
	if err != nil {
		slog.Error("failed to create session logger", "error", err)
		os.Exit(1)
	}

	// 7. Create UI overlay.
	overlay := NewOverlay(OverlayConfig{
		Title:        "Translator Overlay",
		Width:        800,
		Height:       200,
		FontSize:     18,
		TopZoneRatio: 0.6,
	})

	// 8. Create stub audio capture (silent PCM @ 16kHz mono).
	silentFrame := make([]byte, 640) // 320 samples × 2 bytes
	audioCapture := capture.NewStubCapture(
		capture.CaptureConfig{BufferSizeMs: 20},
		silentFrame,
		silentFrame,
		0, // Use default frame interval.
	)

	// 9. Graceful shutdown via signals.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// 10. Start STT provider.
	if err := deepgram.Start(ctx); err != nil {
		slog.Error("failed to start STT provider", "error", err)
		os.Exit(1)
	}

	// 11. Launch pipeline goroutines.
	go runCapture(ctx, audioCapture, deepgram, sessLog)
	go runSTT(ctx, deepgram, engine, overlay, sessLog)
	go runUI(ctx, overlay)

	// 12. Wait for shutdown signal.
	slog.Info("translator running, press Ctrl+C to stop")
	<-ctx.Done()
	fmt.Fprintln(os.Stderr, "\nshutting down...")
	slog.Info("shutting down...")

	// 13. Graceful shutdown.
	_ = deepgram.Stop()
	overlay.WaitShutdown()
	if err := sessLog.Close(); err != nil {
		slog.Error("failed to close session logger", "error", err)
	}
	slog.Info("shutdown complete")
}
