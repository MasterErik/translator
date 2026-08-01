package main

import (
	"fmt"
	"time"
)

// latencyCalc содержит теоретическую модель задержек.
type latencyCalc struct {
	audioDurationSec float64
	enableLLM        bool

	// Эмулятор (этапы внутри sendEvents).
	interimBase     time.Duration
	finalBase       time.Duration
	translationBase time.Duration

	// LLM (через Dispatcher → answerWorker → engine).
	llmBase time.Duration
}

func newLatencyCalc(audioDurationSec float64, enableLLM bool) *latencyCalc {
	return &latencyCalc{
		audioDurationSec: audioDurationSec,
		enableLLM:        enableLLM,
		interimBase:      80 * time.Millisecond,
		finalBase:        400 * time.Millisecond,
		translationBase:  15 * time.Millisecond,
		llmBase:          1500 * time.Millisecond,
	}
}

// workerTotal возвращает теоретическое время одного воркера (медиана).
// connect удалён — в production он делается один раз при старте, не на каждое предложение.
func (c *latencyCalc) workerTotal() time.Duration {
	total := time.Duration(c.audioDurationSec * float64(time.Second))
	total += c.interimBase * 2
	total += c.finalBase
	total += c.translationBase
	if c.enableLLM {
		total += c.llmBase
	}
	return total
}

// wallclockEstimate — оценка wall-clock времени для всего теста.
//
//	T_total = max(T_worker) × iterations + N_questions × T_llm
//
// Параллельная часть: все N workers делают M итераций одновременно.
// Последовательная часть: LLM — один consumer в каждом Dispatcher, суммарно N×M запросов.
func (c *latencyCalc) wallclockEstimate(workers, iterations int) time.Duration {
	workerTime := c.workerTotal()
	parallelPart := workerTime * time.Duration(iterations)

	var llmPart time.Duration
	if c.enableLLM {
		nQuestions := workers * iterations
		llmPart = time.Duration(nQuestions) * c.llmBase
	}

	return parallelPart + llmPart
}

// report возвращает строковый отчёт с теоретическими оценками и таймлайном.
func (c *latencyCalc) report(workers, iterations int) string {
	workerTotal := c.workerTotal()
	wallclock := c.wallclockEstimate(workers, iterations)
	nQuestions := 0
	if c.enableLLM {
		nQuestions = workers * iterations
	}

	timeline := c.buildTimeline()

	return fmt.Sprintf(`
Theoretical Latency Model
=========================
WAV duration:           %.1fs
Workers × iterations:   %d × %d = %d samples
LLM questions:          %d (sequential, 1 consumer per Dispatcher)

Per-worker timeline (median):
%s

Per-worker total:       %v

Wall-clock estimate:    %v
  = T_worker(%.0fms) × %d iter       = %v (parallel)
  + %d questions × T_llm(%.0fms)     = %v (sequential LLM)

Max goroutines:          %d (N workers + Dispatcher answerWorker + main)
Bottleneck:              %s
`,
		c.audioDurationSec,
		workers, iterations, workers*iterations,
		nQuestions,

		timeline,

		workerTotal,

		wallclock,
		float64(workerTotal/time.Millisecond), iterations,
		workerTotal*time.Duration(iterations),
		nQuestions, float64(c.llmBase/time.Millisecond),
		time.Duration(nQuestions)*c.llmBase,

		workers+2,
		c.bottleneck(workers, iterations),
	)
}

// buildTimeline строит ASCII-таймлайн одного воркера с привязкой к UI-зонам.
func (c *latencyCalc) buildTimeline() string {
	audioMs := c.audioDurationSec * 1000
	interimMs := float64(c.interimBase/time.Millisecond) * 2
	finalMs := float64(c.finalBase / time.Millisecond)
	transMs := float64(c.translationBase / time.Millisecond)

	totalMs := audioMs + interimMs + finalMs + transMs

	var sb string
	sb += fmt.Sprintf("  t=%-5.0f audio send   (PCM chunks → эмулятор, 80ms each)\n", audioMs)
	sb += fmt.Sprintf("  t=%-5.0f interim_ui   (зона 1: первая транскрибация)\n", audioMs+float64(c.interimBase/time.Millisecond))
	sb += fmt.Sprintf("  t=%-5.0f interim #2   (зона 1: обновление)\n", audioMs+float64(c.interimBase/time.Millisecond)*2)
	sb += fmt.Sprintf("  t=%-5.0f history_original (зона 3: оригинал + перевод, парой)\n", audioMs+interimMs+finalMs+transMs)
	sb += fmt.Sprintf("  t=%-5.0f translation_ui   (зона 2: перевод, ← %dms после финала)\n", audioMs+interimMs+finalMs+transMs, c.translationBase/time.Millisecond)

	if c.enableLLM {
		llmMs := float64(c.llmBase / time.Millisecond)
		sb += fmt.Sprintf("  t=%-5.0f answer_ui    (зона 4: подсказки LLM)\n", totalMs+llmMs)
		totalMs += llmMs
	}

	_ = totalMs
	return sb
}

// bottleneck возвращает описание узкого места.
func (c *latencyCalc) bottleneck(workers, iterations int) string {
	if c.enableLLM {
		nq := workers * iterations
		llmTime := time.Duration(nq) * c.llmBase
		return fmt.Sprintf("LLM queue: %d sequential calls × %.0fs = %v (answerWorker в каждом Dispatcher)",
			nq, c.llmBase.Seconds(), llmTime)
	}
	workerTime := c.workerTotal()
	return fmt.Sprintf("STT emulator: %d workers parallel, %v per iteration",
		workers, workerTime)
}
