package dispatcher

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mastererik/translator/internal/common"
	"github.com/mastererik/translator/internal/translator"
	"github.com/mastererik/translator/internal/ui"
)

// waitFor ожидает, пока условие станет истинным, до таймаута.
func waitFor(t *testing.T, cond func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("условие не выполнено до таймаута")
}

// waitIdle ожидает, пока worker закончит текущую генерацию (busy=false) и
// очередь заданий опустеет. Замена фиксированного time.Sleep после серии
// входных событий: даёт детерминированную точку «всё обработано», не завязываясь
// на скорость машины (флак-риск D7).
func waitIdle(t *testing.T, d *Dispatcher) {
	t.Helper()
	waitFor(t, func() bool {
		return !d.busy.Load() && len(d.answerCh) == 0
	}, 2*time.Second)
}

// waitMessages ожидает, пока overlay накопит хотя бы n сообщений.
func waitMessages(t *testing.T, overlay *mockOverlay, n int) {
	t.Helper()
	waitFor(t, func() bool { return len(overlay.GetMessages()) >= n }, 2*time.Second)
}

// startDispatcher запускает Dispatcher.Run в фоне и возвращает его,
// textStream и cancel для завершения.
func startDispatcher(t *testing.T, engine *mockEngine) (*Dispatcher, chan common.STTEvent, context.CancelFunc) {
	t.Helper()
	d := New(&mockOverlay{}, engine, &mockLogger{}, DefaultConfig())
	ctx, cancel := context.WithCancel(context.Background())
	textStream := make(chan common.STTEvent, 8)
	done := make(chan struct{}, 1)
	go d.Run(ctx, textStream, done)
	return d, textStream, cancel
}

// startDispatcherWithOverlay — как startDispatcher, но возвращает overlay для
// проверки UI-сообщений (шапок AnswerCandidates). engine принимает любой
// AnswerGenerator (mockEngine или blockingEngine).
func startDispatcherWithOverlay(t *testing.T, engine AnswerGenerator, cfg Config) (*Dispatcher, *mockOverlay, chan common.STTEvent, context.CancelFunc) {
	t.Helper()
	overlay := &mockOverlay{}
	d := New(overlay, engine, &mockLogger{}, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	textStream := make(chan common.STTEvent, 8)
	done := make(chan struct{}, 1)
	go d.Run(ctx, textStream, done)
	return d, overlay, textStream, cancel
}

func question(text string) common.STTEvent {
	return common.STTEvent{
		Event:     common.EventEndOfTurn,
		ChannelID: "speaker",
		Text:      text,
		Timestamp: time.Now(),
	}
}

// lastCandidates возвращает последнее AnswerCandidates-сообщение overlay и ok.
func lastCandidates(overlay *mockOverlay) (ui.UIMessage, bool) {
	var last ui.UIMessage
	found := false
	for _, m := range overlay.GetMessages() {
		if m.Type == ui.AnswerCandidates {
			last = m
			found = true
		}
	}
	return last, found
}

// blockingEngine — LLM-мок, который блокирует генерацию до закрытия release
// (или отмены ctx). Позволяет проверить состояние UI/таймлайна в момент, когда
// генерация ещё не завершена. Горутина освобождается через ctx.Done()/release.
type blockingEngine struct {
	answers []string
	release <-chan struct{}

	mu       sync.Mutex
	calls    []string
	commands []translator.GenerationCommand
	closed   bool
	done     chan struct{}
}

func (b *blockingEngine) GenerateAnswers(ctx context.Context, req translator.AnswerRequest) ([]string, error) {
	b.mu.Lock()
	b.calls = append(b.calls, req.Question)
	b.commands = append(b.commands, req.Command)
	if b.done == nil {
		b.done = make(chan struct{})
	}
	b.mu.Unlock()

	select {
	case <-b.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return b.answers, nil
}

func (b *blockingEngine) Calls() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.calls))
	copy(out, b.calls)
	return out
}

// Commands возвращает команды всех вызовов в порядке поступления.
func (b *blockingEngine) Commands() []translator.GenerationCommand {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]translator.GenerationCommand, len(b.commands))
	copy(out, b.commands)
	return out
}

// snapshotOf возвращает снимок таймлайна под тестовым доступом.
func snapshotOf(d *Dispatcher) []QEntry {
	return d.timeline.Snapshot()
}

// ── Авто-ответ (AutoAnswer=true, default) ────────────────────────────────

// TestAutoAnswerF1AfterReadyGoesMoreContext — при включённом авто-ответе
// первый F1 после готового авто-ответа сразу даёт CommandMoreContext (Step уже 1).
func TestAutoAnswerF1AfterReadyGoesMoreContext(t *testing.T) {
	engine := &mockEngine{answers: []string{"EN: A | RU: A"}}
	d, overlay, textStream, cancel := startDispatcherWithOverlay(t, engine, DefaultConfig())
	defer cancel()

	textStream <- question("Tell me about Project X.")
	waitFor(t, func() bool { return len(engine.Calls()) == 1 }, 2*time.Second)
	// Авто-ответ = CommandAnswer (Step метки стал 1).
	if reqs := engine.Reqs(); reqs[0].Command != translator.CommandAnswer {
		t.Fatalf("авто-ответ: Command = %v, ожидался CommandAnswer", reqs[0].Command)
	}

	// F1 после Ready → следующий шаг цикла = CommandMoreContext.
	d.HandleDemand()
	waitFor(t, func() bool { return len(engine.Calls()) == 2 }, 2*time.Second)

	reqs := engine.Reqs()
	if reqs[1].Command != translator.CommandMoreContext {
		t.Errorf("F1 после авто-ответа: Command = %v, ожидался CommandMoreContext", reqs[1].Command)
	}

	// Шапка показывает режим "more context".
	if msg, ok := lastCandidates(overlay); ok {
		if len(msg.Answers) == 0 || msg.Answers[0].Mode != "more context" {
			t.Errorf("Mode шапки = %v, ожидался 'more context'", msg.Answers)
		}
	} else {
		t.Error("AnswerCandidates не найдены в UI")
	}
}

// TestAutoAnswerFalseF1ThenMoreContext — AutoAnswer=false: вопрос создаёт метку
// Idle без задания; F1 даёт CommandAnswer; второй F1 (после Ready) — MoreContext.
func TestAutoAnswerFalseF1ThenMoreContext(t *testing.T) {
	auto := false
	cfg := DefaultConfig()
	cfg.AutoAnswer = &auto

	engine := &mockEngine{answers: []string{"EN: A | RU: A"}}
	d, overlay, textStream, cancel := startDispatcherWithOverlay(t, engine, cfg)
	defer cancel()

	textStream <- question("Tell me about Project X.")
	// Убеждаемся, что метка создана и обработана, но генерации не случилось.
	waitFor(t, func() bool { return len(snapshotOf(d)) == 1 }, 2*time.Second)
	waitIdle(t, d)
	// Вопрос не должен запускать генерацию.
	if calls := engine.Calls(); len(calls) != 0 {
		t.Fatalf("AutoAnswer=false: генерация запущена без F1, вызовов %d", len(calls))
	}

	// Метка существует в состоянии Idle.
	snap := snapshotOf(d)
	if len(snap) != 1 {
		t.Fatalf("метка не создана: %d", len(snap))
	}
	if snap[0].State != QIdle {
		t.Fatalf("состояние метки = %v, ожидалось QIdle", snap[0].State)
	}

	// Первый F1 → CommandAnswer.
	d.HandleDemand()
	waitFor(t, func() bool { return len(engine.Calls()) == 1 }, 2*time.Second)
	if reqs := engine.Reqs(); reqs[0].Command != translator.CommandAnswer {
		t.Errorf("первый F1: Command = %v, ожидался CommandAnswer", reqs[0].Command)
	}
	waitFor(t, func() bool { return snapshotOf(d)[0].State == QReady }, 2*time.Second)

	// Второй F1 (после Ready) → CommandMoreContext.
	d.HandleDemand()
	waitFor(t, func() bool { return len(engine.Calls()) == 2 }, 2*time.Second)
	if reqs := engine.Reqs(); reqs[1].Command != translator.CommandMoreContext {
		t.Errorf("второй F1: Command = %v, ожидался CommandMoreContext", reqs[1].Command)
	}

	// В overlay была хотя бы одна шапка AnswerCandidates.
	if _, ok := lastCandidates(overlay); !ok {
		t.Error("AnswerCandidates не найдены в UI")
	}
}

// ── Навигация Prev/Next и F1 на неактивной метке ─────────────────────────

