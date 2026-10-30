package translator

import (
	"os"
	"path/filepath"
	"testing"
)

// testSystemPromptBody — тестовая фикстура шаблона. Источник — встроенный
// (go:embed) дефолт: отдельная копия текста расходилась бы с ним при каждой
// правке правил, и тесты падали бы на формулировках, а не на контракте.
var testSystemPromptBody = defaultSystemPromptBody

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
