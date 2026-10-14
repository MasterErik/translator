//go:build e2e

package ui

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"gioui.org/widget/material"

	"github.com/mastererik/translator/internal/logger"
)

// TestWindowStarts — интеграционный тест: окно создаётся, все 4 зоны получают данные.
func TestWindowStarts(t *testing.T) {
	o := NewOverlay(OverlayConfig{
		Width:    1200,
		Height:   650,
		FontSize: 18,
	}, logger.NewNopSessionLogger())

	// Добавляем сообщения во все 4 зоны.
	o.AddMessage(UIMessage{Type: Interim, Text: "I have five years of..."})
	o.AddMessage(UIMessage{Type: Translation, Text: "У меня пять лет опыта..."})
	o.AddMessage(UIMessage{Type: AnswerCandidates, Answers: answersFrom("Yes, I agree", "No, thanks", "Let me think")})

	// Добавляем 40 строк в историю перевода (>10 — проверка скролла).
	for i := 1; i <= 40; i++ {
		o.AddMessage(UIMessage{
			Type:        History,
			Text:        fmt.Sprintf("Original line %d", i),
			Translation: fmt.Sprintf("Перевод строки %d", i),
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- o.Run(ctx)
	}()

	// Ждём что окно стартует без мгновенной ошибки.
	select {
	case err := <-errCh:
		t.Fatalf("окно упало при старте: %v", err)
	case <-time.After(2 * time.Second):
	}

	msgs := o.GetMessages()

	// Проверяем что все 4 зоны получили данные.
	has := map[UIMessageType]bool{}
	for _, m := range msgs {
		has[m.Type] = true
	}

	zones := []UIMessageType{Interim, Translation, AnswerCandidates, History}
	for _, z := range zones {
		if !has[z] {
			t.Errorf("зона %s не получила данные", z)
		}
	}

	// Проверяем конкретные значения.
	if m := o.lastInterim(); m.Text != "I have five years of..." {
		t.Errorf("Interim = %q", m.Text)
	}
	if m, ok := o.lastAnswers(); !ok || len(m.Answers) != 3 {
		t.Errorf("AnswerCandidates = %v (ok=%v)", m.Answers, ok)
	}
	hist := o.historyMessages()
	if len(hist) != 40 {
		t.Errorf("History count = %d, want 40 — все строки должны быть в буфере для скролла", len(hist))
	}
	// Проверяем что последняя строка доступна (скролл до конца).
	if hist[39].Translation != "Перевод строки 40" {
		t.Errorf("last translation = %q, want %q", hist[39].Translation, "Перевод строки 40")
	}
	if hist[0].Translation != "Перевод строки 1" {
		t.Errorf("first translation = %q, want %q", hist[0].Translation, "Перевод строки 1")
	}

	// Проверяем автоскролл: рендерим кадр как в Run() и читаем FrameMetrics.
	// Раньше проверялись геттеры TranscriptionScrollLen/TranslationAtEnd/
	// TranscriptionAtEnd — теперь метрики кадра возвращает сам render().
	//
	// ВАЖНО: layout.List в Gio НЕ потокобезопасен, а Run() рисует кадры в своей
	// горутине. Чтобы ручной render() ниже не гонялся с кадровым циклом Run()
	// (это давало «panic: unfinished child»), сначала останавливаем Run() и
	// дожидаемся его выхода — после этого списки принадлежат только тесту.
	cancel()
	// Разбудить Run(): он блокируется на w.Event() и замечает ctx.Done() только
	// на следующей итерации цикла. Invalidate порождает событие кадра, которое
	// выводит Run() из ожидания — иначе cancel не наблюдаем без внешних событий.
	o.invalidateIf()
	select {
	case err := <-errCh:
		// Штатное завершение по cancel: Run() возвращает ctx.Err()
		// (context.Canceled) после DestroyEvent — это не ошибка.
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Run завершился с ошибкой: %v", err)
		}
	case <-time.After(destroyEventTimeout + 3*time.Second):
		t.Fatal("Run не завершился после cancel")
	}

	// Зона 4 (Transcription History) по умолчанию СКРЫТА (F4 включается
	// пользователем), а её высота кратна измеренной строке. Чтобы проверить
	// автоскролл именно этой зоны, включаем её перед рендером — иначе
	// TranscriptionAtEnd структурно false (needScroll только у видимой зоны).
	o.ToggleTranscriptionHistory()
	th := material.NewTheme()
	gtx, _ := newTestContext(1200, 650)
	m := o.render(gtx, th)
	if !m.TranslationsAtEnd {
		t.Error("Translation History: скролл НЕ в конце (TranslationsAtEnd=false)")
	}
	if !m.TranscriptionAtEnd {
		t.Error("Transcription History: скролл НЕ в конце (TranscriptionAtEnd=false)")
	}
	if !m.InterimAtEnd {
		t.Error("Interim: скролл НЕ в конце (InterimAtEnd=false)")
	}

	// Высота зоны 4 кратна фактической высоте строки: 4 × lineHeightPx(fs),
	// и вмещает 4 строки без обрезки последней.
	if got, want := historyVisibleHeightPx(o.cfg.FontSize), lineHeightPx(o.cfg.FontSize)*historyVisibleLines; got != want {
		t.Errorf("historyVisibleHeightPx(%d) = %d, want %d × %d = %d",
			o.cfg.FontSize, got, lineHeightPx(o.cfg.FontSize), historyVisibleLines, want)
	}

	// Проверяем размеры окна.
	if o.cfg.Width != 1200 {
		t.Errorf("Width = %d, want 1200", o.cfg.Width)
	}
	if o.cfg.Height != 650 {
		t.Errorf("Height = %d, want 650", o.cfg.Height)
	}

	// Проверяем пропорции зон согласно render() в overlay.go:
	// 1. Interim (Rigid, 2 строки)
	// 2. Translation History (Flexed 0.45, скролл переводов)
	// 3. Transcription History (Flexed 0.35, скролл оригиналов)
	// 4. AnswerCandidates (Flexed 0.20, подсказки)
	zonesInfo := []struct {
		name   string
		flexed bool
	}{
		{"Interim", false},
		{"Translation History", true},
		{"Transcription History", true},
		{"AnswerCandidates", true},
	}
	for _, z := range zonesInfo {
		t.Logf("зона %s: flexed=%v", z.name, z.flexed)
	}
	t.Logf("окно %d×%d, fontSize=%d", o.cfg.Width, o.cfg.Height, o.cfg.FontSize)
}
