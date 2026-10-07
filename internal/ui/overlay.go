package ui

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"sync"
	"time"

	"gioui.org/app"
	"gioui.org/io/event"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"github.com/mastererik/translator/internal/logger"
)

// Overlay — прозрачное окно с четырьмя зонами.
//
// Состояние зон разделено по назначению (Task 2.2, P4): каждое поле хранит
// сообщения ровно одного типа, AddMessage маршрутизирует по msg.Type, а render
// читает поля напрямую (без фильтрации единого слайса). Все поля защищены mu.
type Overlay struct {
	cfg OverlayConfig
	mu  sync.RWMutex

	// interimMsg — текущая речь (зона 1): хранится только последнее Interim.
	interimMsg UIMessage
	// translations — переводы (зона 2): append-only.
	translations []UIMessage
	// history — оригиналы речи (зона 4): append-only.
	history []UIMessage
	// answersHistory — история ВСЕХ AnswerCandidates (зона 3): рендерится
	// последняя, остальные хранятся для будущей навигации по вопросам.
	answersHistory []UIMessage
	// errorMsg — последняя ошибка генерации (зона 3): хранится только последняя.
	errorMsg UIMessage

	shutdown chan struct{}

	invalidate func()

	// Персистентные списки — хранят позицию скролла между кадрами.
	interimList       layout.List
	translationList   layout.List
	transcriptionList layout.List
	answersList       layout.List

	// historyVisible — видимость зоны TranscriptionHistory (F4).
	// Начальное состояние — скрыта (зона 4 и её separator отсутствуют до F4). Доступ под mu.
	historyVisible bool

	sessLog logger.SessionLogger
}

// FrameMetrics — результат рендера кадра. Возвращается из render() вместо
// глобального состояния (Task 3.1/3.2): метрики принадлежат КОНКРЕТНОМУ
// оверлею и конкретному кадру, поэтому не текут между оверлеями и не требуют
// глобальных переменных. Поля AtEnd заполняются в render() после layout.List
// каждого скролла: true — список доскроллен до конца (Position.BeforeEnd=false).
//
// Вызывающий Run() значение игнорирует (метрики нужны только тестам);
// тесты читают возвращённое значение напрямую.
type FrameMetrics struct {
	SeparatorCount int
	InterimAtEnd   bool
	AnswersAtEnd   bool
	// TranslationsAtEnd / TranscriptionAtEnd — зоны 2/4; осмысленны только
	// при наличии данных (пустой список → false).
	TranslationsAtEnd  bool
	TranscriptionAtEnd bool
}

func NewOverlay(cfg OverlayConfig, sessLog logger.SessionLogger) *Overlay {
	if cfg.Width <= 0 {
		cfg.Width = 1200
	}
	if cfg.Height <= 0 {
		cfg.Height = 650
	}
	if cfg.FontSize <= 0 {
		cfg.FontSize = 18
	}
	return &Overlay{
		cfg:               cfg,
		interimMsg:        UIMessage{},
		translations:      make([]UIMessage, 0),
		history:           make([]UIMessage, 0),
		answersHistory:    make([]UIMessage, 0),
		shutdown:          make(chan struct{}),
		interimList:       layout.List{Axis: layout.Vertical},
		translationList:   layout.List{Axis: layout.Vertical},
		transcriptionList: layout.List{Axis: layout.Vertical},
		answersList:       layout.List{Axis: layout.Vertical},
		historyVisible:    false,
		sessLog:           sessLog,
	}
}

// AddMessage маршрутизирует сообщение сразу в поле своей зоны (Task 2.2, P4):
// Interim/Error заменяются, Translation/History/AnswerCandidates накапливаются.
// Все поля защищены mu.
func (o *Overlay) AddMessage(msg UIMessage) {
	o.mu.Lock()
	defer o.mu.Unlock()

	switch msg.Type {
	case Interim:
		// Только последняя текущая речь.
		o.interimMsg = msg

	case Translation:
		// Переводы финализированы: только append (как History).
		o.translations = append(o.translations, msg)

	case History:
		// История оригиналов: append-only.
		o.history = append(o.history, msg)

	case AnswerCandidates:
		// История всех подсказок: append (рендерится последняя).
		o.answersHistory = append(o.answersHistory, msg)

	case Error:
		// Последняя ошибка генерации (замена).
		o.errorMsg = msg

	default:
		o.history = append(o.history, msg)
	}

	o.invalidateIf()
}

