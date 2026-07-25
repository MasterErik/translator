//go:build cgo

// Package main — точка входа приложения Translator.
// Требует CGO для malgo (захват аудио) и GioUI (графический оверлей).
// Использует pipeline.New() для оркестрации всех компонентов.
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

	p, err := pipeline.New(pipeline.Config{
		Capturer: capture.NewCapture(capture.CaptureConfig{
			BufferSizeMs:       80,
			LoopbackDeviceName: cfg.LoopbackDeviceName,
			MicDeviceName:      cfg.MicDeviceName,
		}),
		ValidateAudio:  true,
		LoopbackName:   cfg.LoopbackDeviceName,
		MicName:        cfg.MicDeviceName,
		DeepgramAPIKey: cfg.DeepgramAPIKey,
		DeepgramModel:  cfg.DeepgramModel,
		LLMBaseURL:     cfg.LLMBaseURL,
		LLMAPIKey:      cfg.LLMAPIKey,
		LLMModel:       cfg.OpenAIModel,
		MaxTokens:      cfg.MaxTokens,
		WindowSize:     cfg.WindowSize,
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
