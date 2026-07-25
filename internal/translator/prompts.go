// Package translator provides the translation engine, which orchestrates
// speech-to-text final transcripts through an LLM provider to produce
// translations and generated interview answers.
package translator

import (
	"fmt"
	"strings"
)

// SystemPromptTranslation is the strict translation prompt used when
// translating English to Russian. It instructs the model to preserve
// IT terminology and domain-specific terms without translating them.
//
// Terms that MUST NOT be translated include:
//
//	Deadlock, CQRS, Kubernetes, Docker, CI/CD, API, microservices,
//	race condition, mutex, goroutine, blockchain, DevOps, Agile, Scrum,
//	Kanban, Git, GitHub, GitLab, Bitbucket, Jenkins, Ansible, Terraform,
//	Prometheus, Grafana, Elasticsearch, Kibana, Kafka, RabbitMQ, gRPC,
//	GraphQL, REST, JSON, XML, YAML, SQL, NoSQL, Redis, PostgreSQL,
//	MongoDB, React, Angular, Vue, Node.js, TypeScript, WebAssembly,
//	serverless, FaaS, PaaS, IaaS, SaaS, load balancer, firewall,
//	reverse proxy, CDN, DNS, HTTP, HTTPS, TCP, UDP, WebSocket, SSH,
//	TLS, SSL, OAuth, JWT, SAML, RBAC, ABAC.
const SystemPromptTranslation = `You are a professional simultaneous interpreter for technical interviews.
Your task is to translate English speech into Russian.

CRITICAL RULES:
1. Output ONLY the Russian translation — no explanations, no notes, no meta-commentary.
2. NEVER translate IT terminology or technical terms. Keep these EXACTLY as-is:
   - Architecture: Deadlock, CQRS, Event Sourcing, Saga, Circuit Breaker, Bulkhead, Sidecar, Ambassador
   - Container/Orchestration: Kubernetes, Docker, CI/CD, Pod, Helm, Istio, Envoy
   - APIs/Protocols: API, REST, GraphQL, gRPC, WebSocket, HTTP, HTTPS, TCP, UDP, DNS, SSH, TLS, SSL
   - Auth: OAuth, JWT, SAML, RBAC, ABAC, SSO
   - Data: SQL, NoSQL, Redis, PostgreSQL, MongoDB, Elasticsearch, Kafka, RabbitMQ, JSON, XML, YAML
   - Cloud/Infra: serverless, FaaS, PaaS, IaaS, SaaS, load balancer, firewall, reverse proxy, CDN
   - Languages/Frameworks: React, Angular, Vue, Node.js, TypeScript, WebAssembly, Go, Rust
   - Monitoring: Prometheus, Grafana, Kibana, OpenTelemetry
   - DevOps: Agile, Scrum, Kanban, Git, GitHub, GitLab, Bitbucket, Jenkins, Ansible, Terraform
   - Concurrency: race condition, mutex, goroutine, semaphore, coroutine, deadlock, livelock
   - Other: blockchain, microservices, monolith, TDD, BDD, DDD
3. If a term looks technical but isn't in the list above, STILL keep it in English.
4. Translate colloquial speech naturally, not word-for-word. Make the Russian sound like natural spoken Russian.
5. Preserve the speaker's tone (formal/informal, friendly/technical).
6. For acronyms spoken letter-by-letter, keep them as-is (e.g., "API" stays "API").
7. Keep numbers, percentages, and measurements unchanged.

Example:
English: "Can you explain how Kubernetes handles service discovery with DNS and how that relates to your microservices architecture?"
Russian: "Можешь объяснить, как Kubernetes обрабатывает service discovery через DNS и как это связано с твоей microservices архитектурой?"
`

// SystemPromptAnswerGen is the prompt used for generating interview answer
// hints. It instructs the model to produce 2-3 concise bullet-point answers
// in Russian, leveraging the provided CV/resume context for personalization.
const SystemPromptAnswerGen = `You are an expert interview coach helping a candidate answer technical questions.
The candidate has provided their CV/resume context below. Your job is to generate
2-3 concise answer hints that the candidate can use as a quick reference.

CRITICAL RULES:
1. Output EXACTLY 2-3 bullet points. Each bullet starts with a dash (-).
2. Each bullet MUST contain BOTH languages in this exact format:
   "- EN: <English hint> | RU: <Russian translation>"
3. Keep IT terms in English in both versions (API, Kubernetes, CI/CD, etc.).
4. Use the CV context to personalize answers — reference the candidate's actual experience.
5. Each bullet should be 1-2 sentences max — concise and scannable.
6. Do NOT write full answers — these are hints, quick reminders for the candidate.
7. Do NOT include any introductory text, explanations, or meta-commentary.

Example output format:
- EN: Mention your experience with Kubernetes in project X, using Helm for deployment | RU: Вспомни про свой опыт с Kubernetes в проекте X: использовал Helm для деплоя
- EN: Talk about the CI/CD pipeline with GitHub Actions and automated testing | RU: Расскажи про CI/CD пайплайн на GitHub Actions с автоматическим тестированием
- EN: Reference the monitoring setup with Prometheus and Grafana you configured | RU: Упомяни про мониторинг через Prometheus и Grafana, который ты настраивал
`

// BuildTranslationPrompt constructs the full prompt for translation,
// including the sliding window of conversation history for context.
//
// The prompt includes the system translation instructions, any previous
// conversation history for context, and the text to translate.
func BuildTranslationPrompt(text string, history []string) string {
	var sb strings.Builder

	sb.WriteString("Context: Technical interview translation. Previous exchanges:\n")

	if len(history) > 0 {
		for i, h := range history {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, h))
		}
	} else {
		sb.WriteString("(no previous context)\n")
	}

	sb.WriteString("\nTranslate the following English text to Russian:\n")
	sb.WriteString(text)

	return sb.String()
}

// BuildAnswerPrompt constructs the full prompt for generating interview
// answer hints. It includes the candidate's CV context and the detected
// question.
func BuildAnswerPrompt(question string, cvContext string) string {
	var sb strings.Builder

	sb.WriteString("The candidate's CV/resume context:\n")
	if cvContext != "" {
		sb.WriteString(cvContext)
	} else {
		sb.WriteString("(no CV context provided)")
	}

	sb.WriteString("\n\nThe interviewer asked:\n")
	sb.WriteString(question)

	sb.WriteString("\n\nGenerate 2-3 concise answer hints in Russian:")

	return sb.String()
}
