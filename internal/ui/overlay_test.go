package ui

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"gioui.org/app"
	"gioui.org/io/event"
	"gioui.org/widget/material"

	"github.com/mastererik/translator/internal/logger"
)

func TestNewOverlay(t *testing.T) {
	tests := []struct {
		name         string
		cfg          OverlayConfig
		wantWidth    int
		wantHeight   int
		wantFontSize int
	}{
		{
			name: "all fields set",
			cfg: OverlayConfig{
				Width:    1024,
				Height:   300,
				FontSize: 24,
			},
			wantWidth:    1024,
			wantHeight:   300,
			wantFontSize: 24,
		},
		{
			name:         "zero-value defaults",
			cfg:          OverlayConfig{},
			wantWidth:    1200,
			wantHeight:   650,
			wantFontSize: 18,
		},
		{
			name: "negative font size defaults",
			cfg: OverlayConfig{
				Width:    1200,
				FontSize: -5,
			},
			wantWidth:    1200,
			wantHeight:   650,
			wantFontSize: 18,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := NewOverlay(tt.cfg, logger.NewNopSessionLogger())
			if o.cfg.Width != tt.wantWidth {
				t.Errorf("Width = %d, want %d", o.cfg.Width, tt.wantWidth)
			}
			if o.cfg.Height != tt.wantHeight {
				t.Errorf("Height = %d, want %d", o.cfg.Height, tt.wantHeight)
			}
			if o.cfg.FontSize != tt.wantFontSize {
				t.Errorf("FontSize = %d, want %d", o.cfg.FontSize, tt.wantFontSize)
			}
			if o.translations == nil || o.history == nil || o.answersHistory == nil {
				t.Error("zone slices should be initialized")
			}
			if len(o.GetMessages()) != 0 {
				t.Errorf("initial messages should be empty, got %d", len(o.GetMessages()))
			}
			if o.shutdown == nil {
				t.Error("shutdown channel should be initialized")
			}
		})
	}
}

// TestGetMessagesFieldOrder — GetMessages конкатенирует поля в стабильном
// порядке: interim → translations → history → answersHistory → error.
// Это контракт порядка (обратная совместимость OverlayUI-интерфейса).
func TestGetMessagesFieldOrder(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: 18}, logger.NewNopSessionLogger())

	// Добавляем в перемешанном порядке — GetMessages всё равно сортирует по зонам.
	o.AddMessage(UIMessage{Type: Error, Text: "ошибка"})
	o.AddMessage(UIMessage{Type: AnswerCandidates, Answers: answersFrom("hint")})
	o.AddMessage(UIMessage{Type: Translation, Text: "перевод"})
	o.AddMessage(UIMessage{Type: Interim, Text: "речь"})
	o.AddMessage(UIMessage{Type: History, Text: "оригинал"})

	msgs := o.GetMessages()
	want := []struct {
		typ  UIMessageType
		text string
	}{
		{Interim, "речь"},
		{Translation, "перевод"},
		{History, "оригинал"},
		{AnswerCandidates, ""},
		{Error, "ошибка"},
	}
	if len(msgs) != len(want) {
		t.Fatalf("len(GetMessages) = %d, want %d", len(msgs), len(want))
	}
	for i, w := range want {
		if msgs[i].Type != w.typ {
			t.Errorf("msgs[%d].Type = %v, want %v (порядок полей)", i, msgs[i].Type, w.typ)
		}
		if w.text != "" && msgs[i].Text != w.text {
			t.Errorf("msgs[%d].Text = %q, want %q", i, msgs[i].Text, w.text)
		}
	}
}

// TestGetMessagesOmitsEmptyInterimAndError — zero-value interim/error не
// попадают в вывод (GetMessages пропускает пустые одиночные поля).
func TestGetMessagesOmitsEmptyInterimAndError(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: 18}, logger.NewNopSessionLogger())
	o.AddMessage(UIMessage{Type: Translation, Text: "только перевод"})

	msgs := o.GetMessages()
	if len(msgs) != 1 || msgs[0].Type != Translation {
		t.Fatalf("GetMessages = %v, want только [Translation]", msgs)
	}
}