// TestF1AfterNavigationAnswersQ1 — Q1,Q2,Q3 → Prev,Prev → F1: ответ ложится в
// Q1 (проверяем через Snapshot), и шапка в UI указывает на QuestionIdx=0.
func TestF1AfterNavigationAnswersQ1(t *testing.T) {
	auto := false
	cfg := DefaultConfig()
	cfg.AutoAnswer = &auto

	engine := &mockEngine{answers: []string{"EN: answer-Q1 | RU: ответ"}}
	d, overlay, textStream, cancel := startDispatcherWithOverlay(t, engine, cfg)
	defer cancel()

	for _, q := range []string{"Q1?", "Q2?", "Q3?"} {
		textStream <- question(q)
	}
	waitFor(t, func() bool { return len(snapshotOf(d)) == 3 }, 2*time.Second)

	// Уходим на Q1.
	d.HandlePrev()
	d.HandlePrev()
	waitFor(t, func() bool {
		idx, _, _ := d.timeline.Active()
		return idx == 0
	}, 2*time.Second)

	// F1 отвечает на активную метку Q1.
	d.HandleDemand()

	waitFor(t, func() bool {
		snap := snapshotOf(d)
		return len(snap) == 3 && snap[0].State == QReady
	}, 2*time.Second)

	snap := snapshotOf(d)
	if len(snap[0].Answer) != 1 || snap[0].Answer[0].Source != "EN: answer-Q1" {
		t.Errorf("ответ Q1: %+v, ожидался answer-Q1", snap[0].Answer)
	}
	// Q2/Q3 не должны иметь ответов.
	if len(snap[1].Answer) != 0 || len(snap[2].Answer) != 0 {
		t.Errorf("Q2/Q3 получили ответы: %+v / %+v", snap[1].Answer, snap[2].Answer)
	}

	// Шапка в UI указывает на Q1 (QuestionIdx=0).
	if msg, ok := lastCandidates(overlay); ok {
		if len(msg.Answers) == 0 {
			t.Fatalf("шапка без Answers: %+v", msg)
		}
		if msg.Answers[0].QuestionIdx != 0 {
			t.Errorf("QuestionIdx шапки = %d, ожидался 0", msg.Answers[0].QuestionIdx)
		}
		if msg.Answers[0].Total != 3 {
			t.Errorf("Total шапки = %d, ожидался 3", msg.Answers[0].Total)
		}
	} else {
		t.Error("AnswerCandidates не найдены в UI")
	}
}

// TestPushHeaderIgnoresUnknownIdx — чистый юнит-тест pushHeader по НЕверному idx:
// метка с таким абсолютным номером в окне не найдена → шапка не отправляется,
// overlay не получает ни одного AnswerCandidates. Так pushHeader защищает
// активную метку от перезаписи чужой/устаревшей меткой.
//
// (Прежний TestStaleAnswerDoesNotOverwriteHeader имитировал «пользователь ушёл
// на Q3 во время генерации Q1» прямой мутацией таймлайна, но сценарий
// недостижим: tail-follow (questions.go Add) сам двигает активную метку на
// хвост, а busy-guard блокирует навигацию во время генерации. Реальный путь
// навигации-при-генерации покрыт TestNavigationDuringGenerationIgnoredAnswerToCorrectLabel.)
func TestPushHeaderIgnoresUnknownIdx(t *testing.T) {
	d := New(&mockOverlay{}, &mockEngine{}, &mockLogger{}, DefaultConfig())
	overlay := d.overlay.(*mockOverlay)

	for _, q := range []string{"Q1?", "Q2?", "Q3?"} {
		d.timeline.Add(q)
	}

	// idx вне окна (метка не существует) — шапка не уходит.
	d.pushHeader(99, nil)

	if msgs := overlay.GetMessages(); len(msgs) != 0 {
		t.Errorf("pushHeader по неизвестному idx отправил сообщения в UI: %+v", msgs)
	}
}

// TestNavigationDuringGenerationIgnoredAnswerToCorrectLabel (D6) — РЕАЛЬНЫЙ путь
// навигации (публичные HandleNext/HandlePrev), а не прямая мутация таймлайна:
//
//  1. навигация во время генерации игнорируется busy-guard'ом (метка НЕ едет);
//  2. ответ приходит в метку своего задания (Q1), шапка активного вопроса не
//     перезаписывается ответом чужой метки;
//  3. после генерации навигация снова работает (игнор был именно busy-guard,
//     а не залипание).
//
// Дополняет юнит-тест TestPushHeaderIgnoresUnknownIdx.
func TestNavigationDuringGenerationIgnoredAnswerToCorrectLabel(t *testing.T) {
	auto := false
	cfg := DefaultConfig()
	cfg.AutoAnswer = &auto

	release := make(chan struct{})
	engine := &blockingEngine{answers: []string{"EN: Q1 answer | RU: ответ Q1"}, release: release}

	overlay := &mockOverlay{}
	logger := &mockLogger{}
	d := New(overlay, engine, logger, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	textStream := make(chan common.STTEvent, 8)
	done := make(chan struct{}, 1)
	go d.Run(ctx, textStream, done)

	// Создаём Q1, Q2, Q3 (генерации нет — AutoAnswer=false).
	for _, q := range []string{"Q1?", "Q2?", "Q3?"} {
		textStream <- question(q)
	}
	waitFor(t, func() bool { return len(snapshotOf(d)) == 3 }, 2*time.Second)

	// Реальная навигация назад на Q1 (публичный API, генерации нет → работает).
	d.HandlePrev()
	d.HandlePrev()
	waitFor(t, func() bool { idx, _, _ := d.timeline.Active(); return idx == 0 }, 2*time.Second)

	// Запускаем генерацию Q1 и удерживаем её (busy) через blockingEngine.
	d.HandleDemand()
	waitFor(t, func() bool { return d.busy.Load() }, 2*time.Second)
	if idx, _, _ := d.timeline.Active(); idx != 0 {
		t.Fatalf("активная метка до навигации = %d, ожидалась 0", idx)
	}

	// Пока генерация идёт — реальная навигация (HandleNext×2 → на Q3) ДОЛЖНА
	// быть проигнорирована busy-guard'ом: активная метка остаётся Q1.
	d.HandleNext()
	d.HandleNext()

	// Ждём коалесинг-лог (детерминированно, без sleep): навигация отброшена.
	waitFor(t, func() bool {
		return logger.HasDebug("F3 во время генерации пропущен (коалесинг)")
	}, 2*time.Second)

	if idx, _, _ := d.timeline.Active(); idx != 0 {
		t.Errorf("навигация во время генерации НЕ игнорирована: активная метка = %d, ожидалась 0 (Q1)", idx)
	}

	// Освобождаем генерацию Q1.
	close(release)
	waitFor(t, func() bool { return snapshotOf(d)[0].State == QReady }, 2*time.Second)
	waitIdle(t, d)
	// Даём закрыться возможным отложенным hotkey-действиям (их быть не должно).
	if idx, _, _ := d.timeline.Active(); idx != 0 {
		t.Errorf("после генерации активная метка = %d, ожидалась 0 (навигация не должна была примениться)", idx)
	}

	// Ответ ушёл в правильную метку Q1 и в её шапку; Q2/Q3 ответов не получили.
	snap := snapshotOf(d)
	if len(snap[0].Answer) == 0 {
		t.Errorf("ответ не записан в метку задания Q1: %+v", snap[0])
	}
	if snap[0].Answer[0].Target != "ответ Q1" {
		t.Errorf("ответ в метку Q1 перепутан: %+v", snap[0].Answer)
	}
	if len(snap[1].Answer) != 0 || len(snap[2].Answer) != 0 {
		t.Errorf("Q2/Q3 получили чужие ответы: %+v / %+v", snap[1].Answer, snap[2].Answer)
	}

	// Шапка активного вопроса — про Q1, а не перезаписана чужой меткой.
	msg, ok := lastCandidates(overlay)
	if !ok || len(msg.Answers) == 0 {
		t.Fatalf("AnswerCandidates не найдены: %+v", msg)
	}
	if msg.Answers[0].QuestionIdx != 0 {
		t.Errorf("шапка активного вопроса = idx %d, ожидался 0 (Q1)", msg.Answers[0].QuestionIdx)
	}
	if msg.Answers[0].QuestionText != "Q1?" {
		t.Errorf("текст шапки = %q, ожидался %q (шапка перезаписана чужим вопросом)", msg.Answers[0].QuestionText, "Q1?")
	}

	// После завершения генерации (busy снят) навигация снова работает —
	// подтверждает, что игнор был busy-guard'ом, а не залипанием метки.
	d.HandleNext()
	d.HandleNext()
	waitFor(t, func() bool { idx, _, _ := d.timeline.Active(); return idx == 2 }, 2*time.Second)
}

// ── Коалесинг F1 во время генерации ─────────────────────────────────────

// TestF1DuringGenerationIgnored — F1 в состоянии QGenerating игнорируется,
// новых заданий в LLM не появляется.
func TestF1DuringGenerationIgnored(t *testing.T) {
	auto := false
	cfg := DefaultConfig()
	cfg.AutoAnswer = &auto

	engine := &mockEngine{answers: []string{"EN: answer | RU: ответ"}, delay: 300 * time.Millisecond}
	d, _, textStream, cancel := startDispatcherWithOverlay(t, engine, cfg)
	defer cancel()

	textStream <- question("Q1?")
	waitFor(t, func() bool { return len(snapshotOf(d)) == 1 }, 2*time.Second)

	d.HandleDemand()
	// Ждём начала генерации.
	waitFor(t, func() bool { return snapshotOf(d)[0].State == QGenerating }, 2*time.Second)

	// Несколько F1 во время генерации — игнор (серия без пауз: сами вызовы не
	// ставят заданий).
	for i := 0; i < 5; i++ {
		d.HandleDemand()
	}

	// Даём завершиться единственной генерации и опустеть очереди (без sleep:
	// негативный ассерт обязан проверяться ПОСЛЕ полного покоя воркера).
	waitFor(t, func() bool { return snapshotOf(d)[0].State == QReady }, 2*time.Second)
	waitIdle(t, d)

	if calls := engine.Calls(); len(calls) != 1 {
		t.Errorf("F1 во время генерации не должен создавать заданий: вызовов %d, ожидалось 1", len(calls))
	}
}

// ── Esc ─────────────────────────────────────────────────────────────────

// TestEscKeepsLabelsResetsPending — Esc: метки целы, Queued/Generating → Idle,
// очередь пуста, новый вопрос снова генерит.
func TestEscKeepsLabelsResetsPending(t *testing.T) {
	engine := &mockEngine{answers: []string{"EN: A | RU: A"}, delay: 200 * time.Millisecond}
	d, _, textStream, cancel := startDispatcherWithOverlay(t, engine, DefaultConfig())
	defer cancel()

	// Q1 берётся в работу (генерируется), Q2..Q4 стоят в очереди.
	for _, q := range []string{"Q1?", "Q2?", "Q3?", "Q4?"} {
		textStream <- question(q)
	}
	// Ждём, пока Q1 начнёт генерироваться (очередь заполнится).
	waitFor(t, func() bool {
		snap := snapshotOf(d)
		return len(snap) > 0 && snap[0].State == QGenerating
	}, 2*time.Second)

	d.Cancel()

	// Ждём обработки отмены.
	waitFor(t, func() bool {
		for _, e := range snapshotOf(d) {
			if e.State == QQueued || e.State == QGenerating {
				return false
			}
		}
		return true
	}, 2*time.Second)

	// Метки целы (4 штуки), очередь пуста.
	snap := snapshotOf(d)
	if len(snap) != 4 {
		t.Fatalf("после Esc меток %d, ожидалось 4 (метки должны сохраняться)", len(snap))
	}
	if n := len(d.answerCh); n != 0 {
		t.Errorf("очередь после Esc не пуста: %d заданий", n)
	}

	callsAfterEsc := len(engine.Calls())

	// Новый вопрос снова запускает генерацию.
	textStream <- question("Q5?")
	waitFor(t, func() bool { return len(engine.Calls()) > callsAfterEsc }, 2*time.Second)
}

// ── Пустой таймлайн ─────────────────────────────────────────────────────

// TestHotkeysEmptyTimelineNoPanic — F1/F2/F3 на пустом таймлайне — no-op без паники.
func TestHotkeysEmptyTimelineNoPanic(t *testing.T) {
	engine := &mockEngine{answers: []string{"EN: A | RU: A"}}
	d, _, _, cancel := startDispatcherWithOverlay(t, engine, DefaultConfig())
	defer cancel()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("паника на пустом таймлайне: %v", r)
		}
	}()

	d.HandleDemand()
	d.HandlePrev()
	d.HandleNext()
	// Даём воркеру обработать все действия до полного покоя, затем проверяем,
	// что на пустом таймлайне не ушло ни одного запроса в LLM.
	waitIdle(t, d)

	if calls := engine.Calls(); len(calls) != 0 {
		t.Errorf("на пустом таймлайне вызовов %d, ожидалось 0", len(calls))
	}
}

