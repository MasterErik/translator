package translator

import (
	"strings"
	"testing"
)

func TestBuildSystemPromptDefaultFormat(t *testing.T) {
	// Verify the answer generation prompt specifies the answer format,
	// first-person perspective, candidate-context grounding, and the default
	// en→ru language pair.
	prompt := BuildSystemPrompt("en", "ru")
	checks := []string{
		"first person",
		"candidate context",
		"EN:",
		"| RU:",
		"IT terminology in English",
	}

	for _, check := range checks {
		if !strings.Contains(strings.ToLower(prompt), strings.ToLower(check)) {
			t.Errorf("BuildSystemPrompt(en,ru) should contain %q, got:\n%s", check, prompt)
		}
	}
}

func TestBuildSystemPromptParameterizedTarget(t *testing.T) {
	// en→de: EN label and "| DE:" separator present, RU must be absent.
	enDe := BuildSystemPrompt("en", "de")
	if !strings.Contains(enDe, "EN:") {
		t.Errorf("BuildSystemPrompt(en,de) must contain EN label, got:\n%s", enDe)
	}
	if !strings.Contains(enDe, "| DE:") {
		t.Errorf("BuildSystemPrompt(en,de) must contain | DE: separator, got:\n%s", enDe)
	}
	if strings.Contains(enDe, "RU") {
		t.Errorf("BuildSystemPrompt(en,de) must NOT contain RU, got:\n%s", enDe)
	}

	// Parameterized source label as well.
	deEn := BuildSystemPrompt("de", "en")
	if !strings.Contains(deEn, "DE:") || !strings.Contains(deEn, "| EN:") {
		t.Errorf("BuildSystemPrompt(de,en) must contain DE: label and | EN: separator, got:\n%s", deEn)
	}
}

// TestBuildSystemPromptUppercasesTags — теги выводятся через
// strings.ToUpper, поэтому любой регистр «en»/«En»/«EN» даёт «EN:» / «| RU:».
func TestBuildSystemPromptUppercasesTags(t *testing.T) {
	variants := [][2]string{
		{"en", "ru"},
		{"En", "Ru"},
		{"EN", "RU"},
	}
	for _, v := range variants {
		got := BuildSystemPrompt(v[0], v[1])
		if !strings.Contains(got, "EN:") {
			t.Errorf("BuildSystemPrompt(%q,%q) должен содержать EN:", v[0], v[1])
		}
		if !strings.Contains(got, "| RU:") {
			t.Errorf("BuildSystemPrompt(%q,%q) должен содержать | RU:", v[0], v[1])
		}
		if strings.Contains(got, "en:") || strings.Contains(got, "ru:") {
			t.Errorf("BuildSystemPrompt(%q,%q) не должен содержать строчные теги, got:\n%s", v[0], v[1], got)
		}
	}
}

// TestBuildSystemPromptEmptyLangsDefault — пустые языки дают дефолт en→ru
// (SourceLang/TargetLang по умолчанию).
func TestBuildSystemPromptEmptyLangsDefault(t *testing.T) {
	if got, want := BuildSystemPrompt("", ""), BuildSystemPrompt("en", "ru"); got != want {
		t.Errorf("BuildSystemPrompt(\"\",\"\") must equal the en/ru default")
	}
}

// TestBuildSystemPromptKeepsITTerms удалён: подстрока «Use IT terminology in
// English...» уже проверяется в TestBuildSystemPromptDefaultFormat.

// Test 1/2 — conversation context попадает в user prompt, текущий вопрос всегда в конце.
func TestBuildAnswerPrompt_WithConversationContext(t *testing.T) {
	req := AnswerRequest{
		Question:            "What was your role there?",
		ConversationContext: "Q: Tell me about Project X.\nA: I built the pricing engine.",
		Command:             CommandAnswer,
	}

	prompt := BuildAnswerPrompt(req)

	if !strings.Contains(prompt, "Recent conversation:") {
		t.Error("prompt must contain a conversation context section")
	}
	if !strings.Contains(prompt, "Tell me about Project X.") {
		t.Error("prompt must include conversation history (Project X)")
	}
	if !strings.Contains(prompt, "What was your role there?") {
		t.Error("prompt must include the current question")
	}
	// Текущий вопрос идёт ПОСЛЕ истории.
	if strings.Index(prompt, "Tell me about Project X.") > strings.Index(prompt, "What was your role there?") {
		t.Error("current question must come after conversation history")
	}
}

