package translator

import (
	"strings"
	"testing"
)

func TestSystemPromptTranslationContainsITTermsPreservation(t *testing.T) {
	// Verify the system prompt explicitly instructs the model to preserve
	// IT terminology and keep terms in English.
	terms := []string{
		"NEVER translate IT terminology",
		"keep them as-is",
		"Kubernetes", "Docker", "CI/CD", "API", "microservices",
		"race condition", "mutex", "goroutine",
		"Deadlock", "CQRS",
	}

	for _, term := range terms {
		if !strings.Contains(SystemPromptTranslation, term) {
			t.Errorf("SystemPromptTranslation should contain %q", term)
		}
	}
}

func TestSystemPromptTranslationRequiresRussianOutput(t *testing.T) {
	if !strings.Contains(SystemPromptTranslation, "Russian") {
		t.Error("SystemPromptTranslation should instruct to translate into Russian")
	}
}

func TestSystemPromptTranslationNoMetaCommentary(t *testing.T) {
	if !strings.Contains(SystemPromptTranslation, "no meta-commentary") {
		t.Error("SystemPromptTranslation should prohibit meta-commentary")
	}
}

func TestSystemPromptAnswerGenFormat(t *testing.T) {
	// Verify the answer generation prompt specifies bullet format and language.
	checks := []string{
		"1",
		"bullet",
		"Russian",
		"dash (-)",
		"first person",
	}

	for _, check := range checks {
		if !strings.Contains(strings.ToLower(SystemPromptAnswerGen), strings.ToLower(check)) {
			t.Errorf("SystemPromptAnswerGen should contain %q", check)
		}
	}
}

func TestSystemPromptAnswerGenKeepsITTerms(t *testing.T) {
	if !strings.Contains(SystemPromptAnswerGen, "API, Kubernetes, CI/CD") {
		t.Error("SystemPromptAnswerGen should instruct to keep IT terms in English")
	}
}

func TestBuildTranslationPromptWithHistory(t *testing.T) {
	text := "What is your experience with Docker?"
	history := []string{
		"Hello, nice to meet you.",
		"I have been working in IT for 5 years.",
	}

	result := BuildTranslationPrompt(text, history)

	// Should contain the text to translate.
	if !strings.Contains(result, text) {
		t.Errorf("BuildTranslationPrompt should contain the text to translate, got:\n%s", result)
	}

	// Should contain history entries.
	for _, h := range history {
		if !strings.Contains(result, h) {
			t.Errorf("BuildTranslationPrompt should contain history entry %q", h)
		}
	}

	// Should contain the structure markers.
	if !strings.Contains(result, "Previous exchanges") {
		t.Error("BuildTranslationPrompt should contain 'Previous exchanges' section")
	}
	if !strings.Contains(result, "Translate the following English text to Russian") {
		t.Error("BuildTranslationPrompt should contain translation instruction")
	}
}

func TestBuildTranslationPromptEmptyHistory(t *testing.T) {
	text := "Tell me about microservices."
	result := BuildTranslationPrompt(text, nil)

	if !strings.Contains(result, text) {
		t.Errorf("BuildTranslationPrompt should contain the text to translate, got:\n%s", result)
	}

	if !strings.Contains(result, "(no previous context)") {
		t.Error("BuildTranslationPrompt should indicate empty history")
	}

	if strings.Contains(result, "1. ") {
		t.Error("BuildTranslationPrompt should NOT contain numbered entries when history is empty")
	}
}

func TestBuildAnswerPromptWithCV(t *testing.T) {
	question := "How did you handle database migrations?"
	cvContext := "5 years as backend developer, PostgreSQL, MongoDB, Kubernetes."

	// CV context now goes to system message, NOT user prompt.
	result := BuildAnswerPrompt(question)

	if !strings.Contains(result, question) {
		t.Errorf("BuildAnswerPrompt should contain the question, got:\n%s", result)
	}
	// CV context should NOT be in user prompt anymore (it's the system message).
	if strings.Contains(result, cvContext) {
		t.Errorf("BuildAnswerPrompt should NOT contain CV context (it's the system message now):\n%s", result)
	}
	if !strings.Contains(result, "Generate 1 answer from the candidate's perspective") {
		t.Error("BuildAnswerPrompt should contain the generation instruction")
	}
}

func TestBuildAnswerPromptEmptyCV(t *testing.T) {
	question := "What is TDD?"
	result := BuildAnswerPrompt(question)

	if !strings.Contains(result, question) {
		t.Errorf("BuildAnswerPrompt should contain the question, got:\n%s", result)
	}
	// No CV context — no "(no CV context provided)" placeholder needed anymore.
	if !strings.Contains(result, "Generate 1 answer from the candidate's perspective") {
		t.Error("BuildAnswerPrompt should contain the generation instruction")
	}
}