// ── RecordAnswer только для хвоста ──────────────────────────────────────

// TestRecordAnswerOnlyOnTail — RecordAnswer пишется только для хвостовой метки.
func TestRecordAnswerOnlyOnTail(t *testing.T) {
	auto := false
	cfg := DefaultConfig()
	cfg.AutoAnswer = &auto

	engine := &mockEngine{answers: []string{"EN: tail-answer | RU: ответ"}}
	d, _, textStream, cancel := startDispatcherWithOverlay(t, engine, cfg)
	defer cancel()

	// Q1, Q2, Q3.
	for _, q := range []string{"Q1?", "Q2?", "Q3?"} {
		textStream <- question(q)
	}
	waitFor(t, func() bool { return len(snapshotOf(d)) == 3 }, 2*time.Second)

	// Уходим на Q2 (не хвост: хвост — Q3) и отвечаем.
	d.HandlePrev()
	waitFor(t, func() bool { idx, _, _ := d.timeline.Active(); return idx == 1 }, 2*time.Second)
	d.HandleDemand()

	waitFor(t, func() bool { return snapshotOf(d)[1].State == QReady }, 2*time.Second)

	// Ответ на не-хвостовую метку НЕ должен попасть в историю.
	if recent := d.history.Recent(); len(recent) != 0 {
		t.Errorf("ответ на не-хвостовую метку записан в историю: %+v", recent)
	}

	// Возвращаемся на хвост (Q3) и отвечаем — вот теперь история пишется.
	d.HandleNext()
	waitFor(t, func() bool { idx, _, _ := d.timeline.Active(); return idx == 2 }, 2*time.Second)
	callsBefore := len(engine.Calls())
	d.HandleDemand()
	waitFor(t, func() bool { return len(engine.Calls()) > callsBefore }, 2*time.Second)
	waitFor(t, func() bool { return len(d.history.Recent()) == 1 }, 2*time.Second)

	recent := d.history.Recent()
	if recent[0].Question != "Q3?" {
		t.Errorf("в историю записан неверный вопрос: %q, ожидался Q3?", recent[0].Question)
	}
}

// TestAutoAnswerFalseRegenerationDoesNotAddTurn — повторный F1 (regeneration) на
// хвостовую метку не создаёт новый turn в истории.
func TestAutoAnswerFalseRegenerationDoesNotAddTurn(t *testing.T) {
	auto := false
	cfg := DefaultConfig()
	cfg.AutoAnswer = &auto

	engine := &mockEngine{answers: []string{"EN: A | RU: A"}}
	d, _, textStream, cancel := startDispatcherWithOverlay(t, engine, cfg)
	defer cancel()

	textStream <- question("Tell me about Project X.")
	waitFor(t, func() bool { return len(snapshotOf(d)) == 1 }, 2*time.Second)

	d.HandleDemand()
	waitFor(t, func() bool { return len(engine.Calls()) == 1 }, 2*time.Second)
	waitFor(t, func() bool { return len(d.history.Recent()) == 1 }, 2*time.Second)

	// Второй F1 — regeneration (MoreContext) на хвостовую метку.
	d.HandleDemand()
	waitFor(t, func() bool { return len(engine.Calls()) == 2 }, 2*time.Second)
	waitIdle(t, d)

	recent := d.history.Recent()
	if len(recent) != 1 {
		t.Fatalf("regeneration создала turn: %d, ожидалось 1", len(recent))
	}
	if recent[0].Question != "Tell me about Project X." {
		t.Errorf("вопрос в истории: %q", recent[0].Question)
	}
}

// ── Task 1: немедленная шапка при обнаружении вопроса ────────────────────

