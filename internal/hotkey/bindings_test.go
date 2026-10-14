//go:build windows

package hotkey

import "testing"

// TestBindingsValid проверяет валидность таблицы bindings:
//   - id уникальны (дубль id → RegisterHotKey вернёт 0 и Run упал бы);
//   - пары (mods, vk) уникальны (Windows отвергает повторную регистрацию той же
//     комбинации); Shift+F2/F3 делят vk с чистыми F2/F3, но различаются
//     модификатором — поэтому уникальность проверяется по ПАРЕ, не по одному vk;
//   - множество id покрывает ровно ожидаемые клавиши;
//   - КАЖДЫЙ биндинг содержит РОВНО ожидаемый vk (wantVK-таблица: явные
//     константы VK_F1..VK_F4, VK_ESCAPE и числовые эквиваленты 0x70..0x73, 0x1B);
//   - КАЖДЫЙ биндинг содержит РОВНО ожидаемые mods (строгое равенство, а не
//     только проверка бита): modNorepeat для F1–F4/Esc, modShift|modNorepeat для
//     Shift+F2/F3 (уточнения, план Б-T3);
//   - WM_HOTKEY.id мапится на Key через keyForID.
func TestBindingsValid(t *testing.T) {
	// wantVK — ожидаемый vk для каждого id. Проверяем и сами "виртуальные коды"
	// против их числовых значений, чтобы константы не поехали.
	tests := []struct {
		key      Key
		id       int32
		name     string
		wantVK   uintptr
		wantMods uintptr // строгое равенство: modNorepeat либо modShift|modNorepeat
		numeric  uintptr // независимая сверка числового значения VK из WinAPI
	}{
		{KeyF1, int32(KeyF1), "F1", vkF1, modNorepeat, 0x70},
		{KeyF2, int32(KeyF2), "F2", vkF2, modNorepeat, 0x71},
		{KeyF3, int32(KeyF3), "F3", vkF3, modNorepeat, 0x72},
		{KeyF4, int32(KeyF4), "F4", vkF4, modNorepeat, 0x73},
		{KeyEsc, int32(KeyEsc), "Esc", vkEsc, modNorepeat, 0x1B},
		{KeyShiftF2, idShiftF2, "Shift+F2", vkF2, modShift | modNorepeat, 0x71},
		{KeyShiftF3, idShiftF3, "Shift+F3", vkF3, modShift | modNorepeat, 0x72},
	}

	// Константы пакета обязаны совпадать с числовыми кодами WinAPI.
	for _, tt := range tests {
		if tt.wantVK != tt.numeric {
			t.Errorf("package constant for %s = %#x, want numeric VK %#x", tt.name, tt.wantVK, tt.numeric)
		}
	}

	// Числовые значения модификаторов WinAPI — независимая сверка констант.
	const (
		numericModShift    = 0x0004
		numericModNorepeat = 0x4000
	)
	if modShift != numericModShift {
		t.Errorf("modShift = %#x, want %#x (MOD_SHIFT)", uintptr(modShift), numericModShift)
	}
	if modNorepeat != numericModNorepeat {
		t.Errorf("modNorepeat = %#x, want %#x (MOD_NOREPEAT)", uintptr(modNorepeat), numericModNorepeat)
	}

	if len(bindings) != len(tests) {
		t.Fatalf("bindings count = %d, want %d", len(bindings), len(tests))
	}

	seenIDs := make(map[int32]struct{}, len(bindings))
	seenCombos := make(map[[2]uintptr]struct{}, len(bindings))

	for i, b := range bindings {
		if _, dup := seenIDs[b.id]; dup {
			t.Errorf("bindings[%d]: duplicate id %d", i, b.id)
		}
		seenIDs[b.id] = struct{}{}

		combo := [2]uintptr{b.mods, b.vk}
		if _, dup := seenCombos[combo]; dup {
			t.Errorf("bindings[%d]: duplicate (mods=%#x, vk=%#x) combo", i, b.mods, b.vk)
		}
		seenCombos[combo] = struct{}{}

		// id обязан совпадать с ожидаемым (Run мапит WM_HOTKEY.wParam → Key).
		if b.id != tests[i].id {
			t.Errorf("bindings[%d]: id = %d, want %d (%s)", i, b.id, tests[i].id, tests[i].name)
		}

		// Явная сверка vk для КАЖДОЙ записи.
		if b.vk != tests[i].wantVK {
			t.Errorf("bindings[%d] (%s): vk = %#x, want %#x", i, tests[i].name, b.vk, tests[i].wantVK)
		}

		// Строгое равенство mods: ровно ожидаемый набор модификаторов,
		// без лишних битов и без недостающих.
		if b.mods != tests[i].wantMods {
			t.Errorf("bindings[%d] (%s): mods = %#x, want %#x", i, tests[i].name, b.mods, tests[i].wantMods)
		}

		// keyForID обязан вернуть ожидаемый Key (маршрутизация WM_HOTKEY).
		gotKey, ok := keyForID(b.id)
		if !ok {
			t.Errorf("bindings[%d] (%s): keyForID(%d) = ok=false", i, tests[i].name, b.id)
		} else if gotKey != tests[i].key {
			t.Errorf("bindings[%d]: keyForID(%d) = %d, want %d", i, b.id, gotKey, tests[i].key)
		}
	}

	// Множество id обязано покрывать ровно ожидаемые клавиши.
	for _, tt := range tests {
		if _, ok := seenIDs[tt.id]; !ok {
			t.Errorf("missing binding id %d (%s)", tt.id, tt.name)
		}
	}

	// keyForID не мапит чужие id.
	for _, bad := range []int32{-1, 5, 999, 1000, 1003, 9999} {
		if k, ok := keyForID(bad); ok {
			t.Errorf("keyForID(%d) = %d, want ok=false", bad, k)
		}
	}
}