// TestAddMessageConcurrentAllZones — конкурентная запись во все зоны
// (interim/translation/history/answers/error) + чтение GetMessages под -race.
// Дополняет TestConcurrentAccess (тот писал только Status).
func TestAddMessageConcurrentAllZones(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: 18}, logger.NewNopSessionLogger())

	const writers = 8
	const perWriter = 100
	var wg sync.WaitGroup
	wg.Add(writers * 2)
	for w := 0; w < writers; w++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				switch i % 5 {
				case 0:
					o.AddMessage(UIMessage{Type: Interim, Text: fmt.Sprintf("i%d", i)})
				case 1:
					o.AddMessage(UIMessage{Type: Translation, Text: fmt.Sprintf("t%d", i)})
				case 2:
					o.AddMessage(UIMessage{Type: History, Text: fmt.Sprintf("h%d", i)})
				case 3:
					o.AddMessage(UIMessage{Type: AnswerCandidates, Answers: answersFrom("a")})
				case 4:
					o.AddMessage(UIMessage{Type: Error, Text: fmt.Sprintf("e%d", i)})
				}
			}
		}(w)
		// Параллельные читатели.
		go func() {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				_ = o.GetMessages()
			}
		}()
	}
	wg.Wait()

	// interim заменяется (1), error заменяется (1), остальное накапливается.
	tr := o.translationMessages()
	h := o.historyMessages()
	if len(tr) != writers*perWriter/5 || len(h) != writers*perWriter/5 {
		t.Errorf("translationMessages=%d historyMessages=%d, want %d each",
			len(tr), len(h), writers*perWriter/5)
	}
}

func TestAddMessageGetMessages(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 200}, logger.NewNopSessionLogger())

	// Статус и подсказки добавляются (append).
	msg1 := UIMessage{Type: Status, Text: "Connected", Timestamp: time.Now()}
	msg2 := UIMessage{Type: AnswerCandidates, Text: "Q", Answers: answersFrom("A1", "A2"), Timestamp: time.Now()}
	o.AddMessage(msg1)
	o.AddMessage(msg2)

	msgs := o.GetMessages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Type != Status {
		t.Errorf("msg[0].Type = %v, want Status", msgs[0].Type)
	}
	if msgs[1].Type != AnswerCandidates {
		t.Errorf("msg[1].Type = %v, want AnswerCandidates", msgs[1].Type)
	}
}

// TestTranslationAppendOnly — Translation стал append-only (Task 1.1, P1):
// streaming-ветка удалена, dispatcher шлёт только "done". Все Translation
// сообщения накапливаются без фильтра по статусу.
func TestTranslationAppendOnly(t *testing.T) {
	tests := []struct {
		name      string
		messages  []UIMessage
		wantTexts []string
	}{
		{
			name: "pending appends like done",
			messages: []UIMessage{
				{Type: Translation, Text: "first"},
				{Type: Translation, Text: "second"},
				{Type: Translation, Text: "third"},
			},
			wantTexts: []string{"first", "second", "third"},
		},
		{
			name: "done only unaffected",
			messages: []UIMessage{
				{Type: Translation, Text: "a"},
				{Type: Translation, Text: "b"},
			},
			wantTexts: []string{"a", "b"},
		},
		{
			name: "empty status appends",
			messages: []UIMessage{
				{Type: Translation, Text: "x"},
			},
			wantTexts: []string{"x"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := NewOverlay(OverlayConfig{Width: 800, Height: 200}, logger.NewNopSessionLogger())
			for _, m := range tt.messages {
				o.AddMessage(m)
			}
			tr := o.translationMessages()
			if len(tr) != len(tt.wantTexts) {
				t.Fatalf("translationMessages count = %d, want %d", len(tr), len(tt.wantTexts))
			}
			for i, want := range tt.wantTexts {
				if tr[i].Text != want {
					t.Errorf("translationMessages[%d].Text = %q, want %q", i, tr[i].Text, want)
				}
			}
		})
	}
}