// TestEnqueueQuestionPushesHeaderImmediately — Task 1: шапка метки уходит в
// overlay сразу при обнаружении вопроса, ДО завершения генерации. При
// блокированном LLM-моке overlay должен получить AnswerCandidates с State
// queued (AutoAnswer=true: задание поставлено) либо idle (AutoAnswer=false:
// метка ждёт F1) — иначе метка невидима для пользователя, пока молчит LLM.
func TestEnqueueQuestionPushesHeaderImmediately(t *testing.T) {
	auto := false
	cfg := DefaultConfig()
	cfg.AutoAnswer = &auto

	// release блокирует мок LLM: генерация не завершится до конца теста.
	release := make(chan struct{})
	defer close(release)
	engine := &blockingEngine{release: release}

	d, overlay, textStream, cancel := startDispatcherWithOverlay(t, engine, cfg)
	defer cancel()

	textStream <- question("Tell me about Project X.")

	// Шапка появляется до какой-либо генерации (release ещё не закрыт).
	waitFor(t, func() bool {
		msg, ok := lastCandidates(overlay)
		return ok && len(msg.Answers) > 0
	}, 2*time.Second)

	msg, _ := lastCandidates(overlay)
	a := msg.Answers[0]
	if a.QuestionIdx != 0 {
		t.Errorf("QuestionIdx шапки = %d, ожидался 0", a.QuestionIdx)
	}
	if a.Total != 1 {
		t.Errorf("Total шапки = %d, ожидался 1", a.Total)
	}
	if a.QuestionText != "Tell me about Project X." {
		t.Errorf("QuestionText шапки = %q, ожидался текст вопроса", a.QuestionText)
	}
	if a.Source != "" || a.Target != "" {
		t.Errorf("каркасная шапка несёт текст ответа: Source=%q Target=%q", a.Source, a.Target)
	}

	// AutoAnswer=false: LLM не вызывался, метка ждёт F1 → idle.
	if calls := engine.Calls(); len(calls) != 0 {
		t.Fatalf("AutoAnswer=false: вызовов LLM %d, ожидалось 0", len(calls))
	}
	if snap := snapshotOf(d); len(snap) != 1 || snap[0].State != QIdle {
		t.Fatalf("состояние метки = %+v, ожидалось QIdle", snap)
	}
	if a.State != QIdle.String() {
		t.Errorf("State шапки = %q, ожидался %q", a.State, QIdle.String())
	}
}

// TestEnqueueQuestionHeaderQueuedWhileGenerating — Task 1 + AutoAnswer=true:
// шапка уходит с State=queued сразу, ещё до того как worker возьмёт задание
// (генерация заблокирована моком LLM).
func TestEnqueueQuestionHeaderQueuedWhileGenerating(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	engine := &blockingEngine{release: release}

	_, overlay, textStream, cancel := startDispatcherWithOverlay(t, engine, DefaultConfig())
	defer cancel()

	textStream <- question("Tell me about Project X.")

	// Ждём старта генерации (мок получил запрос и заблокировался).
	waitFor(t, func() bool { return len(engine.Calls()) == 1 }, 2*time.Second)

	// К моменту старта генерации шапка уже должна была уйти в overlay.
	msg, ok := lastCandidates(overlay)
	if !ok || len(msg.Answers) == 0 {
		t.Fatalf("шапка не отправлена при обнаружении вопроса: %+v", msg)
	}
	if st := msg.Answers[0].State; st != QQueued.String() && st != QGenerating.String() {
		t.Errorf("State шапки = %q, ожидался %q или %q (шапка должна уйти ПОСЛЕ перевода метки в очередь)", st, QQueued.String(), QGenerating.String())
	}
	if len(msg.Answers[0].Source) != 0 {
		t.Errorf("до завершения генерации в шапке появился текст ответа: %q", msg.Answers[0].Source)
	}
}

// ── Task 3: Prev/Next игнорируются во время генерации ────────────────────

// TestPrevNextIgnoredDuringGeneration — Task 3: пока worker генерирует
// (busy), HandlePrev/HandleNext дропаются (как HandleDemand). После
// разблокировки LLM активная метка не сдвигается.
func TestPrevNextIgnoredDuringGeneration(t *testing.T) {
	auto := false
	cfg := DefaultConfig()
	cfg.AutoAnswer = &auto

	release := make(chan struct{})
	engine := &blockingEngine{answers: []string{"EN: answer | RU: ответ"}, release: release}

	d, _, textStream, cancel := startDispatcherWithOverlay(t, engine, cfg)
	defer cancel()

	textStream <- question("Q1?")
	waitFor(t, func() bool {
		snap := snapshotOf(d)
		return len(snap) == 1 && snap[0].State == QIdle
	}, 2*time.Second)

	d.HandleDemand()
	// Генерация началась и заблокирована.
	waitFor(t, func() bool { return len(engine.Calls()) == 1 }, 2*time.Second)
	waitFor(t, func() bool { return d.busy.Load() }, 2*time.Second)
	waitFor(t, func() bool {
		snap := snapshotOf(d)
		return len(snap) == 1 && snap[0].State == QGenerating
	}, 2*time.Second)

	// Пока идёт генерация, сдвигаем таймлайн вперёд (метка Q1 активна, Prev
	// доступен — но защёлкивается busy-guard'ом).
	d.HandlePrev()
	d.HandleNext()
	// Ждём коалесинг-логи hotkey-действий (детерминированно, без sleep):
	// Prev и Next отброшены busy-guard'ом.
	waitFor(t, func() bool {
		return len(d.hotkeyCh) == 0 && d.busy.Load()
	}, 2*time.Second)

	// Разблокируем LLM и ждём завершения генерации.
	close(release)
	waitFor(t, func() bool {
		snap := snapshotOf(d)
		return len(snap) == 1 && snap[0].State == QReady
	}, 2*time.Second)
	// Ждём полного покоя воркера: отложенных Prev/Next быть не должно, поэтому
	// ассерт ниже проверяет состояние ПОСЛЕ обработки, а не по таймингу.
	waitIdle(t, d)

	if idx, _, _ := d.timeline.Active(); idx != 0 {
		t.Errorf("активная метка сдвинулась во время генерации: idx = %d, ожидался 0", idx)
	}
}

// ── Task 4: F1 после Esc снова доступен ──────────────────────────────────

// TestHandleDemandResetsCancelledAfterEsc — Task 4: Esc отменяет активную
// генерацию (cancelled=true). Последовательность Esc → F1 → F1 должна
// снова довести метку до QReady: handleDemand сбрасывает cancelled, иначе
// второе задание молча дропается воркером и метка навсегда остаётся QQueued.
func TestHandleDemandResetsCancelledAfterEsc(t *testing.T) {
	auto := false
	cfg := DefaultConfig()
	cfg.AutoAnswer = &auto

	release := make(chan struct{})
	engine := &blockingEngine{answers: []string{"EN: A | RU: A"}, release: release}

	d, overlay, textStream, cancel := startDispatcherWithOverlay(t, engine, cfg)
	defer cancel()

	textStream <- question("Q1?")
	waitFor(t, func() bool {
		snap := snapshotOf(d)
		return len(snap) == 1 && snap[0].State == QIdle
	}, 2*time.Second)

	d.HandleDemand()
	waitFor(t, func() bool {
		snap := snapshotOf(d)
		return len(snap) == 1 && snap[0].State == QGenerating
	}, 2*time.Second)

	// Esc: активная генерация отменяется, cancelled=true.
	d.Cancel()
	waitFor(t, func() bool { return d.cancelled.Load() }, 2*time.Second)
	// Ждём, пока worker обработает отмену (busy снимется, метка → QIdle) —
	// иначе следующий F1 защёлкнется busy-guard'ом самого HandleDemand.
	waitFor(t, func() bool { return !d.busy.Load() }, 2*time.Second)
	waitFor(t, func() bool { return snapshotOf(d)[0].State == QIdle }, 2*time.Second)

	// F1 после Esc сбрасывает cancelled и снова ставит задание; генерация
	// снова блокируется моком (release ещё открыт).
	d.HandleDemand()
	waitFor(t, func() bool { return d.busy.Load() }, 2*time.Second)

	if d.cancelled.Load() {
		t.Error("handleDemand не сбросил cancelled после Esc")
	}

	// Второй F1 во время генерации — коалесинг, но cancelled НЕ должен
	// вернуться в true (иначе последующая метка залипнет).
	d.HandleDemand()
	if d.cancelled.Load() {
		t.Error("F1 во время генерации вновь выставил cancelled")
	}

	// Разблокируем LLM: ответ должен дойти до overlay и метка стать QReady.
	close(release)

	waitFor(t, func() bool {
		msg, ok := lastCandidates(overlay)
		return ok && len(msg.Answers) > 0 && msg.Answers[0].State == QReady.String()
	}, 3*time.Second)

	waitFor(t, func() bool {
		snap := snapshotOf(d)
		return len(snap) == 1 && snap[0].State == QReady
	}, 3*time.Second)

	msg, _ := lastCandidates(overlay)
	if msg.Answers[0].Source != "EN: A" {
		t.Errorf("ответ в overlay = %q, ожидался EN: A", msg.Answers[0].Source)
	}
}

// ── A-T1: Esc не должен пропускать шаг цикла F1 (A-1) ────────────────────

