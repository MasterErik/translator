//go:build cgo

// Package main — точка входа приложения Translator.
// Требует CGO для malgo (захват аудио) и GioUI (графический оверлей).
// Использует pipeline.New() для оркестрации всех компонентов.
package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"syscall"
	"unsafe"

	"github.com/mastererik/translator/internal/capture"
	"github.com/mastererik/translator/internal/common"
	"github.com/mastererik/translator/internal/pipeline"
	"github.com/mastererik/translator/internal/ui"
)

func main() {
	cfg, err := common.LoadConfigFromYAML("config.yaml")
	if err != nil {
		fatalError("не удалось загрузить config.yaml", err)
	}
	// В Windows GUI-режиме консоль отсутствует — логи только в CSV сессии.
	slog.SetDefault(slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: cfg.SlogLevel()})))

	p, err := pipeline.New(pipeline.Config{
		Capturer: capture.NewCapture(capture.CaptureConfig{
			BufferSizeMs:       80,
			LoopbackDeviceName: cfg.LoopbackDeviceName,
			MicDeviceName:      cfg.MicDeviceName,
		}),
		ValidateAudio: true,
		LoopbackName:  cfg.LoopbackDeviceName,
		MicName:       cfg.MicDeviceName,
		GladiaAPIKey:  cfg.GladiaAPIKey,
		SourceLang:    cfg.SourceLang,
		TargetLang:    cfg.TargetLang,
		SystemPrompt:  cfg.SystemPrompt,
		CustomVocabulary: cfg.CustomVocabulary,
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
		fatalError("не удалось создать pipeline", err)
	}

	if err := p.Run(); err != nil {
		fatalError("pipeline завершился с ошибкой", err)
	}
}

// fatalError показывает MessageBox в GUI-режиме и завершает процесс.
// В GUI-режиме (windowsgui) нет консоли — os.Stderr недоступен.
func fatalError(context string, err error) {
	msg := fmt.Sprintf("%s: %v", context, err)
	slog.Error(context, "err", err)

	// MessageBoxW — единственный способ показать ошибку в GUI-режиме.
	user32 := syscall.NewLazyDLL("user32.dll")
	msgBox := user32.NewProc("MessageBoxW")
	title, _ := syscall.UTF16PtrFromString("Translator — фатальная ошибка")
	body, _ := syscall.UTF16PtrFromString(msg)
	msgBox.Call(0, uintptr(unsafe.Pointer(body)), uintptr(unsafe.Pointer(title)), 0x10) // MB_ICONERROR

	os.Exit(1)
}
