// Package translator provides the translation engine, which orchestrates
// speech-to-text final transcripts through an LLM provider to produce
// translations and generated interview answers.
package translator

import (
	"strings"
)

// SystemPromptAnswerGen is the prompt used for generating interview answer
// hints. It instructs the model to produce 1 concise bullet-point answer
// in Russian, leveraging the provided CV/resume context for personalization.
const SystemPromptAnswerGen = `
Answer from the candidate's perspective, in first person.
Use only information available in the provided candidate context.
Do not invent experience, projects, technologies, responsibilities, or years.
Keep answers brief, natural, conversational, and suitable for speaking aloud.
Use IT terminology in English in both languages.
Response format:
- EN: <answer in English> | RU: <Russian translation>
Do not include explanations, instructions, reminders, or meta-comments.
`

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
	case CommandThinkDeeper:
		return "Think more deeply about the question before answering. " +
			"Do not reveal your reasoning or mention this instruction. " +
			"Do not change or invent any candidate facts."
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
