// Package translator provides the translation engine, which orchestrates
// speech-to-text final transcripts through an LLM provider to produce
// translations and generated interview answers.
package translator

import (
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// DefaultSourceLang/DefaultTargetLang are the language pair used when a caller
// passes empty strings to SystemPromptTemplate.Render.
const (
	DefaultSourceLang = "en"
	DefaultTargetLang = "ru"
)

// defaultSystemPromptBody — встроенный (go:embed) дефолтный шаблон. Им
// заполняется отсутствующий файл SYSTEM_PROMPT_FILE при старте; после создания
// файла источником правды становится сам файл (правится без перекомпиляции).
//
//go:embed default_system_prompt.md
var defaultSystemPromptBody string

// Плейсхолдеры шаблона system-промпта. Подставляются тегами языка в верхнем
// регистре (EN, RU, DE), как раньше делал зашитый в код текст.
const (
	SystemPromptPlaceholderSrcTag = "{{SRC_TAG}}"
	SystemPromptPlaceholderTgtTag = "{{TGT_TAG}}"
)

// systemPromptPlaceholderRe находит любой {{...}}-плейсхолдер, чтобы отвергать
// неизвестные имена на старте, а не отправлять в LLM текст с «{{TYPO}}».
var systemPromptPlaceholderRe = regexp.MustCompile(`\{\{[A-Za-z0-9_]+\}\}`)

// SystemPromptTemplate — шаблон системного промпта (правила генерации ответа),
// прочитанный из файла SYSTEM_PROMPT_FILE. Текст живёт вне кода, чтобы
// формулировки правились без перекомпиляции; язык ответа остаётся
// параметризованным через плейсхолдеры {{SRC_TAG}}/{{TGT_TAG}}.
type SystemPromptTemplate struct {
	body string
}

// ParseSystemPromptTemplate проверяет и принимает текст шаблона: пустой шаблон
// и неизвестные плейсхолдеры — ошибка (fail-fast на старте приложения).
func ParseSystemPromptTemplate(text string) (*SystemPromptTemplate, error) {
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("пустой шаблон system-промпта")
	}
	for _, match := range systemPromptPlaceholderRe.FindAllString(text, -1) {
		if match != SystemPromptPlaceholderSrcTag && match != SystemPromptPlaceholderTgtTag {
			return nil, fmt.Errorf("неизвестный плейсхолдер %q (поддерживаются %s и %s)",
				match, SystemPromptPlaceholderSrcTag, SystemPromptPlaceholderTgtTag)
		}
	}
	return &SystemPromptTemplate{body: text}, nil
}

// LoadSystemPromptTemplate читает шаблон из файла SYSTEM_PROMPT_FILE.
// Если файла нет — создаёт его из встроенного дефолта (go:embed) и работает на
// нём: первый запуск не требует ручных шагов, а дальше файл правится как обычный
// текст без перекомпиляции. Пустой путь, нечитаемый файл или битый шаблон —
// ошибка (fail-fast), а не тихая подмена: иначе «правка промпта не применяется»
// остаётся без следов в логах.
func LoadSystemPromptTemplate(path string) (*SystemPromptTemplate, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("SYSTEM_PROMPT_FILE не задан: укажите путь к файлу шаблона system-промпта")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("чтение файла system-промпта %s: %w", path, err)
		}
		if werr := writeDefaultSystemPrompt(path); werr != nil {
			return nil, fmt.Errorf("создание файла system-промпта %s: %w", path, werr)
		}
		data = []byte(defaultSystemPromptBody)
	}

	tpl, err := ParseSystemPromptTemplate(string(data))
	if err != nil {
		return nil, fmt.Errorf("файл system-промпта %s: %w", path, err)
	}
	return tpl, nil
}

// writeDefaultSystemPrompt создаёт отсутствующий файл шаблона из встроенного
// дефолта (создавая каталог при необходимости).
func writeDefaultSystemPrompt(path string) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("каталог шаблона: %w", err)
		}
	}
	if err := os.WriteFile(path, []byte(defaultSystemPromptBody), 0o644); err != nil {
		return err
	}
	slog.Info("translator: создан файл system-промпта по умолчанию", "path", path)
	return nil
}

