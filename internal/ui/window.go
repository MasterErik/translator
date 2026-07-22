//go:build windows

package ui

import (
	"fmt"
	"syscall"
	"unsafe"
)

// Extended window styles for the overlay window.
// These are not exported by golang.org/x/sys/windows so we define them directly.
const (
	// WS_EX_TOPMOST keeps the window above all non-topmost windows.
	wsExTopmost = 0x00000008

	// WS_EX_LAYERED enables per-pixel alpha blending for transparency.
	wsExLayered = 0x00080000

	// WS_EX_TRANSPARENT makes the window transparent to mouse clicks,
	// passing them through to the window beneath.
	wsExTransparent = 0x00000020

	// WS_EX_NOACTIVATE prevents the window from taking keyboard focus when clicked.
	wsExNoactivate = 0x08000000

	// LWA_ALPHA sets the opacity via the alpha channel (bAlpha parameter).
	lwaAlpha = 0x00000002
)

var (
	user32                         = syscall.NewLazyDLL("user32.dll")
	procFindWindowW                = user32.NewProc("FindWindowW")
	procGetWindowLongPtrW          = user32.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtrW          = user32.NewProc("SetWindowLongPtrW")
	procSetLayeredWindowAttributes = user32.NewProc("SetLayeredWindowAttributes")
)

// setTopmost applies WS_EX_TOPMOST, WS_EX_LAYERED, WS_EX_TRANSPARENT, and
// WS_EX_NOACTIVATE extended styles to the window identified by hwnd.
// This makes the overlay always-on-top, supports alpha transparency, and
// passes mouse clicks through to windows beneath.
//
// The hwnd parameter is a Windows HWND handle obtained from the GioUI window.
func setTopmost(hwnd uintptr) error {
	if hwnd == 0 {
		return fmt.Errorf("setTopmost: invalid window handle (0)")
	}

	// Get current extended styles.
	exStyle, _, _ := procGetWindowLongPtrW.Call(hwnd, ^uintptr(19)) // GWL_EXSTYLE = -20 -> ^uintptr(19)
	if exStyle == 0 {
		return fmt.Errorf("setTopmost: GetWindowLongPtrW(GWL_EXSTYLE) returned 0")
	}

	newExStyle := uint32(exStyle) | wsExTopmost | wsExLayered | wsExTransparent | wsExNoactivate

	ret, _, _ := procSetWindowLongPtrW.Call(hwnd, ^uintptr(19), uintptr(newExStyle)) // GWL_EXSTYLE
	if ret == 0 {
		return fmt.Errorf("setTopmost: SetWindowLongPtrW failed")
	}

	// Set layered window attributes for full opacity (255 = fully opaque).
	// The WS_EX_LAYERED style combined with transparency is handled by Gio.
	procSetLayeredWindowAttributes.Call(hwnd, 0, 255, lwaAlpha)

	return nil
}

// findWindowHWND locates a top-level window by its window title and returns
// its HWND handle. It returns an error if the window cannot be found.
func findWindowHWND(title string) (uintptr, error) {
	titlePtr, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return 0, fmt.Errorf("findWindowHWND: failed to encode title %q as UTF-16: %w", title, err)
	}

	hwnd, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(titlePtr)))
	if hwnd == 0 {
		return 0, fmt.Errorf("findWindowHWND: window with title %q not found", title)
	}

	return hwnd, nil
}
