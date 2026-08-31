//go:build windows

package hotkey

import "testing"

// TestBindingsValid проверяет валидность таблицы bindings:
//   - id уникальны (дубль id → RegisterHotKey вернёт 0 и Run упадёт);
//   - vk уникальны;
//   - множество keys = {KeyF1, KeyF2, KeyF3, KeyF4, KeyEsc};
//   - каждый id == int32(соответствующего клавишного кода).
func TestBindingsValid(t *testing.T) {
	wantKeys := []Key{KeyF1, KeyF2, KeyF3, KeyF4, KeyEsc}

	if len(bindings) != len(wantKeys) {
		t.Fatalf("bindings count = %d, want %d", len(bindings), len(wantKeys))
	}

	seenIDs := make(map[int32]struct{}, len(bindings))
	seenVKs := make(map[uintptr]struct{}, len(bindings))

	for i, b := range bindings {
		if _, dup := seenIDs[b.id]; dup {
			t.Errorf("bindings[%d]: duplicate id %d", i, b.id)
		}
		seenIDs[b.id] = struct{}{}

		if _, dup := seenVKs[b.vk]; dup {
			t.Errorf("bindings[%d]: duplicate vk %#x", i, b.vk)
		}
		seenVKs[b.vk] = struct{}{}

		if i >= len(wantKeys) {
			continue
		}
		want := wantKeys[i]
		if b.id != int32(want) {
			t.Errorf("bindings[%d]: id = %d, want %d (Key %d)", i, b.id, int32(want), want)
		}
	}

	// Множество id обязано покрывать ровно ожидаемые клавиши.
	for _, k := range wantKeys {
		if _, ok := seenIDs[int32(k)]; !ok {
			t.Errorf("missing binding id for key %d", k)
		}
	}
}