// TestShiftBindingsDoNotCollideWithPlainF2F3 — модификатор обязан различать
// Shift+F2/F3 и чистые F2/F3: id лежат в разных диапазонах, а пары (mods, vk) не
// совпадают (иначе Windows отвергнет регистрацию). Диапазон id уточнений
// (1001/1002) не пересекается с диапазоном Key (0..4).
func TestShiftBindingsDoNotCollideWithPlainF2F3(t *testing.T) {
	plain := map[int32]hotkeyBinding{}
	shift := map[int32]hotkeyBinding{}
	for _, b := range bindings {
		switch b.id {
		case idShiftF2, idShiftF3:
			shift[b.id] = b
		default:
			plain[b.id] = b
		}
	}

	for _, id := range []int32{idShiftF2, idShiftF3} {
		if _, ok := shift[id]; !ok {
			t.Fatalf("id %d отсутствует в bindings", id)
		}
		if _, ok := plain[id]; ok {
			t.Fatalf("id %d присутствует и в plain, и в shift — нарушение диапазонов", id)
		}
	}

	// Чистые F2/F3 несут только modNorepeat; Shift-версии — modShift|modNorepeat.
	if plain[int32(KeyF2)].mods != modNorepeat || plain[int32(KeyF3)].mods != modNorepeat {
		t.Errorf("чистые F2/F3 должны иметь mods=modNorepeat: F2=%#x F3=%#x",
			plain[int32(KeyF2)].mods, plain[int32(KeyF3)].mods)
	}
	wantShift := uintptr(modShift | modNorepeat)
	if shift[idShiftF2].mods != wantShift || shift[idShiftF3].mods != wantShift {
		t.Errorf("Shift-биндинги должны иметь mods=modShift|modNorepeat: %#x / %#x, want %#x",
			shift[idShiftF2].mods, shift[idShiftF3].mods, wantShift)
	}

	// vk общий: Shift+F2 делит vkF2 с F2, Shift+F3 делит vkF3 с F3.
	if shift[idShiftF2].vk != plain[int32(KeyF2)].vk {
		t.Errorf("Shift+F2 должен делить vk с F2: %#x vs %#x", shift[idShiftF2].vk, plain[int32(KeyF2)].vk)
	}
	if shift[idShiftF3].vk != plain[int32(KeyF3)].vk {
		t.Errorf("Shift+F3 должен делить vk с F3: %#x vs %#x", shift[idShiftF3].vk, plain[int32(KeyF3)].vk)
	}

	// Но пары (mods, vk) — разные (модификатор различает).
	plainF2 := [2]uintptr{plain[int32(KeyF2)].mods, plain[int32(KeyF2)].vk}
	shiftF2 := [2]uintptr{shift[idShiftF2].mods, shift[idShiftF2].vk}
	if plainF2 == shiftF2 {
		t.Error("пара (mods, vk) для F2 и Shift+F2 совпала — Windows отвергнет регистрацию")
	}
	plainF3 := [2]uintptr{plain[int32(KeyF3)].mods, plain[int32(KeyF3)].vk}
	shiftF3 := [2]uintptr{shift[idShiftF3].mods, shift[idShiftF3].vk}
	if plainF3 == shiftF3 {
		t.Error("пара (mods, vk) для F3 и Shift+F3 совпала — Windows отвергнет регистрацию")
	}

	// keyForID возвращает РАЗНЫЕ Key для Shift-пар и чистых F2/F3.
	for _, tc := range []struct{ shiftID, plainID int32 }{
		{idShiftF2, int32(KeyF2)},
		{idShiftF3, int32(KeyF3)},
	} {
		sk, _ := keyForID(tc.shiftID)
		pk, _ := keyForID(tc.plainID)
		if sk == pk {
			t.Errorf("keyForID(%d) == keyForID(%d) == %d — Shift не различается", tc.shiftID, tc.plainID, sk)
		}
	}
}