// Текущий вопрос всегда присутствует, даже без истории.
func TestBuildAnswerPrompt_CurrentQuestionAlwaysPresent(t *testing.T) {
	prompt := BuildAnswerPrompt(AnswerRequest{Question: "Tell me about Go."})
	if !strings.Contains(prompt, "Tell me about Go.") {
		t.Error("current question must always be present")
	}
	if !strings.Contains(prompt, "Generate 1 answer from the candidate's perspective") {
		t.Error("prompt must contain the generation instruction")
	}
}

// F2–F4 добавляют свои инструкции, F1 — нет.
func TestBuildAnswerPrompt_CommandModifiers(t *testing.T) {
	base := AnswerRequest{Question: "Tell me about Go."}

	if p := BuildAnswerPrompt(base); p == "" {
		t.Fatal("F1 prompt must not be empty")
	}

	f3 := BuildAnswerPrompt(AnswerRequest{Question: "Tell me about Go.", Command: CommandMoreContext})
	if !strings.Contains(f3, "more detailed") {
		t.Error("F3 must add more detailed instruction")
	}

	f4 := BuildAnswerPrompt(AnswerRequest{Question: "Tell me about Go.", Command: CommandSimplerEnglish})
	if !strings.Contains(f4, "simpler English") {
		t.Error("F4 must add simpler English instruction")
	}

	// F1 (CommandAnswer) не должен содержать инструкций F2–F4.
	f1 := BuildAnswerPrompt(base)
	for _, phrase := range []string{"Think more deeply", "more detailed", "simpler English"} {
		if strings.Contains(f1, phrase) {
			t.Errorf("F1 must not contain %q", phrase)
		}
	}
}

// Candidate Context и Conversation Context не смешиваются.
func TestBuildAnswerPrompt_SeparatesContexts(t *testing.T) {
	req := AnswerRequest{
		Question:            "What was your role there?",
		CandidateContext:    "Candidate: Senior Go developer",
		ConversationContext: "Q: Tell me about Project X.\nA: I built it.",
		Command:             CommandAnswer,
	}

	userPrompt := BuildAnswerPrompt(req)
	systemPrompt := buildSystemPrompt(req.CandidateContext, "", "")

	if strings.Contains(userPrompt, "Candidate: Senior Go developer") {
		t.Error("candidate context must not be in the user prompt (it goes to system)")
	}
	if !strings.Contains(systemPrompt, "Candidate: Senior Go developer") {
		t.Error("candidate context must be in the system prompt")
	}
	if !strings.Contains(userPrompt, "Tell me about Project X.") {
		t.Error("conversation context must be in the user prompt")
	}
}

// Test 6 — информация о компании не становится фактами кандидата.
func TestBuildAnswerPrompt_CompanyInfoNotCandidateFacts(t *testing.T) {
	conversation := "Q: Our company has 500 employees.\nA: That is good to know."
	candidate := "Candidate: Senior Go developer, PostgreSQL, Kafka."

	req := AnswerRequest{
		Question:            "What is your role?",
		CandidateContext:    candidate,
		ConversationContext: conversation,
	}

	userPrompt := BuildAnswerPrompt(req)
	systemPrompt := buildSystemPrompt(candidate, "", "")

	if strings.Contains(systemPrompt, "500 employees") {
		t.Error("company info must not leak into candidate context (system prompt)")
	}
	if !strings.Contains(userPrompt, "500 employees") {
		t.Error("company info stays in conversation context as a spoken line, not a candidate fact")
	}
	if !strings.Contains(systemPrompt, "Senior Go developer") {
		t.Error("candidate facts must remain in the candidate context")
	}
}

