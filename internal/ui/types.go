// Package ui implements the GioUI overlay window for the Translator application.
// Три зоны: речь (interim), перевод (скролл), подсказки.
package ui

import "time"

// OverlayConfig holds configuration parameters for the GioUI overlay window.
type OverlayConfig struct {
	Width    int // Default: 1200
	Height   int // Default: 650
	FontSize int // Default: 18
}

// UIMessageType classifies the kind of message sent to the overlay.
type UIMessageType string

const (
	Interim          UIMessageType = "Interim"          // текущая речь (верхняя зона)
	Translation      UIMessageType = "Translation"      // перевод (средняя зона)
	AnswerCandidates UIMessageType = "AnswerCandidates" // подсказки (нижняя зона)
	History          UIMessageType = "History"          // история оригиналов (нижняя зона, скролл)
	Status           UIMessageType = "Status"           // статус (для тестов)
	Error            UIMessageType = "Error"            // сообщение об ошибке генерации (зона 3)
)

// Answer is a single generated hint: the answer in the source language and its
// translation into the target language. The pair of languages is configurable
// (see common.Config.SourceLang/TargetLang), so the fields carry neutral names.
//
// ── КОНТРАКТ СИНХРОНИЗАЦИИ ПОЛЕЙ (R2-n1) ─────────────────────────────────────
// Answer и UIMessage (ниже) — граница между dispatcher и ui: поля контракта
// таймлайна добавляются СИНХРОННО в три места, иначе поле молча не доедет до
// рендера (рассинхрон — регресс, который не поймает компилятор):
//  1. тип Answer (здесь);
//  2. тип UIMessage (ниже), если поле едет на уровне сообщения, а не Answer;
//  3. dispatcher.pushHeader (dispatcher.go) — заполняет поля Answer при
//     отправке шапки. Плюс answersMeta (overlay.go) должен распознавать поле
//     как контрактное, иначе одна только новая ненулевая метаданность
//     трактуется как «старый формат» и рендер идёт по старому пути.
//
// Порядок: сначала Answer/UIMessage, затем pushHeader, затем answersMeta, затем
// тест на «только это поле ненулевое → контракт распознан».
type Answer struct {
	Source string
	Target string

	// Поля ниже — контракт таймлайна вопросов (QuestionTimeline). Они позволяют
	// показывать метку «вопрос N из M» и статус генерации, не вводя в пакет ui
	// зависимость от dispatcher (значения State/Mode задаёт dispatcher).
	//
	// QuestionIdx — АБСОЛЮТНЫЙ номер вопроса в таймлайне (нумерация с 0, не
	// сбрасывается при вытеснении старых меток из кольца).
	// Total — АБСОЛЮТНОЕ число заданных вопросов за сессию (totalAsked,
	// не сбрасывается при вытеснении; больше либо равно числу меток в окне).
	// QuestionText — текст вопроса, к которому относится подсказка.
	// State — статус генерации: "idle", "queued", "generating", "ready", "failed".
	// Mode — режим генерации: "answer", "more context", "simpler".
	// HasFresh — есть в окне кольца метки правее активной (индикатор «➤»).
	// Clarifications — уточнения активного вопроса (последние 3 уходит в промпт).
	//
	// Нулевые значения сохраняют прежнее поведение рендера (поля не заполнены —
	// рендер работает как раньше).
	QuestionIdx    int
	Total          int
	QuestionText   string
	State          string
	Mode           string
	HasFresh       bool
	Clarifications []string
}

// UIMessage represents a single message displayed in the overlay.
//
// Контракт синхронизации полей — см. комментарий у типа Answer выше: при
// добавлении поля на уровне сообщения обновляйте UIMessage и Answer синхронно
// с dispatcher.pushHeader, иначе поле не доедет до рендера.
type UIMessage struct {
	Type      UIMessageType
	Text      string
	Answers   []Answer
	Timestamp time.Time

	// Translation — перевод для History-сообщений (чтобы показывать и оригинал, и перевод).
	Translation string
}
