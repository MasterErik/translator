package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"sync"
	"time"

	"github.com/joho/godotenv"
	"github.com/mastererik/translator/internal/dispatcher"
	"github.com/mastererik/translator/internal/translator"
)

func main() {
	// Парсинг флагов.
	workers := flag.Int("workers", 8, "количество параллельных воркеров")
	iterations := flag.Int("iterations", 20, "итераций на воркер")
	wavPath := flag.String("wav", "test_speech.wav", "путь к WAV-файлу")
	jsonPath := flag.String("json", "test_speech.json", "путь к JSON с текстом/переводом")
	useLLM := flag.Bool("llm", false, "использовать реальный LLM (иначе только STT)")
	timeout := flag.Duration("timeout", 30*time.Second, "таймаут на одну итерацию")
	outJSON := flag.String("out-json", "", "путь для JSON-отчёта (опционально)")
	outCSV := flag.String("out-csv", "", "путь для CSV-отчёта (опционально)")
	flag.Parse()

	// Читаем WAV (только PCM-данные).
	wavData, err := readWAVPCM(*wavPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading WAV: %v\n", err)
		os.Exit(1)
	}

	// Читаем JSON с текстом/переводом.
	testData, err := readTestJSON(*jsonPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading JSON: %v\n", err)
		os.Exit(1)
	}

	// Теоретический расчёт (1.5 секунды аудио отправляется воркером).
	calc := newLatencyCalc(1.5, *useLLM)
	fmt.Print(calc.report(*workers, *iterations))

	// Создаём коллектор метрик.
	metrics := newMetricsCollector()

	// Настраиваем контекст с graceful shutdown.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// Если LLM включён — создаём engine (один на всех воркеров).
	// Dispatcher сам управляет очередью LLM внутри каждой итерации.
	var engine dispatcher.AnswerGenerator
	if *useLLM {
		engine = setupLLMEngine()
	}

	// Запускаем воркеры.
	fmt.Printf("\nRunning %d workers × %d iterations...\n\n", *workers, *iterations)
	runStart := time.Now()

	var wg sync.WaitGroup
	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			cfg := workerConfig{
				ID:          id,
				Iterations:  *iterations,
				WAVData:     wavData,
				Text:        testData.Text,
				Translation: testData.Translation,
				UseLLM:      *useLLM,
				Timeout:     *timeout,
				Metrics:     metrics,
				Engine:      engine,
				STTSeed:     time.Now().UnixNano() + int64(id),
			}
			if err := runWorker(ctx, cfg); err != nil {
				fmt.Fprintf(os.Stderr, "Worker %d error: %v\n", id, err)
			}
		}(w)
	}

	// Ждём завершения всех воркеров.
	wg.Wait()

	wallClock := time.Since(runStart)
	fmt.Printf("Wall-clock time: %v\n\n", wallClock)

	// Вычисляем метрики.
	computed := metrics.Compute()

	// Детекция выбросов.
	detector := newOutlierDetector()
	stageOrder := []string{"interim_ui", "history_original", "translation_ui"}
	if *useLLM {
		stageOrder = append(stageOrder, "answer_ui")
	}
	stageOrder = append(stageOrder, "total")

	var stages []stageReport
	totalSamples := 0
	criticalOutliers := 0
	var warnings []string

	for _, stageName := range stageOrder {
		s, ok := computed[stageName]
		if !ok {
			continue
		}

		// Детекция выбросов.
		outlierResults := detector.Detect(s.Values)
		outlierCount := CountOutliers(outlierResults)
		criticalCount := CountCriticalOutliers(outlierResults)
		s.Outliers = outlierCount
		criticalOutliers += criticalCount
		totalSamples += s.Count

		// SLA-предупреждения: если outliers > 10% от общего числа.
		if s.Count > 0 && float64(outlierCount)/float64(s.Count) > 0.1 {
			warnings = append(warnings, fmt.Sprintf(
				"%s — %d outliers (%.0f%% of %d samples, SLA breach)",
				stageName, outlierCount, float64(outlierCount)/float64(s.Count)*100, s.Count))
		}

		stages = append(stages, stageReport{
			Stage:    stageName,
			Count:    s.Count,
			P50:      formatMs(s.P50()),
			P95:      formatMs(s.P95()),
			P99:      formatMs(s.P99()),
			Min:      formatMs(s.Min),
			Max:      formatMs(s.Max),
			Mean:     formatMs(s.Mean()),
			StdDev:   formatMs(s.StdDev()),
			Outliers: outlierCount,
		})
	}

	// Сортируем стадии по порядку.
	sort.SliceStable(stages, func(i, j int) bool {
		order := map[string]int{
			"interim_ui": 0, "history_original": 1, "translation_ui": 2,
			"answer_ui": 3, "total": 4,
		}
		return order[stages[i].Stage] < order[stages[j].Stage]
	})

	data := reportData{
		Workers:     *workers,
		Iterations:  *iterations,
		TotalCycles: totalSamples,
		WAVPath:     *wavPath,
		LLMEnabled:  *useLLM,
		Stages:      stages,
		Summary: summaryReport{
			TotalSamples:     totalSamples,
			CriticalOutliers: criticalOutliers,
			Warnings:         warnings,
		},
	}

	stdout, csv, jsonOut := generateReports(data)
	fmt.Print(stdout)

	if *outJSON != "" {
		if err := writeReportFile(*outJSON, jsonOut); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing JSON: %v\n", err)
		} else {
			fmt.Printf("JSON report written to: %s\n", *outJSON)
		}
	}
	if *outCSV != "" {
		if err := writeReportFile(*outCSV, csv); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing CSV: %v\n", err)
		} else {
			fmt.Printf("CSV report written to: %s\n", *outCSV)
		}
	}
}

