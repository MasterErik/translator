// Package translator provides the translation engine, which orchestrates
// speech-to-text final transcripts through an LLM provider to produce
// translations and generated interview answers.
package translator

import (
	"strings"
)

// DefaultSourceLang/DefaultTargetLang are the language pair used when a caller
// passes empty strings to BuildSystemPrompt.
const (
	DefaultSourceLang = "en"
	DefaultTargetLang = "ru"
)

// BuildSystemPrompt builds the system prompt for answer generation, templated
// from the configured language pair. The response format is
// "<SRC>: <answer in source> | <TGT>: <translation in target>" where the tags
// are the uppercased ISO 639-1 codes (EN:, RU:, DE:, FR:). Empty langs fall
// back to the defaults (en/ru), so the default en→ru behaviour is unchanged.
func BuildSystemPrompt(sourceLang, targetLang string) string {
	if sourceLang == "" {
		sourceLang = DefaultSourceLang
	}
	if targetLang == "" {
		targetLang = DefaultTargetLang
	}
	srcTag := strings.ToUpper(sourceLang)
	tgtTag := strings.ToUpper(targetLang)

	return "\n" +
		"Answer from the candidate's perspective, in first person.\n" +
		"Use only information available in the provided candidate context.\n" +
		"Do not invent experience, projects, technologies, responsibilities, or years.\n" +
		"Keep answers brief, natural, conversational, and suitable for speaking aloud.\n" +
		"Use IT terminology in English in both languages.\n" +
		"Response format:\n" +
		"- " + srcTag + ": <answer in the source language> | " + tgtTag + ": <translated into the target language>\n" +
		"Do not include explanations, instructions, reminders, or meta-comments.\n"
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

	sb.WriteString("\n\nGenerate 1 answer from the candidate's perspective (first person, ready to read aloud):")

	if instruction := commandInstruction(req.Command); instruction != "" {
		sb.WriteString("\n")
		sb.WriteString(instruction)
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
