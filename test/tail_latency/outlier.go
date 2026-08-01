package main

import (
	"math"
	"time"
)

// outlierDetector выполняет детекцию выбросов двумя методами: IQR и Z-score.
type outlierDetector struct {
	iqrMultiplier  float64 // обычно 1.5
	zscoreThreshold float64 // обычно 3.0
}

// newOutlierDetector создаёт детектор со стандартными параметрами.
func newOutlierDetector() *outlierDetector {
	return &outlierDetector{
		iqrMultiplier:   1.5,
		zscoreThreshold: 3.0,
	}
}

// OutlierResult — результат детекции для одного значения.
type OutlierResult struct {
	Value     time.Duration
	IsIQR     bool // выброс по IQR
	IsZScore  bool // выброс по Z-score
	ZScore    float64
}

// Detect возвращает результаты детекции для слайса значений.
// Возвращает слайс той же длины, что и values.
func (d *outlierDetector) Detect(values []time.Duration) []OutlierResult {
	if len(values) < 4 {
		// Слишком мало данных для осмысленной детекции.
		return make([]OutlierResult, len(values))
	}

	// Вычисляем квартили.
	q1, q3 := quartiles(values)
	iqr := q3 - q1
	lowerIQR := q1 - time.Duration(float64(iqr)*d.iqrMultiplier)
	upperIQR := q3 + time.Duration(float64(iqr)*d.iqrMultiplier)

	// Вычисляем среднее и стандартное отклонение.
	mean := mean(values)
	stddev := stddev(values, mean)

	results := make([]OutlierResult, len(values))
	for i, v := range values {
		var zscore float64
		if stddev > 0 {
			zscore = math.Abs(float64(v-mean) / float64(stddev))
		}

		results[i] = OutlierResult{
			Value:    v,
			IsIQR:    v < lowerIQR || v > upperIQR,
			IsZScore: zscore > d.zscoreThreshold,
			ZScore:   zscore,
		}
	}

	return results
}

// CountOutliers возвращает количество выбросов (IQR или Z-score).
func CountOutliers(results []OutlierResult) int {
	count := 0
	for _, r := range results {
		if r.IsIQR || r.IsZScore {
			count++
		}
	}
	return count
}

// CountCriticalOutliers возвращает количество значений, которые являются
// выбросами по ОБОИМ методам одновременно.
func CountCriticalOutliers(results []OutlierResult) int {
	count := 0
	for _, r := range results {
		if r.IsIQR && r.IsZScore {
			count++
		}
	}
	return count
}

// quartiles возвращает Q1 и Q3 для отсортированного слайса.
func quartiles(sorted []time.Duration) (time.Duration, time.Duration) {
	n := len(sorted)
	if n == 0 {
		return 0, 0
	}

	// Линейная интерполяция для Q1 и Q3.
	q1 := interpolate(sorted, float64(n-1)*0.25)
	q3 := interpolate(sorted, float64(n-1)*0.75)

	return q1, q3
}

// interpolate возвращает значение по дробному индексу с линейной интерполяцией.
func interpolate(sorted []time.Duration, idx float64) time.Duration {
	lo := int(math.Floor(idx))
	hi := int(math.Ceil(idx))
	if lo < 0 {
		lo = 0
	}
	if hi >= len(sorted) {
		hi = len(sorted) - 1
	}
	if lo == hi {
		return sorted[lo]
	}
	frac := idx - float64(lo)
	return time.Duration(float64(sorted[lo]) + frac*float64(sorted[hi]-sorted[lo]))
}

// mean — среднее арифметическое.
func mean(values []time.Duration) time.Duration {
	if len(values) == 0 {
		return 0
	}
	var sum time.Duration
	for _, v := range values {
		sum += v
	}
	return time.Duration(float64(sum) / float64(len(values)))
}

// stddev — стандартное отклонение (выборочное, Bessel-corrected).
func stddev(values []time.Duration, m time.Duration) time.Duration {
	if len(values) < 2 {
		return 0
	}
	var sumSq float64
	for _, v := range values {
		d := float64(v - m)
		sumSq += d * d
	}
	variance := sumSq / float64(len(values)-1)
	return time.Duration(math.Sqrt(variance))
}