// testData — структура для test_speech.json.
type testData struct {
	Text        string `json:"text"`
	Translation string `json:"translation"`
	IsQuestion  bool   `json:"is_question"`
}

// readTestJSON читает JSON с известным текстом и переводом.
func readTestJSON(path string) (testData, error) {
	raw, err := readFileBytes(path)
	if err != nil {
		return testData{}, fmt.Errorf("read test json: %w", err)
	}
	var td testData
	if err := json.Unmarshal(raw, &td); err != nil {
		return testData{}, fmt.Errorf("parse test json: %w", err)
	}
	if td.Text == "" {
		return testData{}, fmt.Errorf("test json: empty text field")
	}
	if td.Translation == "" {
		return testData{}, fmt.Errorf("test json: empty translation field")
	}
	return td, nil
}

// setupLLMEngine создаёт LLM engine из переменных окружения.
func setupLLMEngine() dispatcher.AnswerGenerator {
	_ = godotenv.Load()

	baseURL := os.Getenv("LLM_BASE_URL")
	apiKey := os.Getenv("LLM_API_KEY")
	model := os.Getenv("LLM_MODEL")

	if baseURL == "" {
		baseURL = "https://api.z.ai/api/paas/v4/"
	}
	if model == "" {
		model = "glm-4.7-flash"
	}

	prov := translator.NewChatProvider(baseURL, apiKey, model)
	// Не дизейблим thinking — для GLM это ломает подсказки.
	return &llmAdapter{prov: prov}
}

// llmAdapter адаптирует translator.LLMProvider к dispatcher.AnswerGenerator.
type llmAdapter struct {
	prov *translator.ChatProvider
}

func (a *llmAdapter) GenerateAnswers(ctx context.Context, question string) ([]string, error) {
	return a.prov.GenerateAnswers(ctx, question, "")
}

// readFileBytes читает файл целиком в []byte.
func readFileBytes(path string) ([]byte, error) {
	return os.ReadFile(path)
}