func TestConcurrentAccess(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 200}, logger.NewNopSessionLogger())

	var wg sync.WaitGroup
	numWriters := 10
	numReaders := 5
	messagesPerWriter := 100

	for w := 0; w < numWriters; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			baseTime := time.Now()
			for i := 0; i < messagesPerWriter; i++ {
				o.AddMessage(UIMessage{
					Type:      Status,
					Text:      fmt.Sprintf("%d. msg", workerID*messagesPerWriter+i+1),
					Timestamp: baseTime.Add(time.Duration(i) * time.Millisecond),
				})
			}
		}(w)
	}

	for r := 0; r < numReaders; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < messagesPerWriter; i++ {
				_ = o.GetMessages()
			}
		}()
	}

	wg.Wait()

	msgs := o.GetMessages()
	expected := numWriters * messagesPerWriter
	if len(msgs) != expected {
		t.Errorf("expected %d messages, got %d", expected, len(msgs))
	}
}

func TestGetMessagesReturnsCopy(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 200}, logger.NewNopSessionLogger())
	o.AddMessage(UIMessage{Type: Status, Text: "original", Timestamp: time.Now()})

	msgs := o.GetMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	msgs[0] = UIMessage{Type: Translation, Text: "modified", Timestamp: time.Now()}

	msgs2 := o.GetMessages()
	if msgs2[0].Type == Translation {
		t.Error("GetMessages should return a copy; modification leaked")
	}
	if msgs2[0].Text != "original" {
		t.Errorf("GetMessages returned %q, want %q", msgs2[0].Text, "original")
	}
}

// ── Тесты вспомогательных функций ──

func TestLastAnswers(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 200}, logger.NewNopSessionLogger())

	// Пустой.
	msg, ok := o.lastAnswers()
	if ok {
		t.Errorf("empty: lastAnswers should return false")
	}

	// Кандидаты с пустым списком — не считаются.
	o.AddMessage(UIMessage{Type: AnswerCandidates, Answers: answersFrom()})
	msg, ok = o.lastAnswers()
	if ok {
		t.Errorf("empty answers list: lastAnswers should return false")
	}

	// Реальные кандидаты.
	o.AddMessage(UIMessage{Type: AnswerCandidates, Answers: answersFrom("A", "B", "C")})
	msg, ok = o.lastAnswers()
	if !ok || len(msg.Answers) != 3 {
		t.Errorf("with answers: got ok=%v len=%d, want ok=true len=3", ok, len(msg.Answers))
	}

	// Замена.
	o.AddMessage(UIMessage{Type: AnswerCandidates, Answers: answersFrom("X")})
	msg, ok = o.lastAnswers()
	if !ok || len(msg.Answers) != 1 || msg.Answers[0].Source != "X" {
		t.Errorf("after replace: got ok=%v answers=%v, want [X]", ok, msg.Answers)
	}
}

func TestHistoryMessages(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 200}, logger.NewNopSessionLogger())

	// Пустой.
	if h := o.historyMessages(); len(h) != 0 {
		t.Errorf("empty: historyMessages should return empty slice, got %d", len(h))
	}

	// Добавляем mixed — фильтрует только History.
	o.AddMessage(UIMessage{Type: Interim, Text: "interim"})
	o.AddMessage(UIMessage{Type: History, Text: "h1", Translation: "п1"})
	o.AddMessage(UIMessage{Type: Translation, Text: "tr"})
	o.AddMessage(UIMessage{Type: History, Text: "h2"})

	h := o.historyMessages()
	if len(h) != 2 {
		t.Fatalf("historyMessages count = %d, want 2", len(h))
	}
	if h[0].Text != "h1" || h[1].Text != "h2" {
		t.Errorf("history order: got [%q, %q], want [h1, h2]", h[0].Text, h[1].Text)
	}
}