func (o *Overlay) invalidateIf() {
	if o.invalidate != nil {
		o.invalidate()
	}
}

// GetMessages — обратная совместимость (тесты, OverlayUI-интерфейс): возвращает
// копию конкатенации всех полей в стабильном порядке: interim, translations,
// history, answersHistory, error (последний — только при заданном типе).
func (o *Overlay) GetMessages() []UIMessage {
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := make([]UIMessage, 0, len(o.translations)+len(o.history)+len(o.answersHistory)+2)
	if o.interimMsg.Type != "" {
		out = append(out, o.interimMsg)
	}
	out = append(out, o.translations...)
	out = append(out, o.history...)
	out = append(out, o.answersHistory...)
	if o.errorMsg.Type != "" {
		out = append(out, o.errorMsg)
	}
	return out
}

// ── GioUI Window ──

func (o *Overlay) Run(ctx context.Context) error {
	var w app.Window
	w.Option(
		app.Title(""),
		app.Size(unit.Dp(o.cfg.Width), unit.Dp(o.cfg.Height)),
		app.TopMost(true),
	)
	o.invalidate = func() { w.Invalidate() }

	th := material.NewTheme()
	var ops op.Ops

	go o.applyWindowStyles()
	defer close(o.shutdown)

	o.sessLog.LogDebug(fmt.Sprintf("UI-оверлей запущен: width=%d, height=%d", o.cfg.Width, o.cfg.Height))

	for {
		select {
		case <-ctx.Done():
			w.Perform(system.ActionClose)
			return waitForDestroy(ctx, w.Event, destroyEventTimeout, o.sessLog)
		default:
		}

		evt := w.Event()
		switch e := evt.(type) {
		case app.DestroyEvent:
			return e.Err
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			_ = o.render(gtx, th) // метрики кадра нужны только тестам
			e.Frame(gtx.Ops)
		}
	}
}

// destroyEventTimeout — сколько ждать app.DestroyEvent после ActionClose,
// прежде чем завершиться принудительно (P7: не висеть вечно при shutdown).
const destroyEventTimeout = 2 * time.Second

// waitForDestroy ждёт app.DestroyEvent после запроса закрытия окна, но не дольше
// timeout. Возвращает ctx.Err() если событие получено, nil — при истечении
// таймаута (окно не ответило, выходим принудительно). Вынесено из Run, чтобы
// тестировать без реального окна: nextEvent — блокирующий источник событий
// (для реального окна — w.Event()).
//
// nextEvent опрашивается одной горутиной-насосом: она завершается, когда
// возвращается nextEvent, либо когда waitForDestroy уходит по таймауту
// (закрытие pumpDone) — при w.Event() Gio разблокирует её на следующем событии,
// отдельного вечного ожидания не остаётся.
func waitForDestroy(ctx context.Context, nextEvent func() event.Event, timeout time.Duration, sessLog logger.SessionLogger) error {
	pumpDone := make(chan struct{})
	events := make(chan event.Event, 1)
	go func() {
		defer close(events)
		for {
			evt := nextEvent()
			select {
			case events <- evt:
			case <-pumpDone:
				return
			}
		}
	}()
	defer close(pumpDone)

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			sessLog.LogDebug(fmt.Sprintf("UI-оверлей: DestroyEvent не получен за %v, принудительное завершение", timeout))
			return nil
		case evt, ok := <-events:
			if !ok {
				// Источник событий закрылся без DestroyEvent — выходим принудительно.
				return nil
			}
			if _, ok := evt.(app.DestroyEvent); ok {
				return ctx.Err()
			}
		}
	}
}

func (o *Overlay) WaitShutdown() { <-o.shutdown }

