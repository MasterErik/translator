package ui

import (
	"testing"

	"gioui.org/unit"
	"gioui.org/widget/material"
)

// oneLineHeightPx — высота однострочного label через overlayLabel (тот же путь,
// что и в зонах) при PxPerSp=1: размеры в px совпадают со sp. Эталон для теста
// таблицы lineHeightPx и для проверки «строка не обрезается».
func oneLineHeightPx(th *material.Theme, fs int) int {
	gtx, _ := newLabelContext(2000)
	return overlayLabel(th, unit.Sp(fs), "Hg").Layout(gtx).Size.Y
}

// TestLineHeightPxTable — Task 7: lineHeightPx(fs) — фактическая измеренная
// высота одной строки текста (offline-рендер label'а тем же шрифтом и той же
// шириной окна), а НЕ эвристика fs*5/4. Таблица фиксирует фактические значения
// для fs=10..24, чтобы регрессия метрики была видна явно.
func TestLineHeightPxTable(t *testing.T) {
	th := material.NewTheme()
	// Зафиксированные фактические значения (PxPerSp=1). Источник — измерение
	// тем же путём (overlayLabel). При обновлении Gio/шрифта тест падёт и
	// покажет новые цифры — это ожидаемо и информативно.
	want := map[int]int{
		10: 14, 11: 16, 12: 17, 13: 18, 14: 20, 15: 22, 16: 23,
		17: 24, 18: 26, 19: 27, 20: 28, 21: 30, 22: 31, 23: 32, 24: 34,
	}
	for fs := 10; fs <= 24; fs++ {
		got := lineHeightPx(fs)
		measured := oneLineHeightPx(th, fs)
		if got != measured {
			t.Errorf("lineHeightPx(%d) = %d, want измеренную %d", fs, got, measured)
		}
		if got != want[fs] {
			t.Errorf("lineHeightPx(%d) = %d, want %d (фактическая высота строки)", fs, got, want[fs])
		}
		if want := fs * 5 / 4; got == want && measured != want {
			t.Errorf("lineHeightPx(%d) совпал с эвристикой fs*5/4=%d — магическая константа не удалена", fs, want)
		}
	}
	// История: множитель больше не 5/4 — высота зоны кратна измеренной строке.
	if lineHeightPx(18) == 18*5/4 {
		t.Error("lineHeightPx(18) = 22 (fs*5/4) — эвристика осталась")
	}
}

// TestZoneHeightsFitVisibleLines — Task 7: высота зоны = k × lineHeightPx(fs)
// вмещает k видимых строк БЕЗ обрезки последней строки. Проверяем на реальном
// рендере k строк: высота зоны ≥ натуральной высоты контента из k строк.
// Ключевой кейс — k = historyVisibleLines (зона 4) и interimVisibleLines (зона 1).
func TestZoneHeightsFitVisibleLines(t *testing.T) {
	th := material.NewTheme()

	// k видимых строк: label из k строк текста («Hg\n...»).
	naturalHeight := func(fs, k int) int {
		txt := "Hg"
		for i := 1; i < k; i++ {
			txt += "\nHg"
		}
		gtx, _ := newLabelContext(2000)
		return overlayLabel(th, unit.Sp(fs), txt).Layout(gtx).Size.Y
	}

	for _, fs := range []int{10, 12, 14, 16, 18, 20} {
		lh := lineHeightPx(fs)

		// Зона 1 (Interim) — interimVisibleLines строк.
		zoneH := lh * interimVisibleLines
		need := naturalHeight(fs, interimVisibleLines)
		if zoneH < need {
			t.Errorf("fs=%d interim: зона %d px < натуральная высота %d строк %d px — последняя строка обрезана",
				fs, zoneH, interimVisibleLines, need)
		}

		// Зона 4 (History) — historyVisibleLines строк.
		histH := historyVisibleHeightPx(fs)
		if want := lh * historyVisibleLines; histH != want {
			t.Errorf("fs=%d: historyVisibleHeightPx = %d, want %d × %d = %d", fs, histH, lh, historyVisibleLines, want)
		}
		needHist := naturalHeight(fs, historyVisibleLines)
		if histH < needHist {
			t.Errorf("fs=%d history: зона %d px < натуральная высота %d строк %d px — последняя строка обрезана",
				fs, histH, historyVisibleLines, needHist)
		}
	}
}

// TestLineHeightPxCached — Task 7: фактическая высота строки измеряется один
// раз на fs и кэшируется (не измеряется на каждый кадр).
func TestLineHeightPxCached(t *testing.T) {
	const fs = 18
	first := lineHeightPx(fs)
	second := lineHeightPx(fs)
	if first != second {
		t.Errorf("lineHeightPx(%d) нестабилен: %d затем %d", fs, first, second)
	}
	lineHeightCache.mu.RLock()
	_, cached := lineHeightCache.m[fs]
	lineHeightCache.mu.RUnlock()
	if !cached {
		t.Errorf("lineHeightPx(%d) не закэширован", fs)
	}
}
