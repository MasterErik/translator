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
			h := lineHeightPx(fs) * interimVisibleLines
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

// lineHeightCache — кэш измеренной высоты строки по fs. Высота строки зависит
// только от fs (шрифт и LineSpacingScale едины для всех зон), поэтому измеряется
// один раз и переиспользуется во всех кадрах. Мьютекс — потому что render
// вызывается из горутины окна, а тесты читают/пишут конкурентно.
var lineHeightCache = struct {
	mu sync.RWMutex
	m  map[int]int
}{m: make(map[int]int)}

// lineHeightPx — фактическая высота одной строки текста в px для заданного fs.
// Измеряется оффскрин-рендером однострочного label'а тем же путём (overlayLabel),
// что и зоны, и кэшируется по fs. Заменяет эвристику fs*5/4: высота зоны теперь
// всегда кратна настоящей строке — k × lineHeightPx(fs) — и k видимых строк
// помещаются без обрезки последней.
func lineHeightPx(fs int) int {
	lineHeightCache.mu.RLock()
	if h, ok := lineHeightCache.m[fs]; ok {
		lineHeightCache.mu.RUnlock()
		return h
	}
	lineHeightCache.mu.RUnlock()

	h := measureLineHeightPx(fs)

	lineHeightCache.mu.Lock()
	lineHeightCache.m[fs] = h
	lineHeightCache.mu.Unlock()
	return h
}

// measureLineHeightPx — одно измерение: однострочный label (шрифт fs, метрика
// PxPerSp=1, как в оверлее) в оффскрин-контексте; натуральная высота глиф-бокса
// строки. Ширина заведомо велика — перенос не мешает.
func measureLineHeightPx(fs int) int {
	th := material.NewTheme()
	ops := new(op.Ops)
	gtx := layout.Context{
		Ops: ops,
		Constraints: layout.Constraints{
			Min: image.Pt(0, 0),
			Max: image.Pt(2000, 100000),
		},
		Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
	}
	return overlayLabel(th, unit.Sp(fs), "Hg").Layout(gtx).Size.Y
}

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
func historyVisibleHeightPx(fs int) int { return lineHeightPx(fs) * historyVisibleLines }

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
		return emptyZoneDims(gtx, lineHeightPx(fs)*interimVisibleLines)
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

// Цвета меток ленты вопросов по состоянию (Task 2): сигнализация статуса
// вопроса по цвету, различимая не только прозрачностью.
var (
	// questionPendingColor — янтарный: вопрос отправлен/генерируется (queued/generating).
	questionPendingColor = color.NRGBA{R: 200, G: 200, B: 120, A: 255}
	// questionReadyColor — зелёный: ответ готов (ready).
	questionReadyColor = color.NRGBA{R: 120, G: 200, B: 120, A: 255}
	// questionIdleColor — серый: ответ ещё не запрошен (idle/неизвестное состояние).
	questionIdleColor = color.NRGBA{R: 160, G: 160, B: 180, A: 255}
)

// markInactiveAlpha — прозрачность неактивной метки ленты: приглушает оттенок
// состояния, сохраняя его различимость (активная — A=255).
const markInactiveAlpha = 200

// ── Task 5: таймлайн вопросов в зоне 3 ──

// questionStripMaxVisible — максимум одновременно видимых меток [1][2]… в ленте.
// Если меток больше, лента показывает окно вокруг активной (visibleWindow).
const questionStripMaxVisible = 8

// questionStripNewMark — индикатор «справа есть более свежие метки»
// (Total > активной + 1): маленькая стрелка после правого края ленты.
const questionStripNewMark = "➤"

// smallFontSize — шрифт подписей таймлайна (лента меток, шапка вопроса,
// подсказка состояния, режим): fs-2 с полом 10.
func smallFontSize(fs int) int {
	if fs-2 < 10 {
		return 10
	}
	return fs - 2
}

