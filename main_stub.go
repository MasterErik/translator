//go:build !cgo

// Package main — точка входа без CGO (заглушка захвата аудио).
// Использует StubCapture вместо malgo. GioUI оверлей работает.
// Использует pipeline.New() с ValidateAudio=false.
package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/mastererik/translator/internal/capture"
	"github.com/mastererik/translator/internal/common"
	"github.com/mastererik/translator/internal/pipeline"
	"github.com/mastererik/translator/internal/ui"
)

func main() {
	cfg, err := common.LoadConfigFromYAML("config.yaml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: не удалось загрузить config.yaml: %v\n", err)
		slog.Error("не удалось загрузить config.yaml", "err", err)
		os.Exit(1)
	}
	// Консольный вывод только при LOG_LEVEL=debug (для отладки).
	slogW := io.Discard
	if cfg.LogLevel == "debug" {
		slogW = os.Stderr
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(slogW, &slog.HandlerOptions{Level: cfg.SlogLevel()})))

	// Тихий PCM-фрейм: 80ms @ 16kHz mono s16le = 2560 байт.
	silentFrame := make([]byte, 2560)

	p, err := pipeline.New(pipeline.Config{
		Capturer: capture.NewStubCapture(
			capture.CaptureConfig{BufferSizeMs: 80},
			silentFrame,
			silentFrame,
			0, // интервал по умолчанию
		),
		ValidateAudio: false,
		LoopbackName:  cfg.LoopbackDeviceName,
		MicName:       cfg.MicDeviceName,
		GladiaAPIKey:  cfg.GladiaAPIKey,
		SourceLang:    cfg.SourceLang,
		TargetLang:    cfg.TargetLang,
		LLMBaseURL:    cfg.LLMBaseURL,
		LLMAPIKey:     cfg.LLMAPIKey,
		LLMModel:      cfg.LLMModel,
		MaxTokens:     cfg.MaxTokens,
		OverlayCfg: ui.OverlayConfig{
			Width:    cfg.OverlayWidth,
			Height:   cfg.OverlayHeight,
			FontSize: 18,
		},
		LogDir:    cfg.LogDir,
		SaveAudio: cfg.SaveAudio,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: не удалось создать pipeline: %v\n", err)
		slog.Error("не удалось создать pipeline", "err", err)
		os.Exit(1)
	}

	if err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: pipeline завершился с ошибкой: %v\n", err)
		slog.Error("pipeline завершился с ошибкой", "err", err)
		os.Exit(1)
	}
}
