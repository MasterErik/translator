//go:build windows

package hotkey

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"sync/atomic"
	"syscall"
	"unsafe"

	"github.com/mastererik/translator/internal/logger"
)

// idShiftF2/idShiftF3 — идентификаторы биндингов уточнений. Взяты из чужого
// диапазона (1001/1002), а не из Key: KeyShiftF2/KeyShiftF3 — это внутренние
// ключи маршрутизации, а WM_HOTKEY приносит именно зарегистрированный id.
// Диапазон Key (0..6) и диапазон id (1001/1002) не пересекаются — инцидент с
// дублем id (26cc31d) не повторится.
const (
	idShiftF2 = 1001
	idShiftF3 = 1002
)

const (
	vkF1  = 0x70
	vkF2  = 0x71
	vkF3  = 0x72
	vkF4  = 0x73
	vkEsc = 0x1B

	wmHotkey = 0x0312
	wmQuit   = 0x0012

	// wmWake — приватное сообщение потока (WM_APP+1), используемое как второй
	// (быстрый) путь выхода из цикла GetMessageW при отмене ctx. Честная причина,
	// почему одного WM_QUIT мало: WM_QUIT имеет низкий приоритет и может застрять
	// в очереди за WM_PAINT/WM_TIMER (окно активно перерисовывается), тогда как
	// WM_APP+1 доставляется и обрабатывается раньше. Поэтому watcher постит оба
	// сообщения, и цикл выходит по первому доставленному. Факт доставки wmWake
	// сам по себе — не признак отмены: чужое WM_APP+1 в процессе больше НЕ
	// останавливает хоткеи (см. ветку wmWake в Run — выход только при
	// cancelled==true, иначе continue).
	wmWake = 0x8000 + 1 // WM_APP + 1

	// modNorepeat — MOD_NOREPEAT (Windows Vista+): подавляет автоповтор
	// WM_HOTKEY при удержании клавиши.
	modNorepeat = 0x4000

	// modShift — MOD_SHIFT (0x0004): модификатор Shift для биндингов уточнений
	// Shift+F2/Shift+F3 (план, Б-T3).
	modShift = 0x0004
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

// hotkeyBinding — единая запись биндинга: идентификатор регистрации (id),
// внутренний ключ маршрутизации (key), модификаторы (mods) и виртуальный код
// (vk). Единственный источник правды: и регистрация (Run), и маппинг id→Key
// (keyForID) строятся из одной таблицы bindings — рассинхрон невозможен (r3-6).
//
// Диапазоны id (r3-7): id 0..4 (KeyF1..KeyEsc) исторически совпадают с Key —
// поэтому для чистых клавиш key == Key(id); id 1001+ (idShiftF2/idShiftF3)
// зарезервированы под Shift-биндинги уточнений, чтобы не смешиваться с
// диапазоном Key и не повторять инцидент с дублем id (26cc31d).
type hotkeyBinding struct {
	id   int32
	key  Key
	mods uintptr
	vk   uintptr
}

// Все биндинги регистрируются с MOD_NOREPEAT: тумблер (F4) и Esc тоже
// не должны спамиться при удержании клавиши. Биндинги уточнений (1001/1002)
// несут modShift|modNorepeat и те же vk, что чистые F2/F3 — модификатор
// различает комбинации для Windows (пары (mods, vk) уникальны).
var bindings = []hotkeyBinding{
	{int32(KeyF1), KeyF1, modNorepeat, vkF1},
	{int32(KeyF2), KeyF2, modNorepeat, vkF2},
	{int32(KeyF3), KeyF3, modNorepeat, vkF3},
	{int32(KeyF4), KeyF4, modNorepeat, vkF4},
	{int32(KeyEsc), KeyEsc, modNorepeat, vkEsc},
	{int32(idShiftF2), KeyShiftF2, modShift | modNorepeat, vkF2},
	{int32(idShiftF3), KeyShiftF3, modShift | modNorepeat, vkF3},
}

// keyByID — производный от bindings маппинг id→Key (единственная точка
// перевода зарегистрированного id в Key). Строится один раз при инициализации
// пакета, поэтому не может разойтись с таблицей регистрации (r3-6).
var keyByID = func() map[int32]Key {
	m := make(map[int32]Key, len(bindings))
	for _, b := range bindings {
		m[b.id] = b.key
	}
	return m
}()

// keyForID мапит зарегистрированный id биндинга на внутренний Key по единой
// таблице bindings. Неизвестный id → нет второго значения (окно не шлёт
// чужих id; чужое сообщение в цикле безопасно игнорируется).
func keyForID(id int32) (Key, bool) {
	k, ok := keyByID[id]
	return k, ok
}

// postThreadMessage отправляет сообщение в очередь текущего потока и проверяет
// результат. ret == 0 означает, что сообщение не поставлено (очередь потока
// переполнена, поток уже завершается и т.п.) — об этом пишем WARN в slog и, если
// есть, в сессионный лог (sessLog), чтобы висящий GetMessageW не остался без
// диагностики. Возвращает ошибку пост-фактум для тестов/наблюдения, но
// вызывающий её не обязан прерывать работу.
//
// errno корректен при отказе (проверено реальным вызовом): syscall.Call уже
// возвращает актуальный GetLastError, поэтому отдельная ветка Errno(0)→
// GetLastError() не нужна (r3-3).
func postThreadMessage(sessLog logger.SessionLogger, threadID uintptr, m uint32) error {
	ret, _, err := procPostThreadMessageW.Call(threadID, uintptr(m), 0, 0)
	if ret == 0 {
		slog.Warn("hotkey: PostThreadMessageW failed",
			"threadID", threadID, "message", fmt.Sprintf("%#x", m), "err", err)
		sessLogWarn(sessLog, fmt.Sprintf("hotkey: отказ PostThreadMessageW (threadID=%d, message=%#x): %v", threadID, m, err))
		return fmt.Errorf("hotkey: PostThreadMessageW(threadID=%d, message=%#x): %w", threadID, m, err)
	}
	return nil
}

// sessLogWarn пишет предупреждение в сессионный лог, если он есть. Нил-безопасен:
// sessLog может быть nil (логирование отключено / вызов без сессии). Уровень
// передаём префиксом, т.к. в интерфейсе SessionLogger есть только LogDebug (r3-4).
func sessLogWarn(sessLog logger.SessionLogger, msg string) {
	if sessLog != nil {
		_ = sessLog.LogDebug("WARN: " + msg) // _ = err: лог не критичен для работы (r3-4)
	}
}

// sessLogDebug пишет отладочное сообщение в сессионный лог, если он есть.
// Нил-безопасен (sessLog может быть nil).
func sessLogDebug(sessLog logger.SessionLogger, msg string) {
	if sessLog != nil {
		_ = sessLog.LogDebug(msg) // _ = err: лог не критичен для работы
	}
}

// Run регистрирует глобальные hotkeys (F1–F4, Esc, Shift+F2/F3) и запускает
// цикл обработки сообщений. onKey вызывается из цикла при нажатии. Возвращается
// при отмене ctx.
//
// При отмене ctx фоновый watcher сначала выставляет atomic-флаг cancelled, затем
// постит ОБА сообщения — WM_QUIT и wake (WM_APP+1) — в очередь этого потока.
// Цикл выходит по первому полученному; порядок «флаг → пост» гарантирует, что к
// моменту обработки wmWake флаг уже виден (r3-1). Потеря любого из двух
// сообщений не подвешивает GetMessageW. Watcher имеет собственный путь выхода
// (done), который закрывается при возврате из Run, — горутина не утекает, даже
// если PostThreadMessageW не сработал.
//
// cancelled также делает безопасным чужое WM_APP+1: ветка wmWake выходит ТОЛЬКО
// при cancelled==true, иначе сообщение игнорируется (continue) и хоткеи
// продолжают работать при живом ctx.
//
// sessLog может быть nil — все обращения через nil-безопасные хелперы.
// Контракт возврата: nil при штатной отмене ctx (по аналогии со stub-версией).
func Run(ctx context.Context, onKey func(Key), sessLog logger.SessionLogger) error {
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
			// errno корректен при отказе (r3-3): отдельная ветка Errno(0)→
			// GetLastError() не нужна. Снимаем уже зарегистрированные (их знаем
			// точно) и сообщаем об отказе в оба лога (r3-4).
			unregisterRegistered()
			slog.Warn("hotkey: RegisterHotKey failed", "id", b.id, "err", err)
			sessLogWarn(sessLog, fmt.Sprintf("hotkey: отказ RegisterHotKey(id=%d): %v", b.id, err))
			return fmt.Errorf("hotkey: RegisterHotKey(id=%d): %w", b.id, err)
		}
		registered = append(registered, b)
	}

	// cancelled — признак штатной отмены ctx. Выставляется watcher'ом ДО постинга
	// wmWake, поэтому в цикле доставка wmWake ещё не означает отмену (r3-1).
	var cancelled atomic.Bool

	// done закрывается при выходе из Run — путь выхода для watcher-goroutine
	// (B-2/B-10): даже если оба Post* потеряны, горутина завершится и не утечёт.
	done := make(chan struct{})
	defer close(done)

	// При отмене ctx: сначала флаг, затем оба сообщения; цикл выходит по первому
	// полученному.
	go func() {
		select {
		case <-ctx.Done():
			cancelled.Store(true)
			_ = postThreadMessage(sessLog, threadID, wmQuit)
			_ = postThreadMessage(sessLog, threadID, wmWake)
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
			if cancelled.Load() {
				// Штатная отмена — выходим из цикла.
				sessLogDebug(sessLog, "hotkey: цикл завершён по отмене ctx")
				return nil
			}
			// Чужое WM_APP+1 при живом ctx: не наш сигнал, игнорируем и
			// продолжаем — иначе чужой hook/плагин тихо остановил бы хоткеи (r3-1).
			continue
		}
		if m.message == wmHotkey {
			// WM_HOTKEY.wParam — зарегистрированный id; мапим его на Key.
			// Чистые F1–F4/Esc имеют id == Key, уточнения — 1001/1002.
			if k, ok := keyForID(int32(m.wParam)); ok {
				onKey(k)
			}
		}
	}
}
