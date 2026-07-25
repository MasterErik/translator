//go:build !cgo

// Package main — точка входа без CGO (заглушка захвата аудио).
// Использует StubCapture вместо malgo. GioUI оверлей работает.
// Использует pipeline.New() с ValidateAudio=false.
package main

import (
	"log/slog"
	"os"

	"github.com/mastererik/translator/internal/capture"
	"github.com/mastererik/translator/internal/common"
	"github.com/mastererik/translator/internal/pipeline"
	"github.com/mastererik/translator/internal/ui"
)

func main() {
	cfg := common.LoadConfig()
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))

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
		TargetLang:    cfg.TargetLang,
		LLMBaseURL:    cfg.LLMBaseURL,
		LLMAPIKey:     cfg.LLMAPIKey,
		LLMModel:      cfg.OpenAIModel,
		MaxTokens:     cfg.MaxTokens,
		OverlayCfg: ui.OverlayConfig{
			Width:    cfg.OverlayWidth,
			Height:   cfg.OverlayHeight,
			FontSize: 18,
			MaxLines: cfg.OverlayMaxLines,
		},
		LogDir:    cfg.LogDir,
		SaveAudio: cfg.SaveAudio,
	})
	if err != nil {
		slog.Error("не удалось создать pipeline", "err", err)
		os.Exit(1)
	}

	if err := p.Run(); err != nil {
		slog.Error("pipeline завершился с ошибкой", "err", err)
		os.Exit(1)
	}
}