// TestEscThenF1KeepsCommandCycle — регресс A-1: вопрос сгенерирован
// авто-ответом (AutoAnswer=true), затем Esc (ResetPending). Первый F1 после Esc
// обязан снова дать CommandAnswer, второй — CommandMoreContext, а НЕ
// CommandSimplerEnglish (баг: NextStep продвигал Step в QIdle, а его результат
// отбрасывался в пользу хардкода CommandAnswer — шаг цикла терялся).
func TestEscThenF1KeepsCommandCycle(t *testing.T) {
	engine := &mockEngine{answers: []string{"EN: A | RU: A"}, delay: 200 * time.Millisecond}
	d, _, textStream, cancel := startDispatcherWithOverlay(t, engine, DefaultConfig())
	defer cancel()

	// Q1 берётся в работу (генерируется), Q2..Q3 стоят в очереди.
	for _, q := range []string{"Q1?", "Q2?", "Q3?"} {
		textStream <- question(q)
	}
	waitFor(t, func() bool {
		snap := snapshotOf(d)
		return len(snap) > 0 && snap[0].State == QGenerating
	}, 2*time.Second)

	// Esc: очередь дропается, состояния Queued/Generating → Idle, Step → 0.
	d.Cancel()
	waitFor(t, func() bool { return !d.busy.Load() }, 2*time.Second)
	waitFor(t, func() bool {
		for _, e := range snapshotOf(d) {
			if e.State == QQueued || e.State == QGenerating {
				return false
			}
		}
		return true
	}, 2*time.Second)

	// Уходим на первую метку (Q1) — на ней проверяем цикл команд.
	for {
		idx, _, _ := d.timeline.Active()
		if idx == 0 {
			break
		}
		d.timeline.Prev()
	}

	// Первый F1 после Esc → снова CommandAnswer (не пропуск шага).
	callsBefore := len(engine.Calls())
	d.HandleDemand()
	waitFor(t, func() bool { return len(engine.Calls()) > callsBefore }, 2*time.Second)
	waitFor(t, func() bool { return snapshotOf(d)[0].State == QReady }, 2*time.Second)

	reqs := engine.Reqs()
	if got := reqs[callsBefore].Command; got != translator.CommandAnswer {
		t.Fatalf("F1 после Esc: Command = %v, ожидался CommandAnswer", got)
	}

	// Второй F1 → CommandMoreContext, а не CommandSimplerEnglish.
	callsBefore = len(engine.Calls())
	d.HandleDemand()
	waitFor(t, func() bool { return len(engine.Calls()) > callsBefore }, 2*time.Second)

	reqs = engine.Reqs()
	if got := reqs[callsBefore].Command; got != translator.CommandMoreContext {
		t.Errorf("второй F1 после Esc: Command = %v, ожидался CommandMoreContext (CommandSimplerEnglish = пропуск шага)", got)
	}
}

// ── C1: cancelled-дроп задания не должен двигать Step ────────────────────

// TestCancelledDropResetsStep — регресс C1 (остаточный A-1). Ветка
// dispatcher.go, где worker читает задание из answerCh при cancelled=true
// (дроп после Esc), ОБЯЗАНА сбрасывать Step — симметрично dropQueue/
// enqueueAnswer. Иначе у дропнутой метки остаётся Step=1, и первый F1 после
// Esc в QIdle-ветке вернёт CommandMoreContext вместо CommandAnswer (A-1).
//
// Ветка выбрана ДЕТЕРМИНИРОВАННО, а не лотереей select (worker читает
// answerCh и cancelCh с равной вероятностью):
//  1. Q1 генерируется (blockingEngine держит worker занятым);
//  2. Cancel() — cancelled=true + токен в cancelCh;
//  3. worker берёт токен cancelCh (answerCh пуст → взять больше нечего) и
//     опустошает пустую очередь (dropQueue — no-op);
//  4. только теперь в answerCh кладутся Q2/Q3 (Step=1) — worker читает их
//     при cancelled=true, т.е. именно в целевой ветке.
//
// Без фиксированных sleep: состояния читаются через waitFor/waitIdle.
func TestCancelledDropResetsStep(t *testing.T) {
	release := make(chan struct{})
	engine := &blockingEngine{answers: []string{"EN: A | RU: A"}, release: release}

	d, _, textStream, cancel := startDispatcherWithOverlay(t, engine, DefaultConfig()) // AutoAnswer=true
	defer cancel()

	// 1. Q1 берётся в работу (генерируется, worker занят).
	textStream <- question("Q1?")
	waitFor(t, func() bool {
		snap := snapshotOf(d)
		return len(snap) == 1 && snap[0].State == QGenerating && d.busy.Load()
	}, 2*time.Second)

	// 2. Esc во время генерации: cancelled=true, активная генерация отменяется.
	d.Cancel()
	waitFor(t, func() bool { return d.cancelled.Load() }, 2*time.Second)

	// 3. Освобождаем Q1. Worker выходит из генерации, возвращает Q1 в QIdle и
	// читает токен cancelCh (answerCh пуст) — очередь уже опустошена dropQueue.
	close(release)
	waitIdle(t, d)
	waitFor(t, func() bool { return len(d.cancelCh) == 0 }, 2*time.Second)

	// 4. Кладём в очередь Q2/Q3 при живом cancelled=true. enqueueAnswer — только
	// постановка задания (не сбрасывает cancelled, в отличие от enqueueQuestion).
	// Каждой метке даём Step=1 (как это делает авто-постановка через NextStep);
	// Step=1 фиксируем ДО enqueueAnswer, пока задание ещё не в канале, — иначе
	// worker (idle, cancelled=true) успел бы сбросить Step в целевой ветке.
	enqueueQueued := func(q string) int {
		idx := d.timeline.Add(q)
		d.timeline.NextStep(idx) // Step 0→1, как авто-постановка
		d.timeline.SetState(idx, QQueued)
		if e := entryByIdx(snapshotOf(d), idx); e.Step != 1 {
			t.Fatalf("предусловие: Step метки %q = %d, ожидалось 1 (авто-постановка)", q, e.Step)
		}
		d.enqueueAnswer(answerTask{idx: idx, cmd: translator.CommandMoreContext, question: q, fresh: true})
		return idx
	}
	q2 := enqueueQueued("Q2?")
	q3 := enqueueQueued("Q3?")

	// Worker читает Q2/Q3 при cancelled=true → целевая ветка дропа. Ждём именно
	// эффект (обе метки QIdle+Step=0), а не опустошение канала: чтение задания
	// и его SetState/ResetStep происходят в одной горутине, но между receive и
	// мутацией таймлайна есть окно.
	waitFor(t, func() bool {
		snap := snapshotOf(d)
		return len(snap) == 3 &&
			entryByIdx(snap, q2).State == QIdle && entryByIdx(snap, q2).Step == 0 &&
			entryByIdx(snap, q3).State == QIdle && entryByIdx(snap, q3).Step == 0
	}, 2*time.Second)
	waitIdle(t, d)

	// Обе метки — QIdle со Step=0 (симметрично dropQueue).
	for _, idx := range []int{q2, q3} {
		entry := entryByIdx(snapshotOf(d), idx)
		if entry.State != QIdle {
			t.Errorf("метка idx=%d после cancelled-дропа: State = %v, ожидалось QIdle", idx, entry.State)
		}
		if entry.Step != 0 {
			t.Errorf("метка idx=%d после cancelled-дропа: Step = %d, ожидалось 0 (иначе F1 пропустит CommandAnswer)", idx, entry.Step)
		}
	}

	// Последующий F1 (сбрасывает cancelled) обязан начать цикл с CommandAnswer.
	// Активная метка — хвост Q3 (tail-follow). Step=0 ⇒ NextStep вернёт Answer.
	callsBefore := len(engine.Calls())
	d.HandleDemand()
	waitFor(t, func() bool { return len(engine.Calls()) > callsBefore }, 2*time.Second)

	if got := engine.Commands()[callsBefore]; got != translator.CommandAnswer {
		t.Errorf("F1 после cancelled-дропа: Command = %v, ожидался CommandAnswer (CommandMoreContext = пропуск шага, регресс C1)", got)
	}
}

// entryByIdx возвращает метку снапшота по абсолютному idx (fatalf, если нет).
func entryByIdx(snap []QEntry, idx int) QEntry {
	for i := range snap {
		if snap[i].Idx == idx {
			return snap[i]
		}
	}
	return QEntry{Idx: -1}
}

// ── C2: потерянное уточнение возвращается в lastFinal ────────────────────

