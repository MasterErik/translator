//go:build windows

package ui

import (
	"testing"
)

func TestFindWindowByPID(t *testing.T) {
	hwnd, err := findWindowByPID()
	if err == nil && hwnd == 0 {
		t.Error("findWindowByPID: expected error or non-zero hwnd")
	}
}

func TestSetNoActivate_InvalidHWND(t *testing.T) {
	err := setNoActivate(0)
	if err == nil {
		t.Error("setNoActivate(0) should return error")
	}
}

// TestApplyWindowStylesSkipsInDebugInput — debug-режим ручного ввода
// (DEBUG_INPUT=1) делает окно кликабельным и фокусируемым: NOACTIVATE и
// TRANSPARENT к нему НЕ применяются. Это осознанное поведение — иначе Editor
// не получит фокус клавиатуры и ручной ввод не заработает.
//
// Проверка на уровне решения (не в реальном app.Window): счётчик взят из
// applyWindowStylesWith, который Run вызывает ровно тем же условием
// `if !o.cfg.DebugInput`. Стили идут через tryApplyStyles → setNoActivate, а
// window.go — windows-only и без зависимости от cfg, поэтому пробрасывать флаг
// внутрь applyWindowStyles ради одного if не нужно.
func TestApplyWindowStylesSkipsInDebugInput(t *testing.T) {
	tests := []struct {
		name       string
		debugInput bool
		wantCalls  int
	}{
		{name: "обычный режим — стили применяются", debugInput: false, wantCalls: 1},
		{name: "debug-режим — стили не применяются", debugInput: true, wantCalls: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			applyWindowStylesWith(func() { calls++ }, tc.debugInput)
			if calls != tc.wantCalls {
				t.Errorf("applyWindowStyles вызван %d раз, want %d (debugInput=%v)", calls, tc.wantCalls, tc.debugInput)
			}
		})
	}
}

// TestOverlayRunStyleGate — тот же гейт, но у самого Overlay: без cfg.DebugInput
// стили применяются (прежнее поведение), с cfg.DebugInput — нет. Оба состояния
// проверяются на одном и том же методе, которым пользуется Run.
func TestOverlayRunStyleGate(t *testing.T) {
	tests := []struct {
		name       string
		debugInput bool
		wantCalls  int
	}{
		{name: "DebugInput=false", debugInput: false, wantCalls: 1},
		{name: "DebugInput=true", debugInput: true, wantCalls: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := &Overlay{cfg: OverlayConfig{Width: 800, Height: 650, FontSize: 18, DebugInput: tc.debugInput}}
			calls := 0
			o.applyWindowStylesWith(func() { calls++ })
			if calls != tc.wantCalls {
				t.Errorf("Overlay.applyWindowStylesWith вызван %d раз, want %d", calls, tc.wantCalls)
			}
		})
	}
}

// TestWindowConstants удалён: ассертил литералы Win32-констант
// (wsExTransparent == 0x20) — зеркалит объявления, компилятор и так гарантирует.
