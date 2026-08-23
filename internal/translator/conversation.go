// Package translator provides the translation engine, which orchestrates
// speech-to-text final transcripts through an LLM provider to produce
// translations and generated interview answers, plus conversation-context
// management for the current interview.
package translator

import (
	"strings"
	"sync"

	"github.com/mastererik/translator/internal/common"
)

// GenerationCommand — команда управления генерацией ответа (функциональные клавиши F1–F4).
type GenerationCommand int

const (
	// CommandAnswer (F1) — обычная генерация ответа на текущий обнаруженный вопрос.
	CommandAnswer GenerationCommand = iota
	// CommandThinkDeeper (F2) — повторный ответ на тот же вопрос с более тщательным
	// reasoning (не раскрывается пользователю, факты кандидата не меняются).
	CommandThinkDeeper
	// CommandMoreContext (F3) — повторный ответ с большей историей разговора и
	// чуть более содержательным ответом.
	CommandMoreContext
	// CommandSimplerEnglish (F4) — переформулировка текущего ответа проще по
	// английскому без изменения смысла и фактов.
	CommandSimplerEnglish
)

func (c GenerationCommand) String() string {
	switch c {
	case CommandAnswer:
		return "Answer"
	case CommandThinkDeeper:
		return "ThinkDeeper"
	case CommandMoreContext:
		return "MoreContext"
	case CommandSimplerEnglish:
		return "SimplerEnglish"
	default:
		return "Unknown"
	}
}

// ConversationTurn — один виток интервью: вопрос интервьюера и финальный ответ кандидата.
type ConversationTurn struct {
	Question string
	Answer   string
}

// AnswerRequest — полный запрос на генерацию ответа, передаваемый LLM-провайдеру.
// Разделяет CandidateContext (постоянные факты кандидата) и ConversationContext
// (уже обсуждавшееся в текущем интервью).
type AnswerRequest struct {
	// Question — текущий вопрос интервьюера.
	Question string
	// CandidateContext — постоянные факты кандидата (база CV). Выше по приоритету,
	// чем ConversationContext. LLM не должна придумывать факты сверх этого.
	CandidateContext string
	// ConversationContext — отформатированная история интервью (ограниченная по размеру).
	ConversationContext string
	// Command — управляющая команда генерации (F1–F4).
	Command GenerationCommand
}

// ConversationHistory хранит историю turns текущего интервью.
//
// История живёт на стороне приложения, а не в памяти LLM. Каждый успешно
// сгенерированный ответ сохраняется; текущий вопрос и ответ становятся частью
// context для следующих вопросов.
//
// Regeneration (F2–F4) НЕ создаёт новый turn: последняя успешно сгенерированная
// версия ответа заменяет предыдущую для того же вопроса.
type ConversationHistory struct {
	mu               sync.Mutex
	turns            []ConversationTurn
	recentTurns      int // максимум turns, попадающих в context (default 6)
	maxContextTokens int // лимит размера context в токенах (default 4000)
}

// NewConversationHistory создаёт историю с заданными лимитами.
// Нулевые/отрицательные значения заменяются на значения по умолчанию.
func NewConversationHistory(recentTurns, maxContextTokens int) *ConversationHistory {
	if recentTurns <= 0 {
		recentTurns = 6
	}
	if maxContextTokens <= 0 {
		maxContextTokens = 4000
	}
	return &ConversationHistory{
		recentTurns:      recentTurns,
		maxContextTokens: maxContextTokens,
	}
}

// RecordAnswer сохраняет финальный ответ на вопрос.
//
// Если последний turn в истории имеет тот же вопрос — заменяет его ответ
// (regeneration F2–F4). Иначе добавляет новый turn в конец истории.
// При превышении recentTurns самые старые turns удаляются, чтобы история
// не росла бесконечно.
func (h *ConversationHistory) RecordAnswer(question, answer string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if len(h.turns) > 0 && h.turns[len(h.turns)-1].Question == question {
		// Regeneration: последняя успешная версия становится текущим ответом.
		h.turns[len(h.turns)-1].Answer = answer
		return
	}

	h.turns = append(h.turns, ConversationTurn{Question: question, Answer: answer})
	if len(h.turns) > h.recentTurns {
		h.turns = h.turns[len(h.turns)-h.recentTurns:]
	}
}

// Recent возвращает копию последних turns истории (для тестов и инспекции).
func (h *ConversationHistory) Recent() []ConversationTurn {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]ConversationTurn, len(h.turns))
	copy(out, h.turns)
	return out
}

// BuildContext форматирует conversation context для промпта.
//
// Правила:
//  1. Берутся последние recentTurns turns (старые уже удалены при RecordAnswer).
//  2. Если суммарный размер (оценка токенов) превышает maxContextTokens —
//     удаляются старые turns (с начала), сохраняются самые свежие.
//  3. Возвращает пустую строку, если истории нет.
//
// Текущий вопрос в BuildContext НЕ входит — он добавляется отдельно в
// BuildAnswerPrompt и никогда не обрезается.
func (h *ConversationHistory) BuildContext() string {
	h.mu.Lock()
	defer h.mu.Unlock()

	if len(h.turns) == 0 {
		return ""
	}

	// Собираем с конца (самые свежие turns), пока укладываемся в лимит.
	// Самый свежий turn сохраняется всегда.
	blocks := make([]string, 0, len(h.turns))
	total := 0
	for i := len(h.turns) - 1; i >= 0; i-- {
		block := "Q: " + h.turns[i].Question + "\nA: " + h.turns[i].Answer
		tk := common.EstimateTokens(block)
		if len(blocks) == 0 || total+tk <= h.maxContextTokens {
			blocks = append(blocks, block)
			total += tk
			continue
		}
		break
	}

	// blocks собраны от свежего к старому — разворачиваем в хронологический порядок.
	var sb strings.Builder
	for i := len(blocks) - 1; i >= 0; i-- {
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString(blocks[i])
	}
	return sb.String()
}
