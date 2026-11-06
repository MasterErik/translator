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

// Плейсхолдеры шаблона system-промпта. {{SRC_TAG}}/{{TGT_TAG}} подставляются
// тегами языка в верхнем регистре (EN, RU, DE); {{RESPONSE_FORMAT}} — блоком
// формата ответа согласно LLM_JSON_MODE (F8): JSON-схема либо строковый
// формат «SRC: … | TGT: …».
const (
	SystemPromptPlaceholderSrcTag = "{{SRC_TAG}}"
	SystemPromptPlaceholderTgtTag = "{{TGT_TAG}}"
	SystemPromptPlaceholderFormat = "{{RESPONSE_FORMAT}}"
)

// jsonSystemPromptFormatBlock — раскрытие {{RESPONSE_FORMAT}} для JSON-режимов
// (LLM_JSON_MODE=prompt и response_format): ответ — строго JSON-объект.
const jsonSystemPromptFormatBlock = `Response format — reply with ONLY a JSON object, no markdown fences, no extra keys:
{"answers":[{"source":"<answer in {{SRC_TAG}}, in simple English>","target":"<the same answer translated into {{TGT_TAG}}>"}]}
- 1 to 3 variants in the array, best first.
Do not include explanations, instructions, reminders, or meta-comments.`

// stringSystemPromptFormatBlock — раскрытие {{RESPONSE_FORMAT}} для
// LLM_JSON_MODE=off: старый строковый формат (legacy-парсер parseAnswerHints).
const stringSystemPromptFormatBlock = `Response format — one line per variant, in this exact format:
- {{SRC_TAG}}: <answer in the source language, in simple English> | {{TGT_TAG}}: <translated into the target language>
- Up to 3 variants, best first.
Do not include explanations, instructions, reminders, or meta-comments.`

// responseFormatBlock возвращает блок формата для режима jsonMode.
func responseFormatBlock(jsonMode string) string {
	if jsonMode == JSONModeOff {
		return stringSystemPromptFormatBlock
	}
	return jsonSystemPromptFormatBlock
}

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
		switch match {
		case SystemPromptPlaceholderSrcTag, SystemPromptPlaceholderTgtTag, SystemPromptPlaceholderFormat:
			// Известные плейсхолдеры.
		default:
			return nil, fmt.Errorf("неизвестный плейсхолдер %q (поддерживаются %s, %s и %s)",
				match, SystemPromptPlaceholderSrcTag, SystemPromptPlaceholderTgtTag, SystemPromptPlaceholderFormat)
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
// теги — в верхнем регистре (EN, RU, DE). Формат ответа — JSON (дефолтный
// режим LLM_JSON_MODE).
func (t *SystemPromptTemplate) Render(sourceLang, targetLang string) string {
	return t.RenderFormat(sourceLang, targetLang, JSONModePrompt)
}

// RenderFormat рендерит шаблон с раскрытием {{RESPONSE_FORMAT}} согласно
// режиму jsonMode (F8), затем языковых тегов. Два прохода — блок формата
// сам содержит {{SRC_TAG}}/{{TGT_TAG}}. Если плейсхолдера формата в шаблоне
// нет (старый файл), блок не добавляется — формат берётся из текста файла.
func (t *SystemPromptTemplate) RenderFormat(sourceLang, targetLang, jsonMode string) string {
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

	// Проход 1: блок формата (в нём — языковые плейсхолдеры).
	rendered := strings.ReplaceAll(t.body, SystemPromptPlaceholderFormat, responseFormatBlock(jsonMode))
	// Проход 2: языковые теги.
	rendered = strings.NewReplacer(
		SystemPromptPlaceholderSrcTag, srcTag,
		SystemPromptPlaceholderTgtTag, tgtTag,
	).Replace(rendered)

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

	sb.WriteString("\n\nGenerate up to 3 candidate answers from the candidate's perspective (best first, first person, ready to read aloud):")

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

// JoinConversationContext объединяет непустые блоки conversational-контекста
// (история Q/A, реплики интервьюера и т.п.) двойным переводом строки "\n\n".
// Пустые части отбрасываются: пустой результат означает «блок не добавляется
// в промпт вовсе» — часть ответа не размывается пустыми разделителями.
func JoinConversationContext(parts ...string) string {
	nonEmpty := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	return strings.Join(nonEmpty, "\n\n")
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