// TestClarifyRestoresLastFinalOnEvictedLabel — регресс C2. Если AddClarification
// не удался (активная метка вытеснена из окна кольца между Active() и правкой),
// потреблённый из lastFinal текст возвращается обратно: реплика интервьюера не
// теряется безвозвратно и «оживёт» при следующем Shift+F2 на валидной метке.
//
// Настоящую гонку вытеснения детерминированно не воспроизвести, поэтому тест
// проверяет контракт restoreLastFinal напрямую (публичный/тестовый доступ к
// полям пакета): после takeLastFinal текст уходит в локальную переменную и, если
// AddClarification по вытесненному idx возвращает false, restoreLastFinal
// кладёт текст назад.
func TestClarifyRestoresLastFinalOnEvictedLabel(t *testing.T) {
	d := New(&mockOverlay{}, &mockEngine{}, &mockLogger{}, DefaultConfig())

	const text = "I mean horizontally."
	d.setLastFinal(text)

	// Имитируем путь handleClarify: потребляем кандидат…
	got, ok := d.takeLastFinal()
	if !ok || got != text {
		t.Fatalf("takeLastFinal: (%q, %v), ожидалось (%q, true)", got, ok, text)
	}
	if v := lastFinalOf(d); v != "" {
		t.Fatalf("lastFinal после потребления = %q, ожидалось пусто", v)
	}
	// …и убеждаемся, что правка по пустому таймлайну невозможна.
	if d.timeline.AddClarification(0, got) {
		t.Fatal("предусловие: AddClarification по пустому таймлайну вернул true")
	}
	// Провал правки → текст возвращается в lastFinal (как в handleClarify).
	d.restoreLastFinal(got)

	if v := lastFinalOf(d); v != text {
		t.Errorf("lastFinal после restoreLastFinal = %q, ожидалось %q (текст потерян)", v, text)
	}

	// Восстановленный текст применяется обычным путём к существующей метке.
	d.timeline.Add("How do you scale?")
	if !d.timeline.AddClarification(0, text) {
		t.Fatal("AddClarification по существующей метке вернул false")
	}
	if got := d.timeline.Clarifications(0); len(got) != 1 || got[0] != text {
		t.Errorf("Clarifications = %v, ожидалось [%s]", got, text)
	}
}

// ── A-T2: абсолютный счётчик вопросов (A-4) ──────────────────────────────

// TestHeaderTotalIsAbsoluteAcrossEviction — Total, который pushHeader кладёт в
// шапку, — абсолютное число заданных вопросов (TotalAsked), а не размер окна
// кольца. Без фикса pushHeader выставлял Total = len(snap) и на 150-м вопросе
// счётчик «N из M» начинал уменьшаться.
func TestHeaderTotalIsAbsoluteAcrossEviction(t *testing.T) {
	engine := &mockEngine{answers: []string{"EN: A | RU: A"}}
	d, overlay, _, cancel := startDispatcherWithOverlay(t, engine, DefaultConfig())
	defer cancel()

	const extra = 5
	const total = questionTimelineCapacity + extra // ровно cap+extra добавлений

	// Все вопросы уникальны: иначе воркер дедуплицирует подряд идущие fresh-задания.
	for i := 0; i < total; i++ {
		d.enqueueQuestion(fmt.Sprintf("Q%d?", i))
	}

	want := total
	if got := d.timeline.TotalAsked(); got != want {
		t.Fatalf("TotalAsked() = %d, ожидалось %d", got, want)
	}
	snap := snapshotOf(d)
	if len(snap) != questionTimelineCapacity {
		t.Fatalf("len(Snapshot()) = %d, ожидалось %d (окно кольца)", len(snap), questionTimelineCapacity)
	}
	if want <= len(snap) {
		t.Fatalf("тест бессмыслен: total=%d не больше окна=%d", want, len(snap))
	}

	// Абсолютный номер метки-головы окна: base = total - cap.
	headIdx := total - questionTimelineCapacity
	for _, idx := range []int{headIdx, headIdx + 1, total - 1} {
		// Переводим активную позицию на нужную метку и шлём её шапку напрямую
		// (pushHeader — та самая функция, которая заполняет Total).
		target := snapshotOf(d)[idx-headIdx]
		if target.Idx != idx {
			t.Fatalf("снимок: метка позиции %d имеет idx=%d", idx-headIdx, target.Idx)
		}
		d.pushHeader(idx, nil)

		msg, ok := lastCandidates(overlay)
		if !ok || len(msg.Answers) == 0 {
			t.Fatalf("idx=%d: шапка не найдена в overlay: %+v", idx, msg)
		}
		a := msg.Answers[0]
		if a.Total != want {
			t.Errorf("idx=%d: Answer.Total = %d, ожидалось %d (абсолютный, окно хранит %d)",
				idx, a.Total, want, len(snap))
		}
		if a.QuestionIdx != idx {
			t.Errorf("idx=%d: Answer.QuestionIdx = %d", idx, a.QuestionIdx)
		}
	}
}

// TestHeaderTotalGrowsMonotonicallyAcrossEviction — Total в шапках монотонно
// растёт вместе с числом вопросов и никогда не откатывается (проверка
// пользовательского контракта «вопрос N из M» после вытеснения).
func TestHeaderTotalGrowsMonotonicallyAcrossEviction(t *testing.T) {
	engine := &mockEngine{answers: []string{"EN: A | RU: A"}}
	d, overlay, _, cancel := startDispatcherWithOverlay(t, engine, DefaultConfig())
	defer cancel()

	prev := 0
	for i := 0; i < questionTimelineCapacity+10; i++ {
		d.enqueueQuestion(fmt.Sprintf("Q%d?", i))
		idx := d.timeline.TotalAsked() - 1
		d.pushHeader(idx, nil)

		msg, ok := lastCandidates(overlay)
		if !ok || len(msg.Answers) == 0 {
			t.Fatalf("итерация %d: шапка не найдена", i)
		}
		got := msg.Answers[0].Total
		if got != i+1 {
			t.Fatalf("итерация %d: Total = %d, ожидалось %d", i, got, i+1)
		}
		if got < prev {
			t.Fatalf("итерация %d: Total откатился с %d до %d (вытеснение сбросило счётчик)", i, prev, got)
		}
		prev = got
	}
}

// ── A-T3: индикатор свежих меток «➤» (HasFresh в шапке) ──────────────────

// TestPushHeaderHasFreshFalseWhenAtTail — метка на хвосте (tail-follow): шапка
// уходит с HasFresh=false — индикатора «➤» нет.
func TestPushHeaderHasFreshFalseWhenAtTail(t *testing.T) {
	d := New(&mockOverlay{}, &mockEngine{}, &mockLogger{}, DefaultConfig())
	overlay := d.overlay.(*mockOverlay)

	const n = 3
	for i := 0; i < n; i++ {
		d.timeline.Add(fmt.Sprintf("Q%d?", i))
	}
	d.pushActiveHeader()

	msg, ok := lastCandidates(overlay)
	if !ok || len(msg.Answers) == 0 {
		t.Fatalf("шапка на хвосте не найдена: %+v", msg)
	}
	if msg.Answers[0].HasFresh {
		t.Errorf("HasFresh на хвосте = true, ожидалось false (индикатор гаснет)")
	}
	if msg.Answers[0].QuestionIdx != n-1 {
		t.Errorf("QuestionIdx = %d, ожидалось %d (активная = хвост)", msg.Answers[0].QuestionIdx, n-1)
	}
}

