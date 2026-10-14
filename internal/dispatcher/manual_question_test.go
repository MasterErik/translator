package dispatcher

import (
	"testing"
	"time"

	"github.com/mastererik/translator/internal/translator"
)

// Регрессионный контракт: реплика собеседника (ChannelID="speaker",
// Event=EndOfTurn), являющаяся вопросом, запускает генерацию ответа; обычная
// реплика — не запускает.
//
// Повод: smoke-сессия debug-ввода показала «ввели строку — нет ответа». Тест
// фиксирует решение явно — на уровне таймлайна (метка создана/не создана) и на
// уровне вызова LLM (генерация запущена/не запущена).
//
// AutoAnswer задаётся ЯВНО в конфиге (указатель *bool): тест не зависит от
// значения env-флага CONVERSATION_AUTO_ANSWER / config.yaml, которые использует
// реальная сессия (config.yaml по умолчанию оставляет auto_answer закомментированным).
//
// ВАЖНО о синхронизации: waitIdle() проверяет !busy && len(answerCh)==0 — это
// тривиально истинно ДО того, как route() обработает событие, поэтому как
// «событие обработано» он не годится. Ждём именно наблюдаемого эффекта (метка
// таймлайна / overlay) через waitFor — как это делает существующий harness.
func TestManualQuestionDetection(t *testing.T) {
	// AutoAnswer=false: реплика-вопрос обязана создать метку таймлайна, но НЕ
	// запускать генерацию сама (ответ — по F1). Не-вопрос не создаёт ничего.
	cfg := DefaultConfig()
	auto := false
	cfg.AutoAnswer = &auto

	tests := []struct {
		name         string
		text         string
		wantQuestion bool
	}{
		{
			name:         "вопрос создаёт метку",
			text:         "What is your experience with Go?",
			wantQuestion: true,
		},
		{
			name:         "обычная реплика не создаёт метку",
			text:         "just a statement",
			wantQuestion: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Fixture обязан распознаваться той же эвристикой, что и в route().
			if got := translator.IsQuestion(tc.text); got != tc.wantQuestion {
				t.Fatalf("fixture испорчен: IsQuestion(%q) = %v, want %v", tc.text, got, tc.wantQuestion)
			}

			// Harness переиспользован (DRY): startDispatcherWithOverlay + mockEngine.
			engine := &mockEngine{answers: []string{"EN: five years | RU: пять лет"}}
			d, overlay, textStream, cancel := startDispatcherWithOverlay(t, engine, cfg)
			defer cancel()

			textStream <- question(tc.text)

			if tc.wantQuestion {
				// Ждём наблюдаемый эффект обработки: метка появилась в таймлайне.
				waitFor(t, func() bool { return len(d.timeline.Snapshot()) == 1 }, 2*time.Second)

				snap := d.timeline.Snapshot()
				if snap[0].Question != tc.text {
					t.Errorf("текст метки = %q, want %q", snap[0].Question, tc.text)
				}
				if snap[0].State != QIdle {
					t.Errorf("состояние метки = %v, want QIdle (AutoAnswer=false — генерация только по F1)", snap[0].State)
				}
				if d.timeline.TotalAsked() != 1 {
					t.Errorf("TotalAsked = %d, want 1", d.timeline.TotalAsked())
				}
				// Метка ушла в overlay шапкой (пользователь видит «F1 — ответить»).
				waitFor(t, func() bool {
					_, ok := lastCandidates(overlay)
					return ok
				}, 2*time.Second)
			} else {
				// Не-вопрос: ждём, что реплика дошла до overlay, и что метки НЕТ.
				// waitMessages(2) = History + Interim — оба пуша идут в default-ветке
				// route() строго ПОСЛЕ проверки IsQuestion, поэтому к этому моменту
				// решение по метке уже принято (гонки нет).
				waitMessages(t, overlay, 2)

				if snap := d.timeline.Snapshot(); len(snap) != 0 {
					t.Errorf("меток таймлайна = %d, want 0 (обычная реплика не создаёт метку)", len(snap))
				}
				if _, ok := lastCandidates(overlay); ok {
					t.Error("AnswerCandidates найдены: обычная реплика не должна порождать шапку/метку")
				}
				if d.timeline.TotalAsked() != 0 {
					t.Errorf("TotalAsked = %d, want 0", d.timeline.TotalAsked())
				}
			}

			// Ждём, пока worker освободится, чтобы len(engine.Calls()) был стабилен.
			waitIdle(t, d)

			// При AutoAnswer=false генерации не должно быть ни в одном кейсе.
			if calls := engine.Calls(); len(calls) != 0 {
				t.Fatalf("вызовов GenerateAnswers = %d, want 0 (AutoAnswer=false): %v", len(calls), calls)
			}
		})
	}
}