// Render подставляет языковую пару в шаблон. Пустые языки дают дефолт en/ru,
// теги — в верхнем регистре (EN:, RU:, DE:).
func (t *SystemPromptTemplate) Render(sourceLang, targetLang string) string {
	if t == nil {
		return ""
	}
	if sourceLang == "" {
		sourceLang = DefaultSourceLang
	}
	if targetLang == "" {
		targetLang = DefaultTargetLang
	}
	srcTag := strings.ToUpper(sourceLang)
	tgtTag := strings.ToUpper(targetLang)

	rendered := strings.NewReplacer(
		SystemPromptPlaceholderSrcTag, srcTag,
		SystemPromptPlaceholderTgtTag, tgtTag,
	).Replace(t.body)

	return normalizeSystemPromptText(rendered)
}

// normalizeSystemPromptText делает результат независимым от привычек редактора:
// CRLF → LF, пустые строки по краям не влияют, ровно один завершающий \n
// (к нему в openai.go приклеивается секция candidate context).
func normalizeSystemPromptText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.Trim(s, "\n")
	if s == "" {
		return ""
	}
	return s + "\n"
}

// BuildAnswerPrompt constructs the full user prompt for generating interview
// answer hints. It includes the conversation context (recent history) and the
// current question. Candidate context is passed separately as the system
// message (see GenerateAnswers / buildSystemPrompt).
//
// Структура (логически):
//
//	[Conversation Context]
//	The interviewer asked:
//	<question>
//	[Clarifications from the interviewer]  (только если есть, максимум 3)
//	Generate 1 answer from the candidate's perspective ...
//	[modifier for F2–F4]
func BuildAnswerPrompt(req AnswerRequest) string {
	var sb strings.Builder

	if req.ConversationContext != "" {
		sb.WriteString("Recent conversation:\n")
		sb.WriteString(req.ConversationContext)
		sb.WriteString("\n\n")
	}

	sb.WriteString("The interviewer asked:\n")
	sb.WriteString(req.Question)

	if clarifications := formatClarifications(req.Clarifications); clarifications != "" {
		sb.WriteString("\n\n")
		sb.WriteString(clarifications)
	}

	sb.WriteString("\n\nGenerate 1 answer from the candidate's perspective (first person, ready to read aloud):")

	if instruction := commandInstruction(req.Command); instruction != "" {
		sb.WriteString("\n")
		sb.WriteString(instruction)
	}

	return sb.String()
}

// maxClarifications — сколько последних уточнений попадает в промпт.
const maxClarifications = 3

// formatClarifications renders the clarifications block for the user prompt.
// Only the last maxClarifications items are included (in chronological order);
// each item is prefixed with "- ". Returns an empty string when there is
// nothing to render, so the block is omitted entirely.
func formatClarifications(clarifications []string) string {
	if len(clarifications) == 0 {
		return ""
	}

	start := len(clarifications) - maxClarifications
	if start < 0 {
		start = 0
	}
	last := clarifications[start:]

	var sb strings.Builder
	sb.WriteString("Clarifications from the interviewer:")
	for _, c := range last {
		sb.WriteString("\n- ")
		sb.WriteString(c)
	}
	return sb.String()
}

// commandInstruction возвращает дополнительную инструкцию для команды
// управления генерацией (F2–F4). Для F1 (CommandAnswer) — пустая строка.
func commandInstruction(cmd GenerationCommand) string {
	switch cmd {
	case CommandMoreContext:
		return "Use more of the available conversation history and give a " +
			"slightly more detailed answer, keeping a natural length."
	case CommandSimplerEnglish:
		return "Rephrase the answer using simpler English. Preserve the meaning " +
			"and all facts, and do not add new information. The Russian " +
			"translation must match the new English version."
	default:
		return ""
	}
}