// TestPushHeaderHasFreshTrueOffTail — регрессия D4: окно кольца ограничено 150,
// TotalAsked=156 (абсолютный), активная НЕ на хвосте, а свежая метка правее неё
// существует → шапка несёт HasFresh=true, хотя QuestionIdx (153) заметно меньше
// Total (156). Старая UI-логика («➤» при QuestionIdx < Total) ошибалась бы в
// обе стороны, потому что сравнивала абсолютный idx с оконным total.
func TestPushHeaderHasFreshTrueOffTail(t *testing.T) {
	d := New(&mockOverlay{}, &mockEngine{}, &mockLogger{}, DefaultConfig())
	overlay := d.overlay.(*mockOverlay)

	// 155 добавлений: голова окна вытеснена, окно хранит 150 меток (idx 5..154).
	const reached = questionTimelineCapacity + 5
	for i := 0; i < reached; i++ {
		d.timeline.Add(fmt.Sprintf("Q%d?", i))
	}
	// Уходим с хвоста (tail-follow выключается) — теперь новая метка станет fresh.
	if !d.timeline.Prev() {
		t.Fatal("Prev() с хвоста: false, ожидалось true")
	}
	// Вопрос приходит при просмотре истории: активная не двигается, свежая метка
	// запоминается (freshAt = последняя) — это и есть индикатор «➤».
	if idx := d.timeline.Add("Qnew?"); idx != reached {
		t.Fatalf("Add вне хвоста вернул idx=%d, ожидалось %d", idx, reached)
	}

	total := d.timeline.TotalAsked()
	if total != reached+1 {
		t.Fatalf("TotalAsked() = %d, ожидалось %d", total, reached+1)
	}
	if snap := snapshotOf(d); len(snap) != questionTimelineCapacity {
		t.Fatalf("len(Snapshot()) = %d, ожидалось %d (окно кольца)", len(snap), questionTimelineCapacity)
	}
	activeAbs, _, ok := d.timeline.Active()
	if !ok || activeAbs != reached-2 {
		t.Fatalf("активная метка idx=%d ok=%v, ожидалось %d", activeAbs, ok, reached-2)
	}

	d.pushActiveHeader()

	msg, ok := lastCandidates(overlay)
	if !ok || len(msg.Answers) == 0 {
		t.Fatalf("шапка не найдена: %+v", msg)
	}
	a := msg.Answers[0]
	if !a.HasFresh {
		t.Errorf("HasFresh = false при active=%d total=%d, ожидалось true (в окне есть свежая метка правее)",
			a.QuestionIdx, a.Total)
	}
	if a.QuestionIdx != reached-2 || a.Total != total {
		t.Errorf("шапка = {idx:%d total:%d}, ожидалось {idx:%d total:%d}",
			a.QuestionIdx, a.Total, reached-2, total)
	}
}

// TestPushHeaderHasFreshClearedOnReturnToTail — возврат на свежую метку (Next)
// сбрасывает индикатор: HasFresh=false на хвосте, снова true после уходa назад.
func TestPushHeaderHasFreshClearedOnReturnToTail(t *testing.T) {
	d := New(&mockOverlay{}, &mockEngine{}, &mockLogger{}, DefaultConfig())
	overlay := d.overlay.(*mockOverlay)

	for i := 0; i < 3; i++ {
		d.timeline.Add(fmt.Sprintf("Q%d?", i))
	}
	// Уходим с хвоста — новая метка (потом) станет fresh, хвост помечен свежим.
	if !d.timeline.Prev() {
		t.Fatal("Prev(): false, ожидалось true")
	}
	d.timeline.Add("Q4?") // окно не двигается на tail-follow при уходе — freshAt = Q4

	d.pushActiveHeader()
	if a := answers0(t, overlay); !a.HasFresh {
		t.Errorf("после Add вне хвоста: HasFresh = false, ожидалось true")
	}

	// Next: active = Q3? (не свежая) → индикатор ещё горит.
	if !d.timeline.Next() {
		t.Fatal("Next() на Q3?: false, ожидалось true")
	}
	d.pushActiveHeader()
	if a := answers0(t, overlay); !a.HasFresh {
		t.Errorf("на промежуточной метке Q3?: HasFresh = false, ожидалось true")
	}

	// Next: active достиг свежей метки Q4? → clearFreshOn гасит индикатор.
	if !d.timeline.Next() {
		t.Fatal("Next() на Q4?: false, ожидалось true")
	}
	d.pushActiveHeader()
	a := answers0(t, overlay)
	if a.HasFresh {
		t.Errorf("после возврата на свежую метку: HasFresh = true, ожидалось false (индикатор гаснет)")
	}
	if _, fresh := d.timeline.HasFresh(); fresh {
		t.Errorf("таймлайн: HasFresh() = true после перехода на freshAt, ожидалось false")
	}
}

// answers0 возвращает первый Answer последней шапки overlay.
func answers0(t *testing.T, overlay *mockOverlay) ui.Answer {
	t.Helper()
	msg, ok := lastCandidates(overlay)
	if !ok || len(msg.Answers) == 0 {
		t.Fatalf("шапка не найдена: %+v", msg)
	}
	return msg.Answers[0]
}

// ── Совместимость: GenerateAnswers (shutdown/тесты) ──────────────────────

func TestGenerateAnswersAddsLabelAndAnswers(t *testing.T) {
	engine := &mockEngine{answers: []string{"EN: A | RU: A"}}
	overlay := &mockOverlay{}
	cfg := DefaultConfig()
	d := New(overlay, engine, &mockLogger{}, cfg)

	d.GenerateAnswers("What is Redis?")

	snap := snapshotOf(d)
	if len(snap) != 1 {
		t.Fatalf("GenerateAnswers не создал метку: %d", len(snap))
	}
	if snap[0].State != QReady {
		t.Errorf("состояние метки = %v, ожидалось QReady", snap[0].State)
	}
	if _, ok := lastCandidates(overlay); !ok {
		t.Error("GenerateAnswers не отправил AnswerCandidates в UI")
	}
}

// lastFinalOf читает кандидат уточнения под мьютексом (тестовый доступ).
func lastFinalOf(d *Dispatcher) string {
	d.lastFinalMu.Lock()
	defer d.lastFinalMu.Unlock()
	return d.lastFinal
}

// waitLastFinal ожидает, пока lastFinal станет равным want (иначе — таймаут).
func waitLastFinal(t *testing.T, d *Dispatcher, want string) {
	t.Helper()
	waitFor(t, func() bool { return lastFinalOf(d) == want }, 2*time.Second)
}

// ── Б-T2: ручные уточнения (Shift+F2 / Shift+F3) ─────────────────────────

// TestLastFinalRecordsInterviewerEndOfTurn — каждая финальная (EndOfTurn) фраза
// интервьюера запоминается как кандидат уточнения (lastFinal), включая
// не-вопрос: план требует «каждая EndOfTurn-фраза — кандидат уточнения».
func TestLastFinalRecordsInterviewerEndOfTurn(t *testing.T) {
	auto := false
	cfg := DefaultConfig()
	cfg.AutoAnswer = &auto

	engine := &mockEngine{answers: []string{"EN: A | RU: A"}}
	d, _, textStream, cancel := startDispatcherWithOverlay(t, engine, cfg)
	defer cancel()

	textStream <- question("Tell me about Project X.") // вопрос
	waitFor(t, func() bool { return len(snapshotOf(d)) == 1 }, 2*time.Second)
	waitLastFinal(t, d, "Tell me about Project X.")

	textStream <- question("At scale, with 10k rps.") // не-вопрос
	waitLastFinal(t, d, "At scale, with 10k rps.")
	if got := len(snapshotOf(d)); got != 1 {
		t.Errorf("не-вопрос создал метку: меток %d, ожидалось 1", got)
	}
}

// TestClarifyEmptyTimelineNoOp — Shift+F2 на пустом таймлайне (или без
// зафиксированной финальной фразы) — no-op без паники.
func TestClarifyEmptyTimelineNoOp(t *testing.T) {
	engine := &mockEngine{answers: []string{"EN: A | RU: A"}}
	d, overlay, _, cancel := startDispatcherWithOverlay(t, engine, DefaultConfig())
	defer cancel()

	d.HandleClarify()
	d.HandleUnclarify()
	// Ждём полного покоя воркера: ни одного действия не должно примениться.
	waitIdle(t, d)

	if snap := snapshotOf(d); len(snap) != 0 {
		t.Errorf("пустой таймлайн изменён: %+v", snap)
	}
	if _, ok := lastCandidates(overlay); ok {
		t.Error("на пустом таймлайне ушла шапка в UI")
	}
	if calls := engine.Calls(); len(calls) != 0 {
		t.Errorf("на пустом таймлайне вызовов LLM %d, ожидалось 0", len(calls))
	}
}

// TestClarifyAddsToActiveLabel — обычная (не-дубль) фраза → уточнение активной
// метки, UI обновлён, генерация не запускается.
func TestClarifyAddsToActiveLabel(t *testing.T) {
	auto := false
	cfg := DefaultConfig()
	cfg.AutoAnswer = &auto

	engine := &mockEngine{answers: []string{"EN: A | RU: A"}}
	d, overlay, textStream, cancel := startDispatcherWithOverlay(t, engine, cfg)
	defer cancel()

	textStream <- question("How do you scale?")
	waitFor(t, func() bool { return len(snapshotOf(d)) == 1 }, 2*time.Second)
	textStream <- question("I mean horizontally.")
	// Даём route записать вторую (не-вопрос) реплику в lastFinal.
	waitLastFinal(t, d, "I mean horizontally.")

	d.HandleClarify()
	waitFor(t, func() bool { return len(snapshotOf(d)[0].Clarifications) == 1 }, 2*time.Second)

	snap := snapshotOf(d)
	if len(snap) != 1 {
		t.Fatalf("меток после уточнения %d, ожидалось 1", len(snap))
	}
	if got := snap[0].Clarifications; got[0] != "I mean horizontally." {
		t.Errorf("Clarifications = %v, ожидалось [I mean horizontally.]", got)
	}
	msg, ok := lastCandidates(overlay)
	if !ok || len(msg.Answers) == 0 {
		t.Fatalf("шапка не найдена: %+v", msg)
	}
	if got := msg.Answers[0].Clarifications; len(got) != 1 || got[0] != "I mean horizontally." {
		t.Errorf("ui.Answer.Clarifications = %v, ожидалось [I mean horizontally.]", got)
	}
	if calls := engine.Calls(); len(calls) != 0 {
		t.Errorf("уточнение запустило генерацию: вызовов %d", len(calls))
	}
}

