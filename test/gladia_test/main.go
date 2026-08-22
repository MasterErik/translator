// Gladia integration test.
// Usage: go run ./test/gladia_test [--wav=test.wav] [--text="Hello world"]
//
// Requires: GLADIA_API_KEY and OPENAI_API_KEY in .env or environment.
package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"github.com/mastererik/translator/internal/logger"
	"io"
	"os"
	"time"

	"github.com/mastererik/translator/internal/common"
	"github.com/mastererik/translator/internal/stt"
	"github.com/mastererik/translator/internal/translator"
)

func main() {
	cfg, err := common.LoadConfigFromYAML("config.yaml")
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "ERROR: config.yaml not found:", err)
		os.Exit(1)
	}

	if cfg.GladiaAPIKey == "" {
		_, _ = fmt.Fprintln(os.Stderr, "ERROR: GLADIA_API_KEY not set in .env or environment")
		os.Exit(1)
	}
	if cfg.LLMAPIKey == "" {
		_, _ = fmt.Fprintln(os.Stderr, "ERROR: LLM_API_KEY (or OPENAI_API_KEY) not set in .env or environment")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// --- Test 1: Gladia STT ---
	fmt.Println("=== TEST 1: Speech-to-Text (Gladia) ===")
	testSTT(ctx, cfg)

	// --- Test 2: OpenAI Answer Generation ---
	fmt.Println("\n=== TEST 2: Answer Generation (GPT-4o-mini) ===")
	testAnswerGeneration(ctx, cfg)
}

func testSTT(ctx context.Context, cfg *common.Config) {
	prov := stt.NewGladiaProvider(stt.GladiaConfig{
		APIKey:     cfg.GladiaAPIKey,
		SourceLang: cfg.SourceLang,
		TargetLang: cfg.TargetLang,
	}, logger.NewNopSessionLogger())

	if err := prov.Start(ctx); err != nil {
		fmt.Printf("  FAIL: Gladia connect: %v\n", err)
		return
	}
	defer prov.Stop()
	fmt.Println("  OK: WebSocket connected to Gladia")

	// Генерируем тестовый WAV если его нет.
	wavPath := "test_speech.wav"
	if _, err := os.Stat(wavPath); os.IsNotExist(err) {
		fmt.Println("  SKIP: no test_speech.wav — run: python generate_test_wav.py")
		return
	}
	pcm, err := readWAVToPCM(wavPath)
	if err != nil {
		fmt.Printf("  SKIP: no test WAV file (%s) — %v\n", wavPath, err)
		fmt.Println("  Generate one with: python generate_test_wav.py")
		return
	}
	fmt.Printf("  Loaded %d bytes PCM from %s\n", len(pcm), wavPath)

	// Send audio in chunks to simulate streaming.
	chunkSize := 640 // 20ms at 16kHz mono
	go func() {
		for i := 0; i < len(pcm); i += chunkSize {
			end := i + chunkSize
			if end > len(pcm) {
				end = len(pcm)
			}
			select {
			case prov.AudioStream() <- pcm[i:end]:
			case <-ctx.Done():
				return
			}
		}
		// 1 секунда тишины в конце — endpointing (0.3s) детектит конец
		// фразы, после чего Gladia выдаёт финальный транскрипт + перевод.
		silence := make([]byte, chunkSize)
		for i := 0; i < 50; i++ { // 50 × 20ms = 1s
			select {
			case prov.AudioStream() <- silence:
			case <-ctx.Done():
				return
			}
		}
	}()

	// Collect results.
	fmt.Println("  Waiting for transcription...")
	var finalText, finalTranslation string
	deadline := time.After(20 * time.Second)

loop:
	for {
		select {
		case event, ok := <-prov.TextStream():
			if !ok {
				break loop
			}
			if event.Error != nil {
				fmt.Printf("  STT error: %v\n", event.Error)
				break loop
			}
			switch {
			case event.ChannelID == "translation":
				finalTranslation = event.Text
				fmt.Printf("  TRANSLATION: %q\n", finalTranslation)
			case event.Event == common.EventEndOfTurn:
				finalText = event.Text
				fmt.Printf("  FINAL: %q\n", finalText)
			case event.Text != "":
				fmt.Printf("  interim: %q\n", event.Text)
			}
			if finalText != "" && finalTranslation != "" {
				break loop
			}
		case <-deadline:
			fmt.Println("  TIMEOUT: no final transcript in 20s")
			break loop
		case <-ctx.Done():
			break loop
		}
	}

	if finalText != "" {
		fmt.Println("  STT RESULT: OK")
	} else {
		fmt.Println("  STT RESULT: no final text (empty audio or silence?)")
	}
	if finalTranslation != "" {
		fmt.Println("  TRANSLATION RESULT: OK")
	} else {
		fmt.Println("  TRANSLATION RESULT: no translation received")
	}
}