// TestManualQuestionAutoAnswerEnabled — тот же контракт «вопрос → генерация»
// при ЯВНО включённом AutoAnswer: реплика-вопрос немедленно запускает
// генерацию ответа (кейс A), обычная реплика — не запускает (кейс B).
// Контраст к TestManualQuestionDetection (AutoAnswer=false, метка без генерации)
// и страховка от зависимости результата от env-флага.
func TestManualQuestionAutoAnswerEnabled(t *testing.T) {
	cfg := DefaultConfig()
	auto := true
	cfg.AutoAnswer = &auto

	t.Run("вопрос запускает генерацию", func(t *testing.T) {
		const text = "What is your experience with Go?"

		engine := &mockEngine{answers: []string{"EN: five years | RU: пять лет"}}
		d, overlay, textStream, cancel := startDispatcherWithOverlay(t, engine, cfg)
		defer cancel()

		textStream <- question(text)

		// Ждём наблюдаемый результат авто-генерации: метка дошла до QReady.
		waitFor(t, func() bool {
			snap := d.timeline.Snapshot()
			return len(snap) == 1 && snap[0].State == QReady
		}, 2*time.Second)
		waitIdle(t, d)

		calls := engine.Calls()
		if len(calls) != 1 {
			t.Fatalf("вызовов GenerateAnswers = %d, want 1 (вопрос обязан запустить генерацию)", len(calls))
		}
		if calls[0] != text {
			t.Errorf("вопрос в LLM = %q, want %q", calls[0], text)
		}

		snap := d.timeline.Snapshot()
		if len(snap) != 1 || snap[0].Question != text {
			t.Fatalf("метка таймлайна = %+v, want 1 метка с текстом вопроса", snap)
		}
		// Метка ушла в overlay шапкой с готовым ответом.
		waitFor(t, func() bool {
			_, ok := lastCandidates(overlay)
			return ok
		}, 2*time.Second)
	})

	t.Run("обычная реплика не запускает генерацию", func(t *testing.T) {
		engine := &mockEngine{answers: []string{"EN: five years | RU: пять лет"}}
		d, overlay, textStream, cancel := startDispatcherWithOverlay(t, engine, cfg)
		defer cancel()

		textStream <- question("just a statement")
		// History + Interim отправляются ПОСЛЕ проверки IsQuestion — к этому
		// моменту решение уже принято.
		waitMessages(t, overlay, 2)
		waitIdle(t, d)

		if calls := engine.Calls(); len(calls) != 0 {
			t.Fatalf("вызовов GenerateAnswers = %d, want 0 (обычная реплика не запускает генерацию): %v", len(calls), calls)
		}
		if snap := d.timeline.Snapshot(); len(snap) != 0 {
			t.Fatalf("меток таймлайна = %d, want 0", len(snap))
		}
		if _, ok := lastCandidates(overlay); ok {
			t.Error("AnswerCandidates найдены: обычная реплика не должна порождать шапку/метку")
		}
	})
}
