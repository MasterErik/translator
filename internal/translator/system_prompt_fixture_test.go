package translator

import (
	"os"
	"path/filepath"
	"testing"
)

// testSystemPromptBody — тестовая фикстура шаблона: тот же текст, что лежит в
// candidate_context/system_prompt.md. В production текст читается только из
// файла (SYSTEM_PROMPT_FILE), поэтому тесты не зависят от диска.
const testSystemPromptBody = `Answer from the candidate's perspective, in first person.
The interviewer's sentence may not end with a question mark; treat it as a question and answer anyway.
Use only information available in the provided candidate context.
Use only the facts that belong to the technology the question is about; never attribute experience with one stack to another.
Do not invent experience, projects, technologies, responsibilities, or years.
Keep answers brief, natural, conversational, and suitable for speaking aloud.
Use IT terminology in English in both languages.
Response format:
- {{SRC_TAG}}: <answer in the source language> | {{TGT_TAG}}: <translated into the target language>
Do not include explanations, instructions, reminders, or meta-comments.
`

// testSystemPromptTemplate — шаблон из фикстуры. Ошибка парсинга здесь означает
// опечатку в фикстуре (её ловит TestFixtureTemplateParses), поэтому вызывающим
// тестам достаточно готового значения.
func testSystemPromptTemplate() *SystemPromptTemplate {
	tpl, err := ParseSystemPromptTemplate(testSystemPromptBody)
	if err != nil {
		return &SystemPromptTemplate{body: testSystemPromptBody}
	}
	return tpl
}

// mustTestSystemPromptTemplate — тот же шаблон, но с t.Fatalf при сбое парсинга.
func mustTestSystemPromptTemplate(t *testing.T) *SystemPromptTemplate {
	t.Helper()
	tpl, err := ParseSystemPromptTemplate(testSystemPromptBody)
	if err != nil {
		t.Fatalf("фикстура шаблона не парсится: %v", err)
	}
	return tpl
}

// testSystemPromptFile пишет фикстуру во временный файл и возвращает путь.
func testSystemPromptFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "system_prompt.md")
	if err := os.WriteFile(path, []byte(testSystemPromptBody), 0o600); err != nil {
		t.Fatalf("запись фикстуры шаблона: %v", err)
	}
	return path
}