// ── Тесты замены и накопления сообщений ──

func TestInterimReplacement(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 200}, logger.NewNopSessionLogger())

	o.AddMessage(UIMessage{Type: Interim, Text: "first"})
	o.AddMessage(UIMessage{Type: Interim, Text: "second"})
	o.AddMessage(UIMessage{Type: Interim, Text: "third"})

	msgs := o.GetMessages()
	interimCount := 0
	var lastInterimText string
	for _, m := range msgs {
		if m.Type == Interim {
			interimCount++
			lastInterimText = m.Text
		}
	}

	if interimCount != 1 {
		t.Errorf("interim count = %d, want 1 — Interim должен заменяться", interimCount)
	}
	if lastInterimText != "third" {
		t.Errorf("last interim = %q, want %q", lastInterimText, "third")
	}
}

// TestHistoryAppendOnly — History накапливается (append-only), перевод
// History-сообщения сохраняется.
func TestHistoryAppendOnly(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 200}, logger.NewNopSessionLogger())

	o.AddMessage(UIMessage{Type: History, Text: "msg1", Translation: "tr1"})
	o.AddMessage(UIMessage{Type: History, Text: "msg2", Translation: "tr2"})
	o.AddMessage(UIMessage{Type: History, Text: "msg3"})

	hist := o.historyMessages()
	if len(hist) != 3 {
		t.Fatalf("history count = %d, want 3 — History должен накапливаться", len(hist))
	}
	if hist[0].Text != "msg1" || hist[1].Text != "msg2" || hist[2].Text != "msg3" {
		t.Error("history order нарушен")
	}
	if hist[0].Translation != "tr1" || hist[1].Translation != "tr2" {
		t.Error("history translation не сохранился")
	}
}

// TestUIMessageConstants удалён: ассертил строковые константы сами на себя
// (string(Translation) == "Translation") — гарантируется компилятором.

// ── Task 2.2: раздельное состояние зон ──

// TestAnswerCandidatesHistoryPreserved — RED (Task 2.2): две AnswerCandidates
// не теряются в истории (GetMessages содержит оба), но рендерится последняя.
func TestAnswerCandidatesHistoryPreserved(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 200}, logger.NewNopSessionLogger())

	o.AddMessage(UIMessage{Type: AnswerCandidates, Answers: answersFrom("first")})
	o.AddMessage(UIMessage{Type: AnswerCandidates, Answers: answersFrom("second")})

	var found []string
	for _, m := range o.GetMessages() {
		if m.Type == AnswerCandidates {
			for _, a := range m.Answers {
				found = append(found, a.Source)
			}
		}
	}
	if len(found) != 2 || found[0] != "first" || found[1] != "second" {
		t.Fatalf("история AnswerCandidates потеряна: %v", found)
	}

	last, ok := o.lastAnswers()
	if !ok || len(last.Answers) != 1 || last.Answers[0].Source != "second" {
		t.Errorf("рендерится не последняя: ok=%v answers=%v", ok, last.Answers)
	}
}

// TestErrorZone3RendersError — Task 2.3: сообщение об ошибке (Type=Error)
// рендерится в зоне 3 отдельным стилем; рендер не паникует и занимает окно.
func TestErrorZone3RendersError(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 650, FontSize: 18}, logger.NewNopSessionLogger())
	o.AddMessage(UIMessage{Type: Error, Text: "LLM timeout"})

	// Ошибка хранится отдельно от подсказок.
	if o.errorMsg.Type != Error || o.errorMsg.Text != "LLM timeout" {
		t.Fatalf("errorMsg = %+v, want Type=Error Text=LLM timeout", o.errorMsg)
	}
	msgs := o.GetMessages()
	if len(msgs) != 1 || msgs[0].Type != Error {
		t.Fatalf("GetMessages = %v, want единственный Error", msgs)
	}

	th := material.NewTheme()
	gtx, _ := newTestContext(800, 650)
	o.render(gtx, th)
	if gtx.Constraints.Max.X != 800 || gtx.Constraints.Max.Y != 650 {
		t.Errorf("render занимает %v, want 800x650", gtx.Constraints.Max)
	}
}

