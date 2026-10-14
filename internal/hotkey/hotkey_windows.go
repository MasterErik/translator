//go:build windows

package hotkey

import (
	"context"
	"fmt"
	"log/slog"
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

	// wmWake — приватное сообщение потока (WM_APP+1), используемое как второй
	// (резервный) путь выхода из цикла GetMessageW при отмене ctx. Если WM_QUIT
	// потерян (чужая очередь переполнена, поток выгружается), wake-сообщение всё
	// равно будит цикл. Само сообщение — no-op: важен лишь факт доставки.
	wmWake = 0x8000 + 1 // WM_APP + 1

	// modNorepeat — MOD_NOREPEAT (Windows Vista+): подавляет автоповтор
	// WM_HOTKEY при удержании клавиши.
	modNorepeat = 0x4000
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

// hotkeyBinding — связка «идентификатор → модификаторы + виртуальный код».
type hotkeyBinding struct {
	id   int32
	mods uintptr
	vk   uintptr
}

// Все биндинги регистрируются с MOD_NOREPEAT: тумблер (F4) и Esc тоже
// не должны спамиться при удержании клавиши.
var bindings = []hotkeyBinding{
	{int32(KeyF1), modNorepeat, vkF1},
	{int32(KeyF2), modNorepeat, vkF2},
	{int32(KeyF3), modNorepeat, vkF3},
	{int32(KeyF4), modNorepeat, vkF4},
	{int32(KeyEsc), modNorepeat, vkEsc},
}

// postThreadMessage отправляет сообщение в очередь текущего потока и проверяет
// результат. ret == 0 означает, что сообщение не поставлено (очередь потока
// переполнена, поток уже завершается и т.п.) — об этом пишем WARN, чтобы
// висящий GetMessageW не остался без диагностики. Возвращает ошибку пост-фактум
// для тестов/наблюдения, но вызывающий её не обязан прерывать работу.
func postThreadMessage(threadID uintptr, m uint32) error {
	ret, _, err := procPostThreadMessageW.Call(threadID, uintptr(m), 0, 0)
	if ret == 0 {
		if err == syscall.Errno(0) {
			err = syscall.GetLastError()
		}
		slog.Warn("hotkey: PostThreadMessageW failed",
			"threadID", threadID, "message", fmt.Sprintf("%#x", m), "err", err)
		return fmt.Errorf("hotkey: PostThreadMessageW(threadID=%d, message=%#x): %w", threadID, m, err)
	}
	return nil
}

// Run регистрирует глобальные hotkeys (F1–F4, Esc) и запускает цикл обработки
// сообщений. onKey вызывается из цикла при нажатии. Возвращается при отмене ctx.
//
// При отмене ctx фоновый watcher посылает ОБА сообщения — WM_QUIT и wake
// (WM_APP+1) — в очередь этого потока. Цикл выходит по первому полученному;
// потеря любого из двух сообщений не подвешивает GetMessageW. Watcher имеет
// собственный путь выхода (done), который закрывается при возврате из Run, —
// горутина не утекает, даже если PostThreadMessageW не сработал.
//
// Контракт возврата: nil при штатной отмене ctx (по аналогии со stub-версией).
func Run(ctx context.Context, onKey func(Key)) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	threadID, _, _ := procGetCurrentThreadID.Call()

	// registered — фактические успешно зарегистрированные биндинги. Снимаем
	// ровно их, а не «все по совпадению id» (B-5).
	registered := make([]hotkeyBinding, 0, len(bindings))
	unregisterRegistered := func() {
		for i := len(registered) - 1; i >= 0; i-- {
			procUnregisterHotKey.Call(0, uintptr(registered[i].id))
		}
		registered = registered[:0]
	}
	defer unregisterRegistered()

	for _, b := range bindings {
		ret, _, err := procRegisterHotKey.Call(0, uintptr(b.id), b.mods, b.vk)
		if ret == 0 {
			if err == syscall.Errno(0) {
				err = syscall.GetLastError()
			}
			// Снимаем уже зарегистрированные (их знаем точно).
			unregisterRegistered()
			return fmt.Errorf("hotkey: RegisterHotKey(id=%d): %w", b.id, err)
		}
		registered = append(registered, b)
	}

	// done закрывается при выходе из Run — путь выхода для watcher-goroutine
	// (B-2/B-10): даже если оба Post* потеряны, горутина завершится и не утечёт.
	done := make(chan struct{})
	defer close(done)

	// При отмене ctx шлём оба сообщения; цикл выходит по первому полученному.
	go func() {
		select {
		case <-ctx.Done():
			_ = postThreadMessage(threadID, wmQuit)
			_ = postThreadMessage(threadID, wmWake)
		case <-done:
			return
		}
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
		if m.message == wmWake {
			// Штатная отмена (или просто no-op wake) — выходим из цикла.
			return nil
		}
		if m.message == wmHotkey {
			switch Key(m.wParam) {
			case KeyF1, KeyF2, KeyF3, KeyF4, KeyEsc:
				onKey(Key(m.wParam))
			}
		}
	}
}
