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
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"github.com/mastererik/translator/internal/capture"
	"github.com/mastererik/translator/internal/common"
	"github.com/mastererik/translator/internal/pipeline"
	"github.com/mastererik/translator/internal/ui"
)

// fatalLogDir — каталог для фатальных логов, переживающих GUI-режим.
const fatalLogDir = "logs"

// fatalLogFile — открытый файл фатальных логов, используемый fatalError для
// прямой записи в обход slog. nil, если файл открыть не удалось.
var fatalLogFile *os.File

func main() {
	// В Windows GUI-режиме (windowsgui) консоли нет — stdout/stderr недоступны.
	// Поэтому до всего остального открываем файл фатальных логов и направляем
	// туда structured-логи, чтобы ошибки старта (конфиг, ключи, инициализация)
	// были видны после аварийного завершения.
	// Уровень по умолчанию info; после загрузки конфига уточняем его согласно
	// cfg.SlogLevel(). Если файл открыть не удалось — не падаем, откатываемся
	// на io.Discard.
	fatalLogPath := filepath.Join(fatalLogDir, fmt.Sprintf("fatal-%s.log", time.Now().Format("2006-01-02_15-04-05")))
	f, openErr := openFatalLog(fatalLogPath)
	if openErr != nil {
		// Консоли нет — сообщить некуда, но процесс продолжает работу.
		fatalLogFile = nil
		slog.SetDefault(slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo})))
	} else {
		fatalLogFile = f
		defer func() { _ = fatalLogFile.Close() }()
		slog.SetDefault(slog.New(slog.NewJSONHandler(fatalLogFile, &slog.HandlerOptions{Level: slog.LevelInfo})))
	}

	cfg, err := common.LoadConfigFromYAML("config.yaml")
	if err != nil {
		fatalError("не удалось загрузить config.yaml", err)
	}
	// Конфиг загружен — уточняем уровень логирования по cfg.LogLevel.
	slog.SetDefault(slog.New(slog.NewJSONHandler(fatalLogSink(fatalLogFile), &slog.HandlerOptions{Level: cfg.SlogLevel()})))

	p, err := pipeline.New(pipeline.Config{
		Capturer: capture.NewCapture(capture.CaptureConfig{
			BufferSizeMs:       80,
			LoopbackDeviceName: cfg.LoopbackDeviceName,
			MicDeviceName:      cfg.MicDeviceName,
			EnableMic:          cfg.SaveAudio,
		}),
		ValidateAudio:        true,
		LoopbackName:         cfg.LoopbackDeviceName,
		MicName:              cfg.MicDeviceName,
		GladiaAPIKey:         cfg.GladiaAPIKey,
		SourceLang:           cfg.SourceLang,
		TargetLang:           cfg.TargetLang,
		LLMBaseURL:           cfg.LLMBaseURL,
		LLMAPIKey:            cfg.LLMAPIKey,
		LLMModel:             cfg.LLMModel,
		MaxTokens:            cfg.MaxTokens,
		CandidateContextFile: cfg.CandidateContextFile,
		CandidateContext:     cfg.CandidateContext,
		Hints:                cfg.Hints,
		RecentTurns:          cfg.Conversation.RecentTurns,
		MaxContextTokens:     cfg.Conversation.MaxContextTokens,
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

// fatalLogSink возвращает writer для slog: открытый файл либо io.Discard.
func fatalLogSink(f *os.File) io.Writer {
	if f == nil {
		return io.Discard
	}
	return f
}

// openFatalLog создаёт каталог и открывает файл фатальных логов на дозапись.
func openFatalLog(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create fatal log dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open fatal log %s: %w", path, err)
	}
	return f, nil
}

// writeFatalLog пишет фатальное сообщение напрямую в файл (минуя slog),
// чтобы ошибка сохранилась, даже если slog-хендлер не настроен или упал.
// Вызывается перед os.Exit, поэтому f.Sync() гарантирует попадание на диск.
func writeFatalLog(f *os.File, context string, err error) error {
	if f == nil {
		return nil
	}
	line := fmt.Sprintf("%s	%s	%s: %v\n",
		time.Now().Format(time.RFC3339), "FATAL", context, err)
	if _, werr := f.WriteString(line); werr != nil {
		return fmt.Errorf("write fatal log: %w", werr)
	}
	if serr := f.Sync(); serr != nil {
		return fmt.Errorf("sync fatal log: %w", serr)
	}
	return nil
}

// fatalError показывает MessageBox в GUI-режиме и завершает процесс.
// В GUI-режиме (windowsgui) нет консоли — os.Stderr недоступен.
func fatalError(context string, err error) {
	msg := fmt.Sprintf("%s: %v", context, err)
	slog.Error(context, "err", err)
	// Дублируем в файл фатальных логов на случай, если slog-хендлер не работает.
	if werr := writeFatalLog(fatalLogFile, context, err); werr != nil {
		slog.Error("writeFatalLog failed", "err", werr)
	}

	// MessageBoxW — единственный способ показать ошибку в GUI-режиме.
	user32 := syscall.NewLazyDLL("user32.dll")
	msgBox := user32.NewProc("MessageBoxW")
	title, _ := syscall.UTF16PtrFromString("Translator — фатальная ошибка")
	body, _ := syscall.UTF16PtrFromString(msg)
	msgBox.Call(0, uintptr(unsafe.Pointer(body)), uintptr(unsafe.Pointer(title)), 0x10) // MB_ICONERROR

	os.Exit(1)
}
