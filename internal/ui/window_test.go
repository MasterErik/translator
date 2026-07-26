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

func TestWindowConstants(t *testing.T) {
	if wsExTransparent != 0x00000020 {
		t.Errorf("WS_EX_TRANSPARENT = 0x%08x, want 0x00000020", wsExTransparent)
	}
	if wsExNoactivate != 0x08000000 {
		t.Errorf("WS_EX_NOACTIVATE = 0x%08x, want 0x08000000", wsExNoactivate)
	}
}