// ToggleTranscriptionHistory переключает видимость зоны TranscriptionHistory (F4).
// Потокобезопасно: вызывается из горутины hotkeys.
func (o *Overlay) ToggleTranscriptionHistory() {
	o.mu.Lock()
	o.historyVisible = !o.historyVisible
	o.mu.Unlock()
	o.invalidateIf()
}

// TranscriptionVisible — видима ли зона TranscriptionHistory. Для тестов.
func (o *Overlay) TranscriptionVisible() bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.historyVisible
}

// HistoryVisible — видима ли зона TranscriptionHistory (зона 4). Для тестов.
// Потокобезопасно: чтение под mu.
func (o *Overlay) HistoryVisible() bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.historyVisible
}

func (o *Overlay) applyWindowStyles() {
	// Gio создаёт нативное окно асинхронно — ждём до 3 секунд с экспоненциальной задержкой.
	if !tryApplyStyles(findWindowByPID, 3*time.Second, 50*time.Millisecond) {
		o.sessLog.LogDebug("applyWindowStyles: окно не появилось за 3 секунды")
		return
	}
	if err := setNoActivate(lastFoundHwnd); err != nil {
		o.sessLog.LogDebug(fmt.Sprintf("applyWindowStyles: не удалось применить стили: error=%v", err))
	} else {
		o.sessLog.LogDebug(fmt.Sprintf("applyWindowStyles: NOACTIVATE+TRANSPARENT применены: hwnd=%v", lastFoundHwnd))
	}
}

// lastFoundHwnd — результат findWindowByPID после успешного поиска.
var lastFoundHwnd uintptr

// tryApplyStyles вызывает finder с экспоненциальной задержкой до deadline.
// Возвращает true если окно найдено, сохраняет HWND в lastFoundHwnd.
func tryApplyStyles(finder func() (uintptr, error), deadline time.Duration, initialDelay time.Duration) bool {
	dl := time.Now().Add(deadline)
	delay := initialDelay
	for time.Now().Before(dl) {
		hwnd, err := finder()
		if err == nil {
			lastFoundHwnd = hwnd
			return true
		}
		time.Sleep(delay)
		delay *= 2
		if delay > 500*time.Millisecond {
			delay = 500 * time.Millisecond
		}
	}
	return false
}

// ── Rendering: четыре зоны ──