// visibleWindow возвращает индексы меток, видимых в ленте ширины n вокруг
// активной: окно длины min(n, total), в котором active по возможности
// отцентрован, но окно не выходит за границы [0, total-1]. Чистая функция —
// тестируется напрямую. Возвращаемый срез всегда в границах таймлайна.
func visibleWindow(active, total, n int) []int {
	if total <= 0 || n <= 0 {
		return nil
	}
	if n > total {
		n = total
	}
	if active < 0 {
		active = 0
	}
	if active > total-1 {
		active = total - 1
	}
	// Ставим active в середину окна, затем прижимаем окно к границам.
	start := active - n/2
	if start < 0 {
		start = 0
	}
	if start+n > total {
		start = total - n
	}
	out := make([]int, 0, n)
	for i := start; i < start+n; i++ {
		out = append(out, i)
	}
	return out
}

// questionStateMark — маркер состояния метки для ленты: ○ (idle — ждёт F1),
// … (queued/generating), ● (ready), ! (failed).
func questionStateMark(state string) string {
	switch state {
	case "queued", "generating":
		return "…"
	case "ready":
		return "●"
	case "failed":
		return "!"
	default:
		return "○" // idle и неизвестное состояние
	}
}

// isMetadataOnly сообщает, что Answer — каркас шапки (без текста ответа):
// Source и Target пусты. Явный State при этом задаёт состояние по контракту
// таймлайна (dispatcher.pushHeader отправляет один пустой Answer с метаданными).
func isMetadataOnly(a Answer) bool {
	return a.Source == "" && a.Target == ""
}

// answersMeta возвращает метаданные таймлайна из первого Answer с заданным
// контрактом (QuestionIdx/Total/State/Mode/HasFresh/Clarifications). ok=false
// для старого формата, где все поля метаданных нулевые — тогда зона 3 рендерится
// как раньше. Непустые Clarifications тоже признак контракта: иначе уточнения
// молча терялись бы у Answer без прочих метаданных.
func answersMeta(answers []Answer) (Answer, bool) {
	for _, a := range answers {
		if a.Total != 0 || a.State != "" || a.Mode != "" || a.QuestionText != "" || a.HasFresh || len(a.Clarifications) > 0 {
			return a, true
		}
	}
	return Answer{}, false
}

// maxClarificationsShown — сколько уточнений активной метки показывает зона 3.
// Ровно столько же уходит в промпт (последние 3): UI честный — ничего скрытого.
const maxClarificationsShown = 3

// clarificationMark — префикс строки уточнения под шапкой активной метки.
const clarificationMark = "↳ "

// recentClarifications возвращает последние maxClarificationsShown уточнений в
// ХРОНОЛОГИЧЕСКОМ порядке (старое → новое), как они уходят в промпт. Пустой
// список — nil: рендер уточнений пропускается, высота зоны не меняется.
// Возвращает копию — fmt.Errorf-инвариант не затронут, но вызывающий не алиасит
// стек уточнений активной метки (копирование как в dispatcher.Clarifications).
func recentClarifications(a Answer) []string {
	if len(a.Clarifications) == 0 {
		return nil
	}
	start := len(a.Clarifications) - maxClarificationsShown
	if start < 0 {
		start = 0
	}
	out := make([]string, len(a.Clarifications)-start)
	copy(out, a.Clarifications[start:])
	return out
}

