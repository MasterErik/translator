package main

import (
	"context"
	"fmt"
	"time"

	"github.com/mastererik/translator/internal/dispatcher"
	"github.com/mastererik/translator/internal/stt"
)

// workerConfig — параметры воркера.
type workerConfig struct {
	ID          int
	Iterations  int
	WAVData     []byte // сырые PCM-данные (без WAV-заголовка)
	Text        string // известный текст
	Translation string // известный перевод
	UseLLM      bool
	Timeout     time.Duration
	Metrics     *MetricsCollector
	Engine      dispatcher.AnswerGenerator // LLM-движок (может быть nil)
	STTSeed     int64                      // seed для эмулятора (разный на каждой итерации)
}

// runWorker выполняет все итерации воркера последовательно.
func runWorker(ctx context.Context, cfg workerConfig) error {
	for i := 0; i < cfg.Iterations; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := runIteration(ctx, cfg); err != nil {
			return fmt.Errorf("worker %d iteration %d: %w", cfg.ID, i, err)
		}
	}
	return nil
}

// runIteration выполняет одну итерацию:
// Emulator → Dispatcher → MockOverlay → замеры.
func runIteration(ctx context.Context, cfg workerConfig) error {
	iterCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	emulator := stt.NewGladiaEmulator(cfg.Text, cfg.Translation, "ru")
	overlay := &MockOverlay{}

	// Создаём Dispatcher с реальным engine (LLM) или без него.
	var engine dispatcher.AnswerGenerator
	dispCfg := dispatcher.DefaultConfig()
	if cfg.UseLLM {
		engine = cfg.Engine
	} else {
		// Без engine — не запускаем answerWorker (AnswerQueueSize < 0).
		// enqueueQuestion с answerCh == nil делает go GenerateAnswers,
		// но nil-guard в GenerateAnswers безопасно пропускает вызов.
		dispCfg.AnswerQueueSize = -1
	}
	d := dispatcher.New(overlay, engine, nil, dispCfg)

	totalStart := time.Now()

	// Запускаем Dispatcher в горутине — он читает TextStream эмулятора.
	dispatchDone := make(chan struct{}, 1)
	go d.Run(iterCtx, emulator.TextStream(), dispatchDone)

	// Запускаем эмулятор (симулирует connect + начинает приём аудио).
	if err := emulator.Start(iterCtx); err != nil {
		return fmt.Errorf("emulator start: %w", err)
	}

	// Отправляем WAV чанками по 80ms (2560 байт @ 16kHz mono 16-bit).
	chunkSize := 2560
	maxSendBytes := 16000 * 2 * 3 / 2 // 1.5 seconds of audio
	sendData := cfg.WAVData
	if len(sendData) > maxSendBytes {
		sendData = sendData[:maxSendBytes]
	}
	audioCh := emulator.AudioStream()

	for offset := 0; offset < len(sendData); offset += chunkSize {
		end := offset + chunkSize
		if end > len(sendData) {
			end = len(sendData)
		}
		chunk := make([]byte, end-offset)
		copy(chunk, sendData[offset:end])

		select {
		case audioCh <- chunk:
		case <-iterCtx.Done():
			return fmt.Errorf("audio send: %w", iterCtx.Err())
		}
	}

	// Ждём завершения Dispatcher (textStream закрыт эмулятором).
	select {
	case <-dispatchDone:
		// OK
	case <-iterCtx.Done():
		return fmt.Errorf("dispatcher wait: %w", iterCtx.Err())
	}

	// Останавливаем эмулятор (идемпотентен).
	_ = emulator.Stop()

	// Замеряем пользовательские задержки от totalStart до появления в MockOverlay.
	if t := overlay.FirstInterimAt(); !t.IsZero() {
		cfg.Metrics.Record("interim_ui", t.Sub(totalStart))
	}
	if t := overlay.FirstHistoryAt(); !t.IsZero() {
		cfg.Metrics.Record("history_original", t.Sub(totalStart))
	}
	if t := overlay.FirstTranslationAt(); !t.IsZero() {
		cfg.Metrics.Record("translation_ui", t.Sub(totalStart))
	}
	if t := overlay.FirstAnswerAt(); !t.IsZero() {
		cfg.Metrics.Record("answer_ui", t.Sub(totalStart))
	}

	// Общее время итерации.
	cfg.Metrics.Record("total", time.Since(totalStart))

	return nil
}

// readWAVPCM читает WAV-файл и возвращает только PCM-данные (без 44-байтового заголовка).
func readWAVPCM(path string) ([]byte, error) {
	raw, err := readFileBytes(path)
	if err != nil {
		return nil, fmt.Errorf("read wav: %w", err)
	}
	if len(raw) < 44 {
		return nil, fmt.Errorf("wav file too small: %d bytes", len(raw))
	}
	if string(raw[0:4]) != "RIFF" || string(raw[8:12]) != "WAVE" {
		return nil, fmt.Errorf("not a valid WAV file")
	}
	return raw[44:], nil
}