// render рисует кадр (четыре зоны) и возвращает FrameMetrics — метрики именно
// этого кадра именно этого оверлея (Task 3.1/3.2). Никакого глобального
// состояния: счётчик separator и флаги «список у конца» живут в возвращаемом
// значении. Вызывающий Run() метрики игнорирует.
func (o *Overlay) render(gtx layout.Context, th *material.Theme) FrameMetrics {
	o.mu.RLock()
	interim := o.lastInterim()
	answers, hasAnswers := o.lastAnswers()
	translations := o.translationMessages()
	history := o.historyMessages()
	errorMsg := o.errorMsg
	historyVisible := o.historyVisible
	o.mu.RUnlock()

	// Автоскролл «всегда в конец» (Task 3.1): зоны 2/3/4 скроллятся к концу в
	// каждый кадр с данными, без счётчиков-дельта и needScroll-условий. Список
	// зоны 1 (interimList) всегда прокручивается через ScrollToEnd при непустом
	// тексте. Позиции layout.List общие для зон 2/3/4 (немедленное обновление).
	needScrollInterim := interim.Text != ""
	needScrollTrans := len(translations) > 0
	needScrollHist := len(history) > 0
	needScrollAnswers := len(answers.Answers) > 0

	// separatorCount собирается layout-функцией separator'а в кадре (см. ниже);
	// локальный счётчик не течёт между оверлеями/кадрами (Task 3.2).
	separatorCount := 0
	layoutZoneSeparator := func(gtx layout.Context) layout.Dimensions {
		separatorCount++
		return zoneSeparator(gtx)
	}

	bg := color.NRGBA{R: 0, G: 0, B: 0, A: 180}
	paintBackground(gtx, bg)

	fs := o.cfg.FontSize

	historyHeight := historyVisibleHeightPx(fs)

	// Собираем children один раз: зоны 1–3 постоянны, зона 4 (история)
	// добавляется только при historyVisible — при скрытом состоянии она
	// и её separator отсутствуют в Flex и не занимают место в layout.
	children := []layout.FlexChild{
		// 1. Interim — речь, фиксированная высота interimVisibleLines строк,
		// внутри — вертикальный скролл (длинная фраза видна целиком). Белый.
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			h := emptyZoneHeight(fs) * interimVisibleLines
			maxX := gtx.Constraints.Max.X
			if h > 0 && gtx.Constraints.Max.Y > h {
				gtx.Constraints = layout.Constraints{Max: image.Pt(maxX, h)}
			}
			return layoutInterim(gtx, th, interim, fs, &o.interimList, needScrollInterim)
		}),
		layout.Rigid(layoutZoneSeparator),

		// 2. Translation History — скролл, 10 строк, переводы.
		layout.Flexed(0.45, func(gtx layout.Context) layout.Dimensions {
			return layoutTranslationHistory(gtx, th, translations, fs, &o.translationList, needScrollTrans)
		}),
		layout.Rigid(layoutZoneSeparator),

		// 3. AnswerCandidates — основная зона ответов: всё оставшееся место
		// (при скрытой истории — практически вся высота окна). Ошибка
		// генерации (Type=Error) рендерится в этой же зоне приоритетно.
		layout.Flexed(0.55, func(gtx layout.Context) layout.Dimensions {
			return o.layoutAnswersZone(gtx, th, answers, hasAnswers, errorMsg, fs, needScrollAnswers)
		}),
	}

	// 4. TranscriptionHistory — внизу окна, только при historyVisible (F4).
	// Высота — ровно historyVisibleLines строк текста.
	if historyVisible {
		children = append(children,
			layout.Rigid(layoutZoneSeparator),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				maxX := gtx.Constraints.Max.X
				if historyHeight > 0 && gtx.Constraints.Max.Y > historyHeight {
					gtx.Constraints = layout.Constraints{Max: image.Pt(maxX, historyHeight)}
				}
				return layoutTranscriptionHistory(gtx, th, history, fs, &o.transcriptionList, needScrollHist)
			}),
		)
	}

	layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)

	// Метрики кадра: «список у конца» читаем из Position каждого списка после
	// Layout. Пустой список → false (скроллить нечего).
	return FrameMetrics{
		SeparatorCount:     separatorCount,
		InterimAtEnd:       needScrollInterim && !o.interimList.Position.BeforeEnd,
		TranslationsAtEnd:  needScrollTrans && !o.translationList.Position.BeforeEnd,
		AnswersAtEnd:       needScrollAnswers && !o.answersList.Position.BeforeEnd,
		TranscriptionAtEnd: historyVisible && needScrollHist && !o.transcriptionList.Position.BeforeEnd,
	}
}

// lineSpacingScale — межстрочный интервал относительно дефолта Gio.
// 1.0 = стандартный интервал; LineHeightScale масштабирует и шрифт, поэтому
// трогать только осознанно (крутилка на будущее, дефолт = стандартный Gio).
const lineSpacingScale = 1.0

// interimVisibleLines — высота зоны Interim (пустая) в строках — стартовый каркас.
const interimVisibleLines = 3

// emptyZoneHeight — высота пустой строки для стартового каркаса: простой множитель
// от fs (дефолтное межстрочное продвижение Gio ≈ 1.0em, глиф-бокс ≈ 1.25em).
func emptyZoneHeight(fs int) int { return fs * 5 / 4 }

// emptyZoneDims — размеры пустой зоны: ширина окна, высота ровно height px.
// Стартовый каркас: пустые зоны резервируют высоту, чтобы separator-линии
// были видны до появления текста.
func emptyZoneDims(gtx layout.Context, height int) layout.Dimensions {
	return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, height)}
}

// historyVisibleLines — высота видимой области TranscriptionHistory в строках.
const historyVisibleLines = 4

