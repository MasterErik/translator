//go:build cgo

// Package main is the entry point for the Translator application.
// It wires all modules together with graceful shutdown.
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
)

func main() {
	// 1. Load configuration (defaults + environment variables).
	cfg := common.LoadConfig()

	// 2. Initialize structured JSON logger.
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	slog.Info("translator starting",
		"deepgram_model", cfg.DeepgramModel,
		"openai_model", cfg.OpenAIModel,
	)

	// 3. Create STT provider (Deepgram WebSocket).
	deepgram := stt.NewDeepgramProvider(cfg.DeepgramAPIKey, cfg.DeepgramModel)

	// 4. Create LLM provider (OpenAI GPT-4o-mini).
	openaiProv := translator.NewOpenAIProvider(cfg.OpenAIAPIKey, cfg.OpenAIModel)

	// 5. Create translation engine with sliding window.
	engine := translator.NewEngine(openaiProv, cfg.WindowSize)

	// 6. Create session logger (JSON lines + PCM dumps).
	sessLog, err := logger.NewFileSessionLogger(cfg.LogDir)
	if err != nil {
		slog.Error("failed to create session logger", "error", err)
		os.Exit(1)
	}

	// 7. Create UI overlay (stub; replace with internal/ui when GioUI is available).
	overlay := NewOverlay(OverlayConfig{
		Title:        "Translator Overlay",
		Width:        800,
		Height:       200,
		FontSize:     18,
		TopZoneRatio: 0.6,
	})

	// 8. Create audio capture (malgo WASAPI loopback + microphone).
	audioCapture := capture.NewCapture(capture.CaptureConfig{
		BufferSizeMs: 20,
	})

	// 9. Set up graceful shutdown via signal handling.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// 10. Start all services in background goroutines.
	// Start STT provider first (establishes WebSocket connection).
	if err := deepgram.Start(ctx); err != nil {
		slog.Error("failed to start STT provider", "error", err)
		os.Exit(1)
	}

	go runCapture(ctx, audioCapture, deepgram, sessLog)
	go runSTT(ctx, deepgram, engine, overlay, sessLog)
	go runUI(ctx, overlay)

	// 11. Wait for shutdown signal.
	<-ctx.Done()
	slog.Info("shutting down...")

	// 12. Graceful shutdown in order:
	//     a) Capture stops via context cancellation (capture goroutines)
	//     b) STT provider stops (closes WebSocket, drains pumps)
	if err := deepgram.Stop(); err != nil {
		slog.Warn("error stopping STT provider", "error", err)
	}

	//     c) UI overlay stops (window closed, event loop drained)
	overlay.WaitShutdown()
	slog.Info("ui overlay stopped")

	//     d) Session logger flushes all buffers to disk.
	if err := sessLog.Close(); err != nil {
		slog.Error("failed to close session logger", "error", err)
	}

	slog.Info("shutdown complete")
}