// layoutClarifications рисует строки уточнений активной метки под её шапкой:
// каждая на своей строке с префиксом «↳», шрифт smallFontSize(fs) (fs-2, пол 10),
// затемнённый цвет — визуально вложено под вопрос. Пустой список — нулевые
// размеры (ничего не занимает): прежний рендер сохраняется байт-в-байт.
//
// Высота блока НЕ резервируется заранее: строки идут Rigid-элементами подряд
// после шапки, поэтому 1–3 уточнения «вытесняют» столько же строк ответа снизу
// (зона 3 — Flexed, ответы в остатке высоты) — окно не раздувается.
func layoutClarifications(gtx layout.Context, th *material.Theme, clarifications []string, fs int) layout.Dimensions {
	if len(clarifications) == 0 {
		return layout.Dimensions{}
	}
	children := make([]layout.FlexChild, 0, len(clarifications))
	for _, c := range clarifications {
		if c == "" {
			continue
		}
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := overlayLabel(th, unit.Sp(smallFontSize(fs)), clarificationMark+c)
			l.Color = clarificationColor
			l.Alignment = text.Start
			return l.Layout(gtx)
		}))
	}
	if len(children) == 0 {
		return layout.Dimensions{}
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

// clarificationColor — цвет строк уточнений: приглушённый серо-голубой, отличим
// и от шапки вопроса (светло-серый), и от строк ответа (белый/зелёный).
var clarificationColor = color.NRGBA{R: 150, G: 170, B: 200, A: 255}

// layoutQuestionStrip рисует ленту меток [1] [2] [3] над ответами: активная
// выделена цветом, у каждой — маркер состояния, справа — индикатор более
// свежих меток «➤». Ширина ленты ограничена questionStripMaxVisible; окно вокруг
// активной считает visibleWindow. Высота — одна строка smallFontSize(fs).
//
// QuestionIdx/Total — АБСОЛЮТНЫЕ номера меток (не сбрасываются при вытеснении
// старых из кольца): видимое окно считается от относительной позиции активной
// в диапазоне [QuestionIdx-Total+1, QuestionIdx], а лейбл метки показывает её
// абсолютный номер. Признак свежести (HasFresh) тоже абсолютный и приходит от
// таймлайна — сравнение QuestionIdx с Total на уровне UI недопустимо.
func layoutQuestionStrip(gtx layout.Context, th *material.Theme, answers []Answer, meta Answer, fs int) layout.Dimensions {
	total := meta.Total
	active := meta.QuestionIdx
	if total <= 0 {
		// Метаданных нет (старый формат) — по числу ответов.
		total = len(answers)
	}
	if active < 0 {
		active = 0
	}
	// relActive — позиция активной метки внутри окна: при монотонной нумерации
	// с 0 это active, при абсолютных номерах — offset от первой метки окна.
	relActive := active
	if last := meta.QuestionIdx; last >= total-1 && total > 0 {
		relActive = total - 1
	}
	return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layoutMarksRow(gtx, th, answers, meta, relActive, total, fs)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			// Распорка: прижимает индикатор свежих меток к правому краю, а при
			// короткой ленте просто заполняет строку.
			return layout.Dimensions{Size: image.Pt(gtx.Constraints.Min.X, 0)}
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			// «➤» — есть непросмотренная метка правее активной. Признак
			// приходит из dispatcher (Answer.HasFresh) и считается по
			// АБСОЛЮТНОМУ idx свежей метки; сравнение QuestionIdx с Total здесь
			// недопустимо — на >150 вопросах окно кольца короче Total (баг D4).
			if !showFreshIndicator(meta) {
				return layout.Dimensions{}
			}
			l := overlayLabel(th, unit.Sp(smallFontSize(fs)), questionStripNewMark)
			l.Color = color.NRGBA{R: 200, G: 200, B: 220, A: 255}
			return l.Layout(gtx)
		}),
	)
}

// showFreshIndicator решает, рисовать ли индикатор «➤» у правого края ленты.
// Признак приходит от таймлайна (Answer.HasFresh, абсолютный idx свежей метки) —
// UI ничего не досчитывает: сравнение meta.QuestionIdx с meta.Total здесь было бы
// смешением абсолютного номера с числом вопросов и врало бы на >cap вопросах
// (окно кольца короче Total). Вынесено отдельной чистой функцией для юнит-теста.
func showFreshIndicator(meta Answer) bool {
	return meta.HasFresh
}

// layoutMarksRow рисует видимые метки ленты: видимое окно вокруг активной.
// win хранит АБСОЛЮТНЫЕ номера меток (нумерация не сбрасывается при вытеснении
// старых из кольца), поэтому они сразу годятся как idx метки для лейбла и
// поиска её Answer.
func layoutMarksRow(gtx layout.Context, th *material.Theme, answers []Answer, meta Answer, relActive, total, fs int) layout.Dimensions {
	base := meta.QuestionIdx - relActive
	win := visibleWindow(relActive, total, questionStripMaxVisible)
	children := make([]layout.FlexChild, 0, len(win))
	for _, rel := range win {
		idx := base + rel
		active := rel == relActive
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layoutMark(gtx, th, answers, meta, idx, active, fs)
		}))
	}
	return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, children...)
}