// historyVisibleHeightPx — высота видимой области TranscriptionHistory в px:
// ровно historyVisibleLines строк текста зоны истории (единый шрифт fs).
func historyVisibleHeightPx(fs int) int { return emptyZoneHeight(fs) * historyVisibleLines }

// lastInterim возвращает текущую речь (зона 1) — последнее Interim.
func (o *Overlay) lastInterim() UIMessage {
	return o.interimMsg
}

// lastAnswers возвращает последние подсказки с непустым списком ответов
// (зона 3). ok=false, если история пуста или в последнем элементе нет ответов.
func (o *Overlay) lastAnswers() (UIMessage, bool) {
	for i := len(o.answersHistory) - 1; i >= 0; i-- {
		if len(o.answersHistory[i].Answers) > 0 {
			return o.answersHistory[i], true
		}
	}
	return UIMessage{}, false
}

// historyMessages возвращает оригиналы речи (зона 4) — прямое чтение поля.
func (o *Overlay) historyMessages() []UIMessage {
	return o.history
}

// translationMessages возвращает все переводы (зона 2) — append-only.
func (o *Overlay) translationMessages() []UIMessage {
	return o.translations
}

// ── Zone renderers ──

// zoneSeparator — разделитель между зонами (3px). Рисует одну линию; счётчик
// separator'ов кадра ведёт замыкание в render (Task 3.2 — без глобала).
func zoneSeparator(gtx layout.Context) layout.Dimensions {
	h := 3
	rect := clip.Rect{Max: image.Pt(gtx.Constraints.Max.X, h)}
	defer rect.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, color.NRGBA{R: 60, G: 60, B: 80, A: 255})
	return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, h)}
}

// layoutInterim — зона 1 (речь): вертикальный скролл. Список персистентный
// (позиция между кадрами). ОДИН элемент — whole-phrase label без MaxLines:
// текст переносится по ширине и не обрезается; List даёт вертикальный скролл,
// когда фраза выше фиксированной высоты зоны (interimVisibleLines строк).
// Автоскролл к концу — через ScrollToEnd (последняя строка у нижней границы).
func layoutInterim(gtx layout.Context, th *material.Theme, msg UIMessage, fs int, list *layout.List, needScroll bool) layout.Dimensions {
	if msg.Text == "" {
		return emptyZoneDims(gtx, emptyZoneHeight(fs)*interimVisibleLines)
	}
	list.Axis = layout.Vertical
	if needScroll {
		autoScrollBottom(list)
	}
	return list.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
		l := overlayLabel(th, unit.Sp(fs), msg.Text)
		l.Color = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
		l.Alignment = text.Start
		return l.Layout(gtx)
	})
}

// answerLine — одна строка в скролле подсказок: EN или RU.
type answerLine struct {
	text string
	isRU bool
}

// layoutErrorColor — цвет текста ошибки генерации (зона 3). Мягкий красный,
// отличимый от белого (Interim/подсказки) и зелёного (перевод подсказки).
var layoutErrorColor = color.NRGBA{R: 255, G: 96, B: 96, A: 255}

// layoutAnswers — подсказки: Source и Target на отдельных строках, с вертикальным скроллом.
// Шрифт единый — fs (Task 3.0: убран fs-2/floor). needScroll — автоскролл к концу (Task 3.1).
func layoutAnswers(gtx layout.Context, th *material.Theme, msg UIMessage, fs int, list *layout.List, needScroll bool) layout.Dimensions {
	// Собираем плоский список: Source (белый), Target (зелёный).
	items := make([]answerLine, 0, len(msg.Answers)*2)
	for _, ans := range msg.Answers {
		items = append(items, answerLine{text: ans.Source, isRU: false})
		if ans.Target != "" {
			items = append(items, answerLine{text: ans.Target, isRU: true})
		}
	}

	list.Axis = layout.Vertical
	if needScroll && len(items) > 0 {
		autoScrollBottom(list)
	}
	return list.Layout(gtx, len(items), func(gtx layout.Context, idx int) layout.Dimensions {
		line := items[idx]
		l := overlayLabel(th, unit.Sp(fs), line.text)
		if line.isRU {
			l.Color = color.NRGBA{R: 144, G: 238, B: 144, A: 255}
		} else {
			l.Color = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
		}
		l.Alignment = text.Start
		return l.Layout(gtx)
	})
}

