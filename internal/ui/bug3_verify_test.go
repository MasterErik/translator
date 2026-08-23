package ui

import (
	"fmt"
	"github.com/mastererik/translator/internal/logger"
	"strings"
	"testing"
)

// TestBug3_TranslationInHistory — ad-hoc verification: BUG #3 исправлен.
// Проверяет: поле Translation, History с переводом, обратная совместимость.
func TestBug3_TranslationInHistory(t *testing.T) {
	// 1. UIMessage.Translation — поле существует и имеет нулевое значение "".
	msg := UIMessage{Type: History, Text: "Hello", Translation: "Привет"}
	if msg.Translation != "Привет" {
		t.Fatalf("Translation field: got %q, want %q", msg.Translation, "Привет")
	}
	if (UIMessage{}).Translation != "" {
		t.Fatal("zero-value Translation should be \"\"")
	}
	t.Log("✓ Translation field works")

	// 2. Overlay: History-сообщения с переводом.
	ov := NewOverlay(OverlayConfig{Width: 800, Height: 400, FontSize: 14}, logger.NewNopSessionLogger())
	ov.AddMessage(UIMessage{
		Type:        History,
		Text:        "Hello world",
		Translation: "Привет мир",
	})
	ov.AddMessage(UIMessage{
		Type:        History,
		Text:        "How are you",
		Translation: "Как дела",
	})
	msgs := ov.GetMessages()
	histMsgs := filterHistory(msgs)
	if len(histMsgs) != 2 {
		t.Fatalf("history count: got %d, want 2", len(histMsgs))
	}
	if histMsgs[0].Translation != "Привет мир" {
		t.Fatalf("history[0].Translation: got %q", histMsgs[0].Translation)
	}
	if histMsgs[1].Translation != "Как дела" {
		t.Fatalf("history[1].Translation: got %q", histMsgs[1].Translation)
	}
	t.Log("✓ History messages with Translation")

	// 3. History без Translation (обратная совместимость).
	ov.AddMessage(UIMessage{
		Type: History,
		Text: "Old message without translation",
	})
	oldMsg := filterHistory(ov.GetMessages())[2]
	if oldMsg.Translation != "" {
		t.Fatalf("old-style History Translation: got %q, want \"\"", oldMsg.Translation)
	}
	t.Log("✓ Old-style History (no Translation) — backward compat")

	// 4. Translation done + History — не конфликтуют.
	ov2 := NewOverlay(OverlayConfig{Width: 800, Height: 400, FontSize: 14}, logger.NewNopSessionLogger())
	ov2.AddMessage(UIMessage{
		Type:      Translation,
		Text:      "Привет мир",
		MsgStatus: "done",
	})
	ov2.AddMessage(UIMessage{
		Type:        History,
		Text:        "Hello world",
		Translation: "Привет мир",
	})
	msgs3 := ov2.GetMessages()
	if !hasTranslationDone(msgs3) {
		t.Fatal("should have Translation done message")
	}
	if len(filterHistory(msgs3)) != 1 {
		t.Fatalf("should have 1 History, got %d", len(filterHistory(msgs3)))
	}
	t.Log("✓ Translation done + History coexist")
}

func filterHistory(msgs []UIMessage) []UIMessage {
	var out []UIMessage
	for _, m := range msgs {
		if m.Type == History {
			out = append(out, m)
		}
	}
	return out
}

func hasTranslationDone(msgs []UIMessage) bool {
	for _, m := range msgs {
		if m.Type == Translation && m.MsgStatus == "done" && strings.Contains(m.Text, "Привет") {
			return true
		}
	}
	fmt.Println("DEBUG hasTranslationDone: no match found")
	for i, m := range msgs {
		fmt.Printf("  msg[%d]: type=%s status=%s text=%q\n", i, m.Type, m.MsgStatus, m.Text)
	}
	return false
}
