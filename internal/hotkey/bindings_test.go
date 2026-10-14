//go:build windows

package hotkey

import "testing"

// TestBindingsValid проверяет валидность таблицы bindings:
//   - id уникальны (дубль id → RegisterHotKey вернёт 0 и Run упадёт);
//   - vk уникальны;
//   - пары (mods, vk) уникальны (Windows отвергает повторную регистрацию той же комбинации);
//   - множество keys = {KeyF1, KeyF2, KeyF3, KeyF4, KeyEsc};
//   - КАЖДЫЙ биндинг содержит РОВНО ожидаемый vk (wantVK-таблица: явные
//     константы VK_F1..VK_F4, VK_ESCAPE и числовые эквиваленты 0x70..0x73, 0x1B);
//   - КАЖДЫЙ биндинг содержит РОВНО mods == modNorepeat (строгое равенство, а не
//     только проверка бита — MOD_NOREPEAT и ничего сверх него).
func TestBindingsValid(t *testing.T) {
	// wantVK — ожидаемый vk для каждого id (id — это Key, но vk — отдельная
	// сущность: ошибочный vk ловится только явной таблицей). Проверяем и сами
	// "виртуальные коды" против их числовых значений, чтобы константы не поехали.
	tests := []struct {
		key     Key
		name    string
		wantVK  uintptr
		numeric uintptr // независимая сверка числового значения VK из WinAPI
	}{
		{KeyF1, "F1", vkF1, 0x70},
		{KeyF2, "F2", vkF2, 0x71},
		{KeyF3, "F3", vkF3, 0x72},
		{KeyF4, "F4", vkF4, 0x73},
		{KeyEsc, "Esc", vkEsc, 0x1B},
	}

	// Константы пакета обязаны совпадать с числовыми кодами WinAPI.
	for _, tt := range tests {
		if tt.wantVK != tt.numeric {
			t.Errorf("package constant for %s = %#x, want numeric VK %#x", tt.name, tt.wantVK, tt.numeric)
		}
	}

	if len(bindings) != len(tests) {
		t.Fatalf("bindings count = %d, want %d", len(bindings), len(tests))
	}

	seenIDs := make(map[int32]struct{}, len(bindings))
	seenVKs := make(map[uintptr]struct{}, len(bindings))
	seenCombos := make(map[[2]uintptr]struct{}, len(bindings))

	for i, b := range bindings {
		if _, dup := seenIDs[b.id]; dup {
			t.Errorf("bindings[%d]: duplicate id %d", i, b.id)
		}
		seenIDs[b.id] = struct{}{}

		if _, dup := seenVKs[b.vk]; dup {
			t.Errorf("bindings[%d]: duplicate vk %#x", i, b.vk)
		}
		seenVKs[b.vk] = struct{}{}

		combo := [2]uintptr{b.mods, b.vk}
		if _, dup := seenCombos[combo]; dup {
			t.Errorf("bindings[%d]: duplicate (mods=%#x, vk=%#x) combo", i, b.mods, b.vk)
		}
		seenCombos[combo] = struct{}{}

		// id обязан совпадать с Key (Run мапит WM_HOTKEY.wParam → Key).
		want := tests[i].key
		if b.id != int32(want) {
			t.Errorf("bindings[%d]: id = %d, want %d (Key %d)", i, b.id, int32(want), want)
		}

		// Явная сверка vk для КАЖДОЙ записи.
		if b.vk != tests[i].wantVK {
			t.Errorf("bindings[%d] (%s): vk = %#x, want %#x", i, tests[i].name, b.vk, tests[i].wantVK)
		}

		// Строгое равенство mods: только MOD_NOREPEAT, без лишних модификаторов.
		if b.mods != modNorepeat {
			t.Errorf("bindings[%d] (%s): mods = %#x, want %#x (MOD_NOREPEAT only)", i, tests[i].name, b.mods, uintptr(modNorepeat))
		}
	}

	// Множество id обязано покрывать ровно ожидаемые клавиши.
	for _, tt := range tests {
		if _, ok := seenIDs[int32(tt.key)]; !ok {
			t.Errorf("missing binding id for key %d (%s)", tt.key, tt.name)
		}
	}
}