// layoutMark рисует одну метку «[N]маркер». Цвет задаётся состоянием метки
// (questionMarkColor): янтарный — генерация, зелёный — готово, красный — ошибка,
// серый — idle; активная метка ярче, неактивные приглушены (alpha). Маркер
// состояния берётся из Answer этой метки (если dispatcher прислал только шапку —
// у активной метки известен лишь её State; остальные получают ○/…).
func layoutMark(gtx layout.Context, th *material.Theme, answers []Answer, meta Answer, idx int, active bool, fs int) layout.Dimensions {
	return markLabelStyle(th, answers, meta, idx, active, fs).Layout(gtx)
}

// markLabelStyle собирает стилизованный лейбл метки «[N]маркер»: маркер и цвет —
// из состояния Answer этой метки (единая точка выбора цвета рендера метки;
// выделено для юнит-тестов цвета без обхода op-потока Gio). idx — АБСОЛЮТНЫЙ
// номер метки: лейбл показывает именно его.
func markLabelStyle(th *material.Theme, answers []Answer, meta Answer, idx int, active bool, fs int) material.LabelStyle {
	state := ""
	if a, ok := answerByQuestionIdx(answers, idx); ok {
		state = a.State
	}
	mark := "○"
	if state != "" {
		mark = questionStateMark(state)
	} else if meta.Total > 0 && idx < meta.QuestionIdx {
		// Метка «слева» от активной уже отжила — готовый ответ или без статуса.
		mark = "○"
	}
	text := "[" + fmt.Sprintf("%d", idx+1) + "]" + mark
	l := overlayLabel(th, unit.Sp(smallFontSize(fs)), text)
	l.Color = questionMarkColor(state, active)
	return l
}

// questionMarkColor — цвет метки ленты по состоянию вопроса: янтарный для
// queued/generating, зелёный для ready, красный (layoutErrorColor) для failed,
// серый для idle/неизвестного. Активная метка — непрозрачная (A=255), неактивная
// приглушена (A=markInactiveAlpha) тем же оттенком: сигнализация состояния
// читается и на невыделенной метке.
func questionMarkColor(state string, active bool) color.NRGBA {
	c := questionIdleColor
	switch state {
	case "queued", "generating":
		c = questionPendingColor
	case "ready":
		c = questionReadyColor
	case "failed":
		c = layoutErrorColor
	}
	if active {
		c.A = 255
	} else {
		c.A = markInactiveAlpha
	}
	return c
}

// answerByQuestionIdx ищет Answer по индексу вопроса портала.
func answerByQuestionIdx(answers []Answer, idx int) (Answer, bool) {
	for _, a := range answers {
		if a.Total != 0 && a.QuestionIdx == idx {
			return a, true
		}
	}
	return Answer{}, false
}

// layoutQuestionHeader рисует шапку активной метки: «Q3 (3/5): <вопрос>». Вопрос
// обрезается до maxQuestionHeaderLines строк (ellipsis), шрифт smallFontSize(fs),
// высота — измеренным продвижением строки (не «на глаз»). Рядом — подпись режима
// генерации Mode («answer»/«more context»/«simpler»).
func layoutQuestionHeader(gtx layout.Context, th *material.Theme, meta Answer, fs int) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layoutQuestionTitle(gtx, th, meta, fs)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if meta.Mode == "" {
				return layout.Dimensions{}
			}
			l := overlayLabel(th, unit.Sp(smallFontSize(fs)), "· "+meta.Mode)
			l.Color = color.NRGBA{R: 150, G: 170, B: 200, A: 255}
			return l.Layout(gtx)
		}),
	)
}

// layoutQuestionTitle — «Q3 (3/5): вопрос»; часть «вопрос» обрезается по числу
// строк (maxQuestionHeaderLines) с ellipsis.
func layoutQuestionTitle(gtx layout.Context, th *material.Theme, meta Answer, fs int) layout.Dimensions {
	prefix := ""
	if meta.Total > 0 {
		prefix = fmt.Sprintf("Q%d (%d/%d): ", meta.QuestionIdx+1, meta.QuestionIdx+1, meta.Total)
	}
	l := overlayLabel(th, unit.Sp(smallFontSize(fs)), prefix+meta.QuestionText)
	l.Color = color.NRGBA{R: 220, G: 220, B: 230, A: 255}
	l.Alignment = text.Start
	l.MaxLines = maxQuestionHeaderLines
	return l.Layout(gtx)
}