// Б-T0: блок уточнений в user prompt.
//
// Правила: 0/nil — блока нет; 1/3 — все строки в порядке добавления с
// префиксом "- "; >3 — только последние 3 (хвост среза) в порядке следования;
// содержимое уточнений идёт как обычный текст без спецобработки.
func TestBuildAnswerPrompt_NoClarificationsBlockWhenEmpty(t *testing.T) {
	base := BuildAnswerPrompt(AnswerRequest{Question: "Tell me about Go."})
	withEmpty := BuildAnswerPrompt(AnswerRequest{Question: "Tell me about Go.", Clarifications: []string{}})

	for _, tc := range []struct {
		name   string
		prompt string
	}{
		{"nil", base},
		{"empty", withEmpty},
	} {
		if strings.Contains(tc.prompt, "Clarifications from the interviewer:") {
			t.Errorf("%s: clarifications block must be omitted, got:\n%s", tc.name, tc.prompt)
		}
		if strings.Contains(tc.prompt, "\n- ") {
			t.Errorf("%s: prompt must not contain clarification bullets, got:\n%s", tc.name, tc.prompt)
		}
	}
}

func TestBuildAnswerPrompt_SingleClarification(t *testing.T) {
	prompt := BuildAnswerPrompt(AnswerRequest{
		Question:       "Tell me about your scalability work.",
		Clarifications: []string{"I mean at scale"},
	})

	if !strings.Contains(prompt, "Clarifications from the interviewer:") {
		t.Fatalf("prompt must contain the clarifications header, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "\n- I mean at scale") {
		t.Errorf("prompt must render the single clarification as \"- I mean at scale\", got:\n%s", prompt)
	}
	if got := strings.Count(prompt, "\n- "); got != 1 {
		t.Errorf("prompt must contain exactly 1 clarification bullet, got %d:\n%s", got, prompt)
	}
}

func TestBuildAnswerPrompt_ThreeClarificationsInOrder(t *testing.T) {
	prompt := BuildAnswerPrompt(AnswerRequest{
		Question:       "Tell me about your scalability work.",
		Clarifications: []string{"I mean at scale", "multi-region", "with Kubernetes"},
	})

	want := "Clarifications from the interviewer:\n- I mean at scale\n- multi-region\n- with Kubernetes"
	if !strings.Contains(prompt, want) {
		t.Errorf("prompt must contain clarifications block:\n%q\ngot:\n%s", want, prompt)
	}
	// В порядке добавления.
	if i, j := strings.Index(prompt, "I mean at scale"), strings.Index(prompt, "multi-region"); i >= j {
		t.Error("clarifications must keep their insertion order (first before second)")
	}
	// Блок идёт после вопроса.
	if strings.Index(prompt, "The interviewer asked:") > strings.Index(prompt, "Clarifications from the interviewer:") {
		t.Error("clarifications block must come after the question")
	}
}

func TestBuildAnswerPrompt_OnlyLastThreeClarifications(t *testing.T) {
	all := []string{"c1", "c2", "c3", "c4", "c5"}
	prompt := BuildAnswerPrompt(AnswerRequest{
		Question:       "Tell me about your scalability work.",
		Clarifications: all,
	})

	want := "Clarifications from the interviewer:\n- c3\n- c4\n- c5"
	if !strings.Contains(prompt, want) {
		t.Errorf("prompt must contain only the last 3 clarifications:\n%q\ngot:\n%s", want, prompt)
	}
	for _, dropped := range []string{"- c1", "- c2"} {
		if strings.Contains(prompt, dropped) {
			t.Errorf("prompt must NOT contain dropped clarification %q, got:\n%s", dropped, prompt)
		}
	}
	if got := strings.Count(prompt, "\n- "); got != 3 {
		t.Errorf("prompt must contain exactly 3 clarification bullets, got %d:\n%s", got, prompt)
	}
}

func TestBuildAnswerPrompt_ClarificationTextNotEscaped(t *testing.T) {
	tricky := "why? \"quotes\" & <tags> | pipes: 100%"
	prompt := BuildAnswerPrompt(AnswerRequest{
		Question:       "Tell me more.",
		Clarifications: []string{tricky},
	})

	if !strings.Contains(prompt, "\n- "+tricky) {
		t.Errorf("clarification must be inserted verbatim (no escaping), got:\n%s", prompt)
	}
}