func testAnswerGeneration(ctx context.Context, cfg *common.Config) {
	prov := translator.NewChatProvider(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel)

	// Test answer generation.
	fmt.Println("  --- Answer Generation ---")
	q := "What is the difference between a mutex and a channel in Go?"
	cv := "Senior Go developer, 5+ years, expert in concurrency patterns."
	fmt.Printf("  Question: %q\n", q)
	answers, err := prov.GenerateAnswers(ctx, translator.AnswerRequest{Question: q, CandidateContext: cv})
	if err != nil {
		fmt.Printf("  FAIL: %v\n", err)
		return
	}
	for i, a := range answers {
		fmt.Printf("  Hint %d: %s\n", i+1, a)
	}
}

// readWAVToPCM reads a WAV file and returns 16kHz mono PCM bytes suitable for Gladia.
// Handles resampling from any sample rate to 16kHz.
func readWAVToPCM(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	raw, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}

	if len(raw) < 44 || string(raw[0:4]) != "RIFF" {
		return nil, fmt.Errorf("not a valid WAV file")
	}

	sampleRate := int(binary.LittleEndian.Uint32(raw[24:28]))
	channels := int(binary.LittleEndian.Uint16(raw[22:24]))
	bitsPerSample := int(binary.LittleEndian.Uint16(raw[34:36]))

	fmt.Printf("  WAV: %dHz, %dch, %dbit\n", sampleRate, channels, bitsPerSample)

	// Find "data" chunk — search from byte 12 (after RIFF header).
	dataStart := 0
	for i := 12; i < len(raw)-8; i++ {
		if raw[i] == 'd' && raw[i+1] == 'a' && raw[i+2] == 't' && raw[i+3] == 'a' {
			dataStart = i + 8
			break
		}
	}
	if dataStart == 0 {
		return nil, fmt.Errorf("no data chunk found in WAV")
	}

	dataLen := int(binary.LittleEndian.Uint32(raw[dataStart-4 : dataStart]))
	if dataStart+dataLen > len(raw) {
		dataLen = len(raw) - dataStart
	}
	pcmBytes := raw[dataStart : dataStart+dataLen]

	// Resample if needed (simple linear interpolation to 16kHz mono).
	return resamplePCM(pcmBytes, sampleRate, channels, bitsPerSample)
}

// resamplePCM converts arbitrary PCM to 16kHz mono int16 bytes.
func resamplePCM(data []byte, srcRate, channels, bits int) ([]byte, error) {
	if bits != 16 {
		return nil, fmt.Errorf("only 16-bit PCM supported, got %d-bit", bits)
	}

	// Read int16 samples.
	sampleCount := len(data) / 2
	samples := make([]int16, sampleCount)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(data[i*2 : i*2+2]))
	}

	// Convert stereo → mono by averaging L+R.
	var monoSamples []int16
	if channels == 2 {
		monoSamples = make([]int16, sampleCount/2)
		for i := 0; i < len(monoSamples); i++ {
			monoSamples[i] = int16((int32(samples[i*2]) + int32(samples[i*2+1])) / 2)
		}
	} else {
		monoSamples = samples
	}

	// Resample to 16kHz using linear interpolation.
	const dstRate = 16000
	ratio := float64(srcRate) / float64(dstRate)
	dstCount := int(float64(len(monoSamples)) / ratio)
	out := make([]byte, dstCount*2)

	for i := 0; i < dstCount; i++ {
		srcIdx := float64(i) * ratio
		idx := int(srcIdx)
		frac := srcIdx - float64(idx)

		var v float64
		if idx+1 < len(monoSamples) {
			v = float64(monoSamples[idx])*(1-frac) + float64(monoSamples[idx+1])*frac
		} else if idx < len(monoSamples) {
			v = float64(monoSamples[idx])
		}

		binary.LittleEndian.PutUint16(out[i*2:], uint16(int16(v)))
	}

	return out, nil
}
