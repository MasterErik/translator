//go:build windows

package ui

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// Extended window styles for the overlay window.
const (
	// WS_EX_TRANSPARENT makes the window transparent to mouse clicks.
	wsExTransparent = 0x00000020

	// WS_EX_NOACTIVATE prevents the window from taking keyboard focus.
	wsExNoactivate = 0x08000000
)

var (
	user32                       = syscall.NewLazyDLL("user32.dll")
	procGetWindowLongPtrW        = user32.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtrW        = user32.NewProc("SetWindowLongPtrW")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
)

// setNoActivate applies WS_EX_NOACTIVATE and WS_EX_TRANSPARENT to hwnd.
// This prevents the overlay from stealing focus or capturing mouse input.
func setNoActivate(hwnd uintptr) error {
	if hwnd == 0 {
		return fmt.Errorf("setNoActivate: invalid window handle (0)")
	}

	exStyle, _, _ := procGetWindowLongPtrW.Call(hwnd, ^uintptr(19)) // GWL_EXSTYLE
	if exStyle == 0 {
		return fmt.Errorf("setNoActivate: GetWindowLongPtrW(GWL_EXSTYLE) returned 0")
	}

	newExStyle := uint32(exStyle) | wsExTransparent | wsExNoactivate
	ret, _, _ := procSetWindowLongPtrW.Call(hwnd, ^uintptr(19), uintptr(newExStyle))
	if ret == 0 {
		return fmt.Errorf("setNoActivate: SetWindowLongPtrW failed")
	}
	return nil
}

// findWindowByPID locates the first top-level window of this process via EnumWindows.
func findWindowByPID() (uintptr, error) {
	pid := uintptr(os.Getpid())
	var found uintptr

	cb := syscall.NewCallback(func(hwnd syscall.Handle, lParam uintptr) uintptr {
		var procID uintptr
		procGetWindowThreadProcessId.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&procID)))
		if procID == pid {
			found = uintptr(hwnd)
			return 0
		}
		return 1
	})

	procEnumWindows.Call(cb, 0)
	if found == 0 {
		return 0, fmt.Errorf("findWindowByPID: no top-level window found for PID %d", pid)
	}
	return found, nil
}