// TestErrorReplacement — Error заменяется (хранится только последняя), в
// отличие от AnswerCandidates.
func TestErrorReplacement(t *testing.T) {
	o := NewOverlay(OverlayConfig{Width: 800, Height: 200}, logger.NewNopSessionLogger())
	o.AddMessage(UIMessage{Type: Error, Text: "first"})
	o.AddMessage(UIMessage{Type: Error, Text: "second"})

	if o.errorMsg.Text != "second" {
		t.Errorf("errorMsg = %q, want %q (замена)", o.errorMsg.Text, "second")
	}
	count := 0
	for _, m := range o.GetMessages() {
		if m.Type == Error {
			count++
		}
	}
	if count != 1 {
		t.Errorf("Error count = %d, want 1 (замена)", count)
	}
}

// ── Task 1.3: таймаут ожидания DestroyEvent (P7) ──

// TestWaitForDestroyReceivesEvent — канал с DestroyEvent → немедленный возврат
// с ошибкой контекста (штатное завершение окна).
func TestWaitForDestroyReceivesEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nextEvent := func() event.Event { return app.DestroyEvent{} }

	start := time.Now()
	err := waitForDestroy(ctx, nextEvent, destroyEventTimeout, logger.NewNopSessionLogger())
	elapsed := time.Since(start)

	if err != ctx.Err() {
		t.Errorf("err = %v, want ctx.Err() = %v", err, ctx.Err())
	}
	// Не должны ждать таймаут: событие пришло сразу.
	if elapsed > destroyEventTimeout/2 {
		t.Errorf("возврат занял %v — DestroyEvent должен возвращаться сразу", elapsed)
	}
}

// TestWaitForDestroyTimeout — канал молчит → возврат по таймауту (nil), не виснет.
func TestWaitForDestroyTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Блокирующий источник, который никогда не отдаёт событие.
	block := make(chan struct{})
	nextEvent := func() event.Event {
		<-block
		return app.DestroyEvent{}
	}

	const timeout = 50 * time.Millisecond
	start := time.Now()
	err := waitForDestroy(ctx, nextEvent, timeout, logger.NewNopSessionLogger())
	elapsed := time.Since(start)
	close(block) // освобождаем насос waitForDestroy (застрял на nextEvent)

	if err != nil {
		t.Errorf("err = %v, want nil (принудительный выход по таймауту)", err)
	}
	if elapsed < timeout {
		t.Errorf("возврат занял %v — раньше таймаута %v", elapsed, timeout)
	}
	if elapsed > 10*timeout {
		t.Errorf("возврат занял %v — таймаут %v не сработал", elapsed, timeout)
	}
}

// TestWaitForDestroyOtherEventTimeout — источник отдаёт НЕ-DestroyEvent
// (например FrameEvent), ждём до таймаута → возврат по таймауту.
func TestWaitForDestroyOtherEventTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Первое событие — FrameEvent (не DestroyEvent); далее источник молчит,
	// иначе насос крутился бы в busy-spin.
	block := make(chan struct{})
	first := true
	nextEvent := func() event.Event {
		if first {
			first = false
			return app.FrameEvent{}
		}
		<-block
		return app.DestroyEvent{}
	}

	const timeout = 50 * time.Millisecond
	start := time.Now()
	err := waitForDestroy(ctx, nextEvent, timeout, logger.NewNopSessionLogger())
	elapsed := time.Since(start)
	close(block)

	if err != nil {
		t.Errorf("err = %v, want nil (FrameEvent не завершает ожидание)", err)
	}
	if elapsed < timeout || elapsed > 10*timeout {
		t.Errorf("возврат занял %v, want ≈%v", elapsed, timeout)
	}
}
