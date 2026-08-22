//go:build windows

package hotkey

import (
	"context"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

// Виртуальные коды клавиш и сообщения WM_*.
const (
	vkF1  = 0x70
	vkF2  = 0x71
	vkF3  = 0x72
	vkF4  = 0x73
	vkEsc = 0x1B

	wmHotkey = 0x0312
	wmQuit   = 0x0012
)

var (
	user32                 = syscall.NewLazyDLL("user32.dll")
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procRegisterHotKey     = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey   = user32.NewProc("UnregisterHotKey")
	procGetMessageW        = user32.NewProc("GetMessageW")
	procPostThreadMessageW = user32.NewProc("PostThreadMessageW")
	procGetCurrentThreadID = kernel32.NewProc("GetCurrentThreadId")
)

// msg — структура MSG (Windows x64, 48 байт).
type msg struct {
	hwnd    uintptr
	message uint32
	_       uint32 // padding
	wParam  uintptr
	lParam  uintptr
	time    uint32
	ptX     int32
	ptY     int32
}

// hotkeyBinding — связка «идентификатор → виртуальный код».
type hotkeyBinding struct {
	id int32
	vk uintptr
}

var bindings = []hotkeyBinding{
	{int32(KeyF1), vkF1},
	{int32(KeyF2), vkF2},
	{int32(KeyF3), vkF3},
	{int32(KeyF4), vkF4},
	{int32(KeyEsc), vkEsc},
}

// Run регистрирует глобальные hotkeys (F1–F4, Esc) и запускает цикл обработки
// сообщений. onKey вызывается из цикла при нажатии. Возвращается при отмене ctx.
func Run(ctx context.Context, onKey func(Key)) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	threadID, _, _ := procGetCurrentThreadID.Call()

	for _, b := range bindings {
		ret, _, _ := procRegisterHotKey.Call(0, uintptr(b.id), 0, b.vk)
		if ret == 0 {
			unregisterAll(b.id)
			return fmt.Errorf("hotkey: RegisterHotKey(id=%d) failed", b.id)
		}
	}
	defer unregisterAll(-1)

	// При отмене ctx посылаем WM_QUIT, чтобы GetMessageW вернул 0 и цикл вышел.
	go func() {
		<-ctx.Done()
		procPostThreadMessageW.Call(threadID, wmQuit, 0, 0)
	}()

	for {
		var m msg
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if ret == 0 { // WM_QUIT
			return nil
		}
		if int32(ret) == -1 {
			return fmt.Errorf("hotkey: GetMessageW failed")
		}
		if m.message == wmHotkey {
			switch Key(m.wParam) {
			case KeyF1, KeyF2, KeyF3, KeyF4, KeyEsc:
				onKey(Key(m.wParam))
			}
		}
	}
}

// unregisterAll снимает регистрацию hotkeys с id < stopID (или все, если stopID < 0).
func unregisterAll(stopID int32) {
	for _, b := range bindings {
		if stopID >= 0 && b.id == stopID {
			return
		}
		procUnregisterHotKey.Call(0, uintptr(b.id))
	}
}
