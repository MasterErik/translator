package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

// reportData — полные данные для отчёта.
type reportData struct {
	Workers     int
	Iterations  int
	TotalCycles int
	WAVPath     string
	LLMEnabled  bool
	Stages      []stageReport
	Summary     summaryReport
}

type stageReport struct {
	Stage    string
	Count    int
	P50      string
	P95      string
	P99      string
	Min      string
	Max      string
	Mean     string
	StdDev   string
	Outliers int
}

type summaryReport struct {
	TotalSamples     int
	CriticalOutliers int
	Warnings         []string
}

// generateReports создаёт все форматы отчёта: stdout (таблица), CSV, JSON.
func generateReports(data reportData) (string, string, string) {
	stdout := formatStdout(data)
	csv := formatCSV(data)
	jsonOut := formatJSON(data)
	return stdout, csv, jsonOut
}

// formatStdout возвращает человеко-читаемую таблицу.
func formatStdout(data reportData) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("=== Tail Latency Test ===\n"))
	b.WriteString(fmt.Sprintf("Workers: %d, Iterations: %d, Total samples: %d\n",
		data.Workers, data.Iterations, data.TotalCycles))
	b.WriteString(fmt.Sprintf("WAV: %s\n", data.WAVPath))
	llmStr := "disabled"
	if data.LLMEnabled {
		llmStr = "enabled (sequential, 1 consumer)"
	}
	b.WriteString(fmt.Sprintf("LLM: %s\n\n", llmStr))

	b.WriteString("--- Per-Stage Latency (ms) ---\n")

	// Таблица через tabwriter.
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "Stage\tp50\tp95\tp99\tmin\tmax\toutliers")
	for _, s := range data.Stages {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%d\n",
			s.Stage, s.P50, s.P95, s.P99, s.Min, s.Max, s.Outliers)
	}
	w.Flush()

	// Сводка.
	b.WriteString(fmt.Sprintf("\nCritical outliers: %d\n", data.Summary.CriticalOutliers))
	if len(data.Summary.Warnings) > 0 {
		b.WriteString(fmt.Sprintf("Warnings: %d\n", len(data.Summary.Warnings)))
		for _, warn := range data.Summary.Warnings {
			b.WriteString(fmt.Sprintf("  - %s\n", warn))
		}
	} else {
		b.WriteString("Warnings: 0\n")
	}

	return b.String()
}

// formatCSV возвращает данные в CSV-формате.
func formatCSV(data reportData) string {
	var b strings.Builder
	// Заголовок.
	b.WriteString("stage,count,p50_ms,p95_ms,p99_ms,min_ms,max_ms,mean_ms,stddev_ms,outliers\n")
	for _, s := range data.Stages {
		b.WriteString(fmt.Sprintf("%s,%d,%s,%s,%s,%s,%s,%s,%s,%d\n",
			s.Stage, s.Count, s.P50, s.P95, s.P99, s.Min, s.Max, s.Mean, s.StdDev, s.Outliers))
	}
	return b.String()
}

// formatJSON возвращает полный JSON-отчёт.
func formatJSON(data reportData) string {
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Sprintf(`{"error": "marshal: %s"}`, err.Error())
	}
	return string(out)
}

// writeReportFile пишет строку в файл.
func writeReportFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
