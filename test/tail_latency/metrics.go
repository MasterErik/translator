package main

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

// stageStats хранит агрегированную статистику по одной стадии.
type stageStats struct {
	Count    int
	Min      time.Duration
	Max      time.Duration
	Values   []time.Duration // сырые значения для расчёта перцентилей
	Outliers int             // количество выбросов (заполняется после анализа)
}

// MetricsCollector потокобезопасно собирает latency-замеры.
type MetricsCollector struct {
	mu     sync.Mutex
	stages map[string]*stageStats
}

// newMetricsCollector создаёт новый коллектор.
func newMetricsCollector() *MetricsCollector {
	return &MetricsCollector{
		stages: make(map[string]*stageStats),
	}
}

// Record сохраняет latency-замер для указанной стадии.
func (m *MetricsCollector) Record(stage string, latency time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.stages[stage]
	if !ok {
		s = &stageStats{
			Min:    latency,
			Max:    latency,
			Values: make([]time.Duration, 0, 1024),
		}
		m.stages[stage] = s
	}

	s.Count++
	if latency < s.Min {
		s.Min = latency
	}
	if latency > s.Max {
		s.Max = latency
	}
	s.Values = append(s.Values, latency)
}

// Compute возвращает отсортированную карту стадий с вычисленными перцентилями.
func (m *MetricsCollector) Compute() map[string]*stageStats {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make(map[string]*stageStats, len(m.stages))
	for stage, s := range m.stages {
		cp := &stageStats{
			Count:  s.Count,
			Min:    s.Min,
			Max:    s.Max,
			Values: make([]time.Duration, len(s.Values)),
		}
		copy(cp.Values, s.Values)
		sort.Slice(cp.Values, func(i, j int) bool {
			return cp.Values[i] < cp.Values[j]
		})
		result[stage] = cp
	}
	return result
}

// percentile возвращает p-й перцентиль из отсортированного слайса.
// p — значение от 0 до 100.
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 100 {
		return sorted[len(sorted)-1]
	}
	idx := (p / 100.0) * float64(len(sorted)-1)
	lo := int(math.Floor(idx))
	hi := int(math.Ceil(idx))
	if lo == hi {
		return sorted[lo]
	}
	frac := idx - float64(lo)
	return time.Duration(float64(sorted[lo]) + frac*float64(sorted[hi]-sorted[lo]))
}

// P50 возвращает медиану.
func (s *stageStats) P50() time.Duration { return percentile(s.Values, 50) }

// P95 возвращает 95-й перцентиль.
func (s *stageStats) P95() time.Duration { return percentile(s.Values, 95) }

// P99 возвращает 99-й перцентиль.
func (s *stageStats) P99() time.Duration { return percentile(s.Values, 99) }

// P999 возвращает 99.9-й перцентиль.
func (s *stageStats) P999() time.Duration { return percentile(s.Values, 99.9) }

// Mean возвращает среднее арифметическое.
func (s *stageStats) Mean() time.Duration {
	if len(s.Values) == 0 {
		return 0
	}
	var sum time.Duration
	for _, v := range s.Values {
		sum += v
	}
	return time.Duration(float64(sum) / float64(len(s.Values)))
}

// StdDev возвращает стандартное отклонение.
func (s *stageStats) StdDev() time.Duration {
	if len(s.Values) < 2 {
		return 0
	}
	mean := s.Mean()
	var sumSq float64
	for _, v := range s.Values {
		d := float64(v - mean)
		sumSq += d * d
	}
	variance := sumSq / float64(len(s.Values)-1)
	return time.Duration(math.Sqrt(variance))
}

// formatMs форматирует time.Duration как миллисекунды (целое число).
func formatMs(d time.Duration) string {
	return fmt.Sprintf("%.0f", float64(d)/float64(time.Millisecond))
}
