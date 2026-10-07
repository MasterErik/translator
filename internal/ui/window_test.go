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

// TestWindowConstants удалён: ассертил литералы Win32-констант
// (wsExTransparent == 0x20) — зеркалит объявления, компилятор и так гарантирует.