// maxQuestionHeaderLines — сколько строк вопроса показывает шапка активной метки.
const maxQuestionHeaderLines = 2

// answerStateHint — подсказка вместо пустоты, когда ответа нет: idle — ждём F1,
// queued/generating — генерация, failed — ошибка. Явный State имеет приоритет;
// пустой State/ready трактуется как idle (ответ ещё не запрошен).
func answerStateHint(state string) string {
	switch state {
	case "queued", "generating":
		return "генерация…"
	case "failed":
		return "ошибка генерации"
	default:
		return "F1 — ответить"
	}
}

// layoutAnswerHint — подсказка состояния метки вместо пустоты: та же логика
// цветов, что у подписей, шрифт smallFontSize(fs), отступ одной строкой сверху
// (чтобы не липнуть к шапке).
func layoutAnswerHint(gtx layout.Context, th *material.Theme, state string, fs int) layout.Dimensions {
	sp := unit.Sp(smallFontSize(fs))
	l := overlayLabel(th, sp, answerStateHint(state))
	c := color.NRGBA{R: 180, G: 180, B: 200, A: 255}
	switch state {
	case "failed":
		c = layoutErrorColor
	case "queued", "generating":
		c = color.NRGBA{R: 200, G: 200, B: 120, A: 255}
	}
	l.Color = c
	l.Alignment = text.Start
	return l.Layout(gtx)
}

// layoutAnswers — подсказки активной метки (зона 3): лента меток и шапка
// вопроса сверху (только для сообщений с контрактом таймлайна), затем строки
// Source (белый) / Target (зелёный) с вертикальным скроллом. Если текста
// ответа нет — вместо пустоты подсказка состояния (idle/queued/failed).
// Шрифт строк ответа — единый fs (Task 3.0). needScroll — автоскролл (Task 3.1).
func layoutAnswers(gtx layout.Context, th *material.Theme, msg UIMessage, fs int, list *layout.List, needScroll bool) layout.Dimensions {
	// Собираем плоский список: Source (белый), Target (зелёный).
	items := make([]answerLine, 0, len(msg.Answers)*2)
	for _, ans := range msg.Answers {
		if ans.Source != "" {
			items = append(items, answerLine{text: ans.Source, isRU: false})
		}
		if ans.Target != "" {
			items = append(items, answerLine{text: ans.Target, isRU: true})
		}
	}

	meta, hasMeta := answersMeta(msg.Answers)

	// Старый формат (метаданных нет) — рендер как раньше: чистый скролл строк.
	if !hasMeta {
		return layoutAnswerLines(gtx, th, items, fs, list, needScroll)
	}

	// Контракт таймлайна: лента меток + шапка активного вопроса + (при пустом
	// ответе) подсказка состояния. Ответные строки скроллятся в остатке высоты.
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layoutQuestionStrip(gtx, th, msg.Answers, meta, fs)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if meta.QuestionText == "" {
				return layout.Dimensions{}
			}
			return layoutQuestionHeader(gtx, th, meta, fs)
		}),
		// Б-T4: строки уточнений активной метки — подряд под шапкой вопроса,
		// каждая на своей строке. Пустой список → нулевые размеры (прежний
		// рендер байт-в-байт); строки вытесняют ответы сверху в остатке высоты.
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layoutClarifications(gtx, th, recentClarifications(meta), fs)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if len(items) > 0 {
				return layout.Dimensions{}
			}
			return layoutAnswerHint(gtx, th, meta.State, fs)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if len(items) == 0 {
				return layout.Dimensions{Size: image.Pt(gtx.Constraints.Min.X, 0)}
			}
			return layoutAnswerLines(gtx, th, items, fs, list, needScroll)
		}),
	)
}

// layoutAnswerLines — скролл строк ответа (общая часть старого и нового формата).
func layoutAnswerLines(gtx layout.Context, th *material.Theme, items []answerLine, fs int, list *layout.List, needScroll bool) layout.Dimensions {
	if len(items) == 0 {
		return layout.Dimensions{}
	}
	list.Axis = layout.Vertical
	if needScroll {
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