// layoutTranslationHistory — скролл переводов из Translation-сообщений.
// Шрифт единый — fs (Task 3.0).
func layoutTranslationHistory(gtx layout.Context, th *material.Theme, messages []UIMessage, fs int, list *layout.List, needScroll bool) layout.Dimensions {
	if len(messages) == 0 {
		return emptyZoneDims(gtx, gtx.Constraints.Max.Y)
	}

	list.Axis = layout.Vertical
	if needScroll && len(messages) > 0 {
		autoScrollBottom(list)
	}
	return list.Layout(gtx, len(messages), func(gtx layout.Context, i int) layout.Dimensions {
		l := overlayLabel(th, unit.Sp(fs), messages[i].Text)
		l.Color = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
		l.Alignment = text.Start
		l.MaxLines = 2
		return l.Layout(gtx)
	})
}

// autoScrollBottom — единая точка автоскролла «всегда в конец» (Task 3.1):
// ScrollToEnd + Position.BeforeEnd=false. Позиция обновляется немедленно, после
// Layout список стоит у последнего элемента. Общий хелпер для зон 1–4 (DRY).
func autoScrollBottom(list *layout.List) {
	list.ScrollToEnd = true
	list.Position.BeforeEnd = false
}

// layoutTranscriptionHistory — скролл оригиналов речи из History.
// Шрифт единый — fs (Task 3.0).
func layoutTranscriptionHistory(gtx layout.Context, th *material.Theme, history []UIMessage, fs int, list *layout.List, needScroll bool) layout.Dimensions {
	if len(history) == 0 {
		return layout.Dimensions{}
	}

	list.Axis = layout.Vertical
	if needScroll && len(history) > 0 {
		autoScrollBottom(list)
	}
	return list.Layout(gtx, len(history), func(gtx layout.Context, i int) layout.Dimensions {
		l := overlayLabel(th, unit.Sp(fs), history[i].Text)
		l.Color = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
		l.Alignment = text.Start
		l.MaxLines = 8
		return l.Layout(gtx)
	})
}

// ── Helpers ──

// overlayLabel — единая точка создания текстовой строки в оверлее.
// Задаёт межстрочный интервал через LineHeightScale (lineSpacingScale=1.0 =
// стандартный интервал Gio); шрифт не масштабируется. LineHeight явно не
// задаётся. Крутилка на будущее: плотнее — меняется одно число.
func overlayLabel(th *material.Theme, sp unit.Sp, text string) material.LabelStyle {
	l := material.Label(th, sp, text)
	l.LineHeightScale = lineSpacingScale
	return l
}

// layoutAnswersZone — зона 3: при наличии ошибки генерации рендерится она
// (приоритетно), иначе подсказки, иначе — пустой каркас на всю высоту.
// needScroll — автоскролл списка подсказок к концу (Task 3.1).
func (o *Overlay) layoutAnswersZone(gtx layout.Context, th *material.Theme, msg UIMessage, has bool, errorMsg UIMessage, fs int, needScroll bool) layout.Dimensions {
	if errorMsg.Type == Error {
		return layoutError(gtx, th, errorMsg, fs)
	}
	if !has {
		return emptyZoneDims(gtx, gtx.Constraints.Max.Y)
	}
	return layoutAnswers(gtx, th, msg, fs, &o.answersList, needScroll)
}

// layoutError — сообщение об ошибке генерации (Type=Error) в зоне 3: красная
// строка с префиксом. Единый шрифт fs (Task 3.0).
func layoutError(gtx layout.Context, th *material.Theme, msg UIMessage, fs int) layout.Dimensions {
	label := overlayLabel(th, unit.Sp(fs), "⚠️ "+msg.Text)
	label.Color = layoutErrorColor
	label.Alignment = text.Start
	return label.Layout(gtx)
}

func paintBackground(gtx layout.Context, c color.NRGBA) {
	defer clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, c)
}