// TestClarifyWithoutNewFinalIsNoOp — lastFinal потребляется: повторный Shift+F2
// без новой финальной фразы ничего не добавляет.
func TestClarifyWithoutNewFinalIsNoOp(t *testing.T) {
	auto := false
	cfg := DefaultConfig()
	cfg.AutoAnswer = &auto

	engine := &mockEngine{answers: []string{"EN: A | RU: A"}}
	d, _, textStream, cancel := startDispatcherWithOverlay(t, engine, cfg)
	defer cancel()

	textStream <- question("How do you scale?")
	waitFor(t, func() bool { return len(snapshotOf(d)) == 1 }, 2*time.Second)
	textStream <- question("I mean horizontally.")
	waitLastFinal(t, d, "I mean horizontally.")

	d.HandleClarify()
	waitFor(t, func() bool { return len(snapshotOf(d)[0].Clarifications) == 1 }, 2*time.Second)

	// Повторный Shift+F2 — lastFinal уже потреблён → no-op.
	d.HandleClarify()
	d.HandleClarify()
	waitIdle(t, d)

	if got := snapshotOf(d)[0].Clarifications; len(got) != 1 {
		t.Errorf("повторный Shift+F2 добавил уточнения: %v", got)
	}
}

// TestUnclarifyRemovesLast — Shift+F3 убирает последнее уточнение активной
// метки; на пустом списке — no-op.
func TestUnclarifyRemovesLast(t *testing.T) {
	auto := false
	cfg := DefaultConfig()
	cfg.AutoAnswer = &auto

	engine := &mockEngine{answers: []string{"EN: A | RU: A"}}
	d, overlay, textStream, cancel := startDispatcherWithOverlay(t, engine, cfg)
	defer cancel()

	textStream <- question("How do you scale?")
	waitFor(t, func() bool { return len(snapshotOf(d)) == 1 }, 2*time.Second)

	for i, tx := range []string{"первое", "второе"} {
		textStream <- question(tx)
		waitLastFinal(t, d, tx)
		d.HandleClarify()
		// Потребление lastFinal происходит в worker'е асинхронно: ждём, пока
		// уточнение реально осядет, иначе следующая фраза перезапишет кандидат.
		want := i + 1
		waitFor(t, func() bool { return len(snapshotOf(d)[0].Clarifications) == want }, 2*time.Second)
	}
	waitFor(t, func() bool { return len(snapshotOf(d)[0].Clarifications) == 2 }, 2*time.Second)

	d.HandleUnclarify()
	waitFor(t, func() bool { return len(snapshotOf(d)[0].Clarifications) == 1 }, 2*time.Second)
	if got := snapshotOf(d)[0].Clarifications; got[0] != "первое" {
		t.Errorf("после Shift+F3 Clarifications = %v, ожидалось [первое]", got)
	}
	msg, ok := lastCandidates(overlay)
	if !ok || len(msg.Answers) == 0 {
		t.Fatalf("шапка не найдена: %+v", msg)
	}
	if got := msg.Answers[0].Clarifications; len(got) != 1 || got[0] != "первое" {
		t.Errorf("ui.Answer.Clarifications = %v, ожидалось [первое]", got)
	}

	// Опустошение и повторный Shift+F3 — no-op без паники.
	d.HandleUnclarify()
	waitFor(t, func() bool { return len(snapshotOf(d)[0].Clarifications) == 0 }, 2*time.Second)
	d.HandleUnclarify()
	waitIdle(t, d)
	if got := snapshotOf(d)[0].Clarifications; len(got) != 0 {
		t.Errorf("Shift+F3 на пустом списке изменил состояние: %v", got)
	}
}

// TestF1RegenerationCarriesClarifications — F1-перегенерация старой метки несёт
// её уточнения в AnswerRequest.Clarifications.
func TestF1RegenerationCarriesClarifications(t *testing.T) {
	auto := false
	cfg := DefaultConfig()
	cfg.AutoAnswer = &auto

	engine := &mockEngine{answers: []string{"EN: A | RU: A"}}
	d, _, textStream, cancel := startDispatcherWithOverlay(t, engine, cfg)
	defer cancel()

	// Q1, затем не-вопрос реплика → уточнение активной метки Q1.
	textStream <- question("Q1?")
	waitFor(t, func() bool { return len(snapshotOf(d)) == 1 }, 2*time.Second)
	textStream <- question("I mean at scale.")
	waitLastFinal(t, d, "I mean at scale.")
	d.HandleClarify()
	waitFor(t, func() bool { return len(snapshotOf(d)[0].Clarifications) == 1 }, 2*time.Second)

	// Q2 приходит — вторая метка.
	textStream <- question("Q2?")
	waitFor(t, func() bool { return len(snapshotOf(d)) == 2 }, 2*time.Second)

	// Уходим на Q1 (старая метка) и генерируем по F1.
	d.HandlePrev()
	waitFor(t, func() bool { idx, _, _ := d.timeline.Active(); return idx == 0 }, 2*time.Second)

	d.HandleDemand()
	waitFor(t, func() bool { return len(engine.Calls()) >= 1 }, 2*time.Second)

	reqs := engine.Reqs()
	last := reqs[len(reqs)-1]
	if last.Question != "Q1?" {
		t.Fatalf("последний запрос про %q, ожидался Q1?", last.Question)
	}
	if len(last.Clarifications) != 1 || last.Clarifications[0] != "I mean at scale." {
		t.Errorf("AnswerRequest.Clarifications = %v, ожидалось [I mean at scale.]", last.Clarifications)
	}
}

// TestPushHeaderFillsClarifications — pushHeader заполняет ui.Answer.Clarifications
// активной метки (копией стека таймлайна), как QuestionText.
func TestPushHeaderFillsClarifications(t *testing.T) {
	d := New(&mockOverlay{}, &mockEngine{}, &mockLogger{}, DefaultConfig())
	overlay := d.overlay.(*mockOverlay)

	d.timeline.Add("Q1?")
	d.timeline.AddClarification(0, "первое")
	d.timeline.AddClarification(0, "второе")

	d.pushActiveHeader()

	a := answers0(t, overlay)
	if len(a.Clarifications) != 2 || a.Clarifications[0] != "первое" || a.Clarifications[1] != "второе" {
		t.Fatalf("ui.Answer.Clarifications = %v, ожидалось [первое второе]", a.Clarifications)
	}
	// Копия: мутация не просачивается в таймлайн.
	a.Clarifications[0] = "MUTATED"
	if got := d.timeline.Clarifications(0); got[0] != "первое" {
		t.Errorf("мутация Answer.Clarifications просочилась в таймлайн: %q", got[0])
	}
}

// TestAutoAnswerRequestCarriesClarifications — авто-генерация несёт уточнения
// метки (метка активна на момент старта): проверяем через pushHeader-путь и
// AnswerRequest авто-задания после добавления уточнения к хвосту.
func TestAutoAnswerRequestCarriesClarifications(t *testing.T) {
	auto := false
	cfg := DefaultConfig()
	cfg.AutoAnswer = &auto

	engine := &mockEngine{answers: []string{"EN: A | RU: A"}}
	d, _, textStream, cancel := startDispatcherWithOverlay(t, engine, cfg)
	defer cancel()

	textStream <- question("Q1?")
	waitFor(t, func() bool { return len(snapshotOf(d)) == 1 }, 2*time.Second)
	textStream <- question("Имею в виду масштаб.")
	waitLastFinal(t, d, "Имею в виду масштаб.")

	d.HandleClarify()
	waitFor(t, func() bool { return len(snapshotOf(d)[0].Clarifications) == 1 }, 2*time.Second)

	d.HandleDemand()
	waitFor(t, func() bool { return len(engine.Calls()) >= 1 }, 2*time.Second)

	reqs := engine.Reqs()
	last := reqs[len(reqs)-1]
	if len(last.Clarifications) != 1 || last.Clarifications[0] != "Имею в виду масштаб." {
		t.Errorf("AnswerRequest.Clarifications = %v, ожидалось [Имею в виду масштаб.]", last.Clarifications)
	}
}
