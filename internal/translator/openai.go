package translator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

const maxRetries = 3

// Режимы JSON-вывода (F7, LLM_JSON_MODE).
const (
	// JSONModePrompt — JSON-инструкция задаётся system-промптом (для
	// reasoning-моделей типа gpt-oss/qwen: Groq отклоняет response_format
	// для части reasoning-моделей, а instruction follow у них отличный).
	JSONModePrompt = "prompt"
	// JSONModeResponseFormat — то же + response_format json_object в запросе
	// (жёсткий режим для non-reasoning моделей).
	JSONModeResponseFormat = "response_format"
	// JSONModeOff — старый строковый формат «EN: … | RU: …», JSON-парсинг выключен.
	JSONModeOff = "off"
)

// validReasoningEfforts — допустимые значения reasoning_effort (OpenAI/Groq).
var validReasoningEfforts = map[string]struct{}{
	"low":    {},
	"medium": {},
	"high":   {},
}

// ValidateJSONMode проверяет значение LLM_JSON_MODE (пустое = дефолт prompt).
func ValidateJSONMode(mode string) error {
	switch mode {
	case "", JSONModePrompt, JSONModeResponseFormat, JSONModeOff:
		return nil
	default:
		return fmt.Errorf("LLM_JSON_MODE: недопустимое значение %q (ожидается %q, %q или %q)",
			mode, JSONModePrompt, JSONModeResponseFormat, JSONModeOff)
	}
}

// ValidateReasoningEffort проверяет значение LLM_REASONING_EFFORT (пустое = не отправлять).
func ValidateReasoningEffort(effort string) error {
	e := strings.ToLower(strings.TrimSpace(effort))
	if e == "" {
		return nil
	}
	if _, ok := validReasoningEfforts[e]; !ok {
		return fmt.Errorf("LLM_REASONING_EFFORT: недопустимое значение %q (ожидается low, medium или high)", effort)
	}
	return nil
}

type ChatProvider struct {
	client    *openai.Client
	baseURL   string
	apiKey    string
	model     string
	maxTokens int
	mu        sync.Mutex

	// systemTemplate — шаблон system-промпта из SYSTEM_PROMPT_FILE (nil →
	// встроенный дефолт). Читается под p.mu.
	systemTemplate *SystemPromptTemplate

	// F7: reasoning_effort для reasoning-моделей, JSON-режим вывода и
	// fallback-цепочка моделей. Читаются под p.mu (buildChatRequest).
	reasoningEffort string
	jsonMode        string
	fallbackModels  []string

	// retryAfterUntil — до какого времени действует Retry-After последнего
	// 429-ответа (заполняет retryAfterTransport под p.retryMu). backoff-цикл
	// удлиняет ожидание до этого момента.
	retryMu         sync.Mutex
	retryAfterUntil time.Time
}

func NewChatProvider(baseURL, apiKey, model string) *ChatProvider {
	cfg := openai.DefaultConfig(apiKey)
	cfg.BaseURL = baseURL
	p := &ChatProvider{
		client:   openai.NewClientWithConfig(cfg),
		baseURL:  baseURL,
		apiKey:   apiKey,
		model:    model,
		jsonMode: JSONModePrompt,
	}
	// Retry-After из 429 читает transport-обёртка: go-openai APIError не
	// несёт заголовков ответа, поэтому единственная точка, где они доступны —
	// http.RoundTripper. Базовый transport берём из HTTPClient, если это
	// *http.Client (дефолтная конфигурация), иначе — DefaultTransport.
	baseTransport := http.DefaultTransport
	if hc, ok := cfg.HTTPClient.(*http.Client); ok && hc != nil && hc.Transport != nil {
		baseTransport = hc.Transport
	}
	cfg.HTTPClient = &http.Client{Transport: &retryAfterTransport{base: baseTransport, p: p}}
	p.client = openai.NewClientWithConfig(cfg)
	return p
}

// retryAfterTransport перехватывает 429-ответы и запоминает Retry-After в
// провайдере (F7). Порядок заголовков и тело ответа не меняются.
type retryAfterTransport struct {
	base http.RoundTripper
	p    *ChatProvider
}

func (t *retryAfterTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rt := t.base
	if rt == nil {
		rt = http.DefaultTransport
	}
	resp, err := rt.RoundTrip(req)
	if err == nil && resp != nil && resp.StatusCode == http.StatusTooManyRequests {
		if d := parseRetryAfter(resp.Header.Get("Retry-After")); d > 0 {
			t.p.retryMu.Lock()
			t.p.retryAfterUntil = time.Now().Add(d)
			t.p.retryMu.Unlock()
		}
	}
	return resp, err
}

// parseRetryAfter разбирает заголовок Retry-After: секунды или HTTP-date.
// Отрицательное/невалидное — 0 (нет данных).
func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func (p *ChatProvider) SetMaxTokens(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.maxTokens = n
}

// SetSystemPromptTemplate задаёт шаблон system-промпта (из SYSTEM_PROMPT_FILE).
// nil возвращает встроенный дефолт. Вызывается на старте, до первых запросов.
func (p *ChatProvider) SetSystemPromptTemplate(tpl *SystemPromptTemplate) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.systemTemplate = tpl
}

// SetReasoningEffort задаёт reasoning_effort для reasoning-моделей (F7).
// Пустая строка — параметр не отправляется.
func (p *ChatProvider) SetReasoningEffort(effort string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reasoningEffort = strings.ToLower(strings.TrimSpace(effort))
}

// SetFallbackModels задаёт fallback-цепочку моделей (F7): при исчерпании
// ретраев основной модели запрос повторяется на следующей из списка.
func (p *ChatProvider) SetFallbackModels(models []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fallbackModels = append([]string(nil), models...)
}

// SetJSONMode задаёт режим JSON-вывода (F7): JSONModePrompt /
// JSONModeResponseFormat / JSONModeOff.
func (p *ChatProvider) SetJSONMode(mode string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if mode == "" {
		mode = JSONModePrompt
	}
	p.jsonMode = mode
}

// buildChatRequestUnderLock собирает ChatCompletionRequest (F7): модель,
// reasoning_effort, response_format при jsonMode=response_format, streaming.
// systemPrompt/userPrompt должны быть собраны заранее под тем же p.mu.
func (p *ChatProvider) buildChatRequestUnderLock(model, systemPrompt, userPrompt string, stream bool) openai.ChatCompletionRequest {
	chatReq := openai.ChatCompletionRequest{
		Model: model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
			{Role: openai.ChatMessageRoleUser, Content: userPrompt},
		},
		Temperature: 0.3,
		MaxTokens:   p.maxTokens,
		N:           1,
		Stream:      stream,
	}
	if p.reasoningEffort != "" {
		chatReq.ReasoningEffort = p.reasoningEffort
	}
	if p.jsonMode == JSONModeResponseFormat {
		chatReq.ResponseFormat = &openai.ChatCompletionResponseFormat{Type: openai.ChatCompletionResponseFormatTypeJSONObject}
	}
	return chatReq
}

// pendingRetryAfter возвращает оставшееся время действия Retry-After
// последнего 429-ответа (0 — нет данных).
func (p *ChatProvider) pendingRetryAfter() time.Duration {
	p.retryMu.Lock()
	defer p.retryMu.Unlock()
	d := time.Until(p.retryAfterUntil)
	if d < 0 {
		return 0
	}
	return d
}

// buildSystemPromptUnderLock собирает system-сообщение, используя уже
// захваченный p.mu (обычный field read без повторного lock — иначе deadlock).
// Формат ответа раскрывается согласно p.jsonMode (F8).
func (p *ChatProvider) buildSystemPromptUnderLock(candidateContext, sourceLang, targetLang string) string {
	return buildSystemPromptWithMode(p.systemTemplate, candidateContext, sourceLang, targetLang, p.jsonMode)
}

// buildSystemPrompt собирает system-промпт: правила формата подсказки
// (BuildSystemPrompt) всегда идут первыми. Candidate context добавляется
// отдельной секцией для персонализации — он НЕ затирает правила формата.
// Раньше непустой cvContext полностью заменял правила, из-за чего модель
// отвечала без «SRC: … | TGT: …» и parseAnswerHints отбрасывал всё.
// buildSystemPromptWith собирает system-сообщение из заданного шаблона
// (дефолтный JSON-режим формата).
func buildSystemPromptWith(tpl *SystemPromptTemplate, candidateContext, sourceLang, targetLang string) string {
	return buildSystemPromptWithMode(tpl, candidateContext, sourceLang, targetLang, JSONModePrompt)
}

// buildSystemPromptWithMode — то же с явным режимом формата (F8):
// jsonMode определяет раскрытие {{RESPONSE_FORMAT}} в шаблоне.
func buildSystemPromptWithMode(tpl *SystemPromptTemplate, candidateContext, sourceLang, targetLang, jsonMode string) string {
	prompt := tpl.RenderFormat(sourceLang, targetLang, jsonMode)
	if candidateContext != "" {
		// RenderFormat гарантирует один завершающий "\n"; убираем его, чтобы
		// между правилами и candidate context была ровно одна пустая строка.
		prompt = strings.TrimRight(prompt, "\n") + "\n\nCandidate context:\n" + candidateContext
	}
	return prompt
}

func (p *ChatProvider) GenerateAnswers(ctx context.Context, req AnswerRequest) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.systemTemplate == nil {
		return nil, fmt.Errorf("translator: шаблон system-промпта не задан (SYSTEM_PROMPT_FILE)")
	}

	userPrompt := BuildAnswerPrompt(req)

	systemPrompt := p.buildSystemPromptUnderLock(req.CandidateContext, req.SourceLang, req.TargetLang)

	// Fallback-цепочка (F7): основная модель, затем LLM_FALLBACK_MODELS.
	// Каждый шаг цепочки — полный retry-цикл по 429; любой неустранимый сбой
	// (включая несуществующую модель) переводит на следующую модель.
	models := make([]string, 0, 1+len(p.fallbackModels))
	models = append(models, p.model)
	models = append(models, p.fallbackModels...)

	var lastErr error
	for i, model := range models {
		chatReq := p.buildChatRequestUnderLock(model, systemPrompt, userPrompt, false)

		resp, err := p.createChatCompletionWithRetry(ctx, chatReq)
		if err == nil {
			if len(resp.Choices) == 0 {
				return nil, fmt.Errorf("generate answers: no choices in response")
			}
			hints := ParseAnswers(resp.Choices[0].Message.Content, req.SourceLang, req.TargetLang)
			if len(hints) == 0 {
				slog.Warn("translator: LLM вернул пустые подсказки", "question", req.Question, "model", model, "raw", resp.Choices[0].Message.Content)
			}
			return hints, nil
		}
		lastErr = fmt.Errorf("модель %s: %w", model, err)
		if ctx.Err() != nil {
			// Отмена/таймаут — fallback-модели не пробуем, контекст мёртв.
			return nil, fmt.Errorf("generate answers: %w", err)
		}
		if i < len(models)-1 {
			slog.Warn("translator: модель недоступна, переход на fallback", "model", model, "err", err, "next", models[i+1])
		}
	}
	return nil, fmt.Errorf("generate answers: %w", lastErr)
}

func (p *ChatProvider) GenerateAnswersStream(ctx context.Context, req AnswerRequest) (<-chan string, error) {
	tokenCh := make(chan string, 64)
	userPrompt := BuildAnswerPrompt(req)

	p.mu.Lock()
	hasTemplate := p.systemTemplate != nil
	systemPrompt := p.buildSystemPromptUnderLock(req.CandidateContext, req.SourceLang, req.TargetLang)
	chatReq := p.buildChatRequestUnderLock(p.model, systemPrompt, userPrompt, true)
	p.mu.Unlock()

	if !hasTemplate {
		return nil, fmt.Errorf("translator: шаблон system-промпта не задан (SYSTEM_PROMPT_FILE)")
	}

	stream, err := p.client.CreateChatCompletionStream(ctx, chatReq)
	if err != nil {
		close(tokenCh)
		return nil, fmt.Errorf("generate answers stream: %w", err)
	}

	go func() {
		defer close(tokenCh)
		defer stream.Close()
		for {
			response, recvErr := stream.Recv()
			if recvErr != nil {
				if recvErr.Error() == "EOF" {
					break
				}
				if ctx.Err() != nil {
					return
				}
				select {
				case tokenCh <- "[ERROR: " + recvErr.Error() + "]":
				case <-ctx.Done():
				}
				return
			}
			if len(response.Choices) > 0 {
				delta := response.Choices[0].Delta.Content
				if delta != "" {
					select {
					case tokenCh <- delta:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()

	return tokenCh, nil
}

func (p *ChatProvider) createChatCompletionWithRetry(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		resp, err := p.client.CreateChatCompletion(ctx, req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !isRateLimitError(err) {
			return openai.ChatCompletionResponse{}, err
		}
		// Backoff (F7): экспоненциальный + Retry-After последнего 429
		// (что больше) + jitter — рассинхронизация параллельных ретраев.
		backoff := time.Duration(1<<uint(attempt)) * time.Second
		if ra := p.pendingRetryAfter(); ra > backoff {
			backoff = ra
		}
		backoff += time.Duration(rand.Int63n(int64(jitterMax)))
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return openai.ChatCompletionResponse{}, fmt.Errorf("retry cancelled: %w", ctx.Err())
		case <-timer.C:
		}
	}
	return openai.ChatCompletionResponse{}, fmt.Errorf("max retries exceeded: %w", lastErr)
}

// jitterMax — верхняя граница случайной добавки к backoff (рассинхронизация
// параллельных воркеров после массового 429).
const jitterMax = 500 * time.Millisecond

// isRateLimitError распознаёт 429: точное совпадение по APIError
// HTTPStatusCode (go-openai), строковый fallback — для обёрнутых/прокси ошибок.
func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) && apiErr.HTTPStatusCode == http.StatusTooManyRequests {
		return true
	}
	errStr := err.Error()
	return strings.Contains(errStr, "429") ||
		strings.Contains(errStr, "rate limit") ||
		strings.Contains(errStr, "too many requests")
}

// maxAnswerHints ограничивает число возвращаемых подсказок.
// Защита от «мусорных» ответов reasoning-моделей, которые генерируют
// десятки строк вместо одной.
const maxAnswerHints = 3

// jsonAnswerPayload — схема JSON-вывода (F7):
// {"answers":[{"source":"…","target":"…"}]} — 1–3 варианта, лучший первым.
// "source" — простой английский ответ, "target" — перевод на целевой язык.
type jsonAnswerPayload struct {
	Answers []struct {
		Source string `json:"source"`
		Target string `json:"target"`
	} `json:"answers"`
}

// ParseAnswers разбирает сырой ответ LLM в список подсказок (F7, единая точка
// парсинга для провайдера и диспетчера): сначала JSON-схема
// {"answers":[…]}, при неудаче — строковый парсер «SRC: … | TGT: …»
// (parseAnswerHints). Результат — строки формата «<SRC>: … | <TGT>: …»,
// которые дальше разбирает ParseAnswer.
func ParseAnswers(raw, srcLang, tgtLang string) []string {
	raw = stripThinking(raw)
	if hints := parseAnswerJSON(raw, srcLang, tgtLang); len(hints) > 0 {
		return hints
	}
	return parseAnswerHints(raw, srcLang, tgtLang)
}

// streamUIInterval — минимальный интервал между обновлениями частичного
// ответа в zone 4 (F7/C6): троттлинг, чтобы GioUI не перерисовывался на
// каждом токене. Экспортируется: dispatcher использует тот же интервал.
const StreamUIInterval = 150 * time.Millisecond

// PartialAnswerDisplay извлекает из накопленного потока токенов читаемый
// частичный текст для zone 4 (F7/C6): в JSON-режиме (буфер начинается с «{»)
// это текущее значение поля "source" (инкрементально, обрыв в любой точке),
// иначе — сырой буфер без thinking-блоков. Пустой результат — показывать
// нечего (JSON-скелет ещё не дошёл до source).
func PartialAnswerDisplay(buf string) string {
	trimmed := strings.TrimSpace(buf)
	if strings.HasPrefix(trimmed, "{") {
		return extractJSONSourcePartial(trimmed)
	}
	return strings.TrimSpace(stripThinking(trimmed))
}

// extractJSONSourcePartial читает частичное строковое значение поля "source"
// из недописанного JSON: возвращает прочитанную часть значения даже если
// закрывающей кавычки ещё нет. Ключ не найден/значение не началось — "".
func extractJSONSourcePartial(buf string) string {
	const srcKey = `"source"`
	key := strings.Index(buf, srcKey)
	if key == -1 {
		return ""
	}
	rest := buf[key+len(srcKey):]
	rest = strings.TrimLeft(rest, " \t\r\n:")
	if !strings.HasPrefix(rest, `"`) {
		return ""
	}
	rest = rest[1:]
	var sb strings.Builder
	for i := 0; i < len(rest); i++ {
		switch c := rest[i]; c {
		case '\\':
			if i+1 >= len(rest) {
				// Обрыв посреди escape-последовательности — показываем, что уже собрали.
				return sb.String()
			}
			switch rest[i+1] {
			case 'n', 't', 'r':
				sb.WriteByte(' ')
			case '"', '\\', '/':
				sb.WriteByte(rest[i+1])
			case 'u':
				// \uXXXX: декодируем 4 hex в руну; на обрыве — то, что есть.
				// i += 4: ещё 4 символа hex съест общий i++ в ветке case '\\' и
				// инкремент цикла.
				if i+6 <= len(rest) {
					var r rune
					valid := true
					for _, h := range rest[i+2 : i+6] {
						n, err := strconv.ParseInt(string(h), 16, 32)
						if err != nil {
							valid = false
							break
						}
						r = r*16 + rune(n)
					}
					if !valid {
						return sb.String()
					}
					sb.WriteRune(r)
					i += 4
				} else {
					return sb.String()
				}
			default:
				sb.WriteByte(rest[i+1])
			}
			i++
		case '"':
			return sb.String()
		default:
			sb.WriteByte(c)
		}
	}
	return sb.String()
}

// parseAnswerJSON разбирает JSON-ответ модели (F7): объект с массивом
// answers, каждый элемент — {source, target}. Markdown-ограждения ```json
// срезаются. Возвращает строки «<SRC>: <source> | <TGT>: <target>»
// (без target — строка source целиком: ParseAnswer сохранит её как Source).
// Не-JSON/пустой source — nil (вызывающий код падает на строковый парсер).
func parseAnswerJSON(raw, srcLang, tgtLang string) []string {
	trimmed := strings.TrimSpace(raw)
	// Убираем markdown-ограждение, если модель его добавила.
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, "```"))
	if !strings.HasPrefix(trimmed, "{") {
		return nil
	}

	var payload jsonAnswerPayload
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return nil
	}
	srcTag, tgtTag := langTags(srcLang, tgtLang)

	hints := make([]string, 0, len(payload.Answers))
	for _, a := range payload.Answers {
		source := strings.TrimSpace(a.Source)
		if source == "" {
			continue
		}
		target := strings.TrimSpace(a.Target)
		if target == "" {
			hints = append(hints, source)
			continue
		}
		hints = append(hints, fmt.Sprintf("%s: %s | %s: %s", srcTag, source, tgtTag, target))
	}
	if len(hints) > maxAnswerHints {
		hints = hints[:maxAnswerHints]
	}
	return hints
}

// parseAnswerHints разбирает сырой ответ LLM и оставляет строки-подсказки в
// формате «<SRC>: … | <TGT>: …» для настроенной пары языков. Пустые теги
// трактуются как дефолт en/ru.
func parseAnswerHints(raw, srcLang, tgtLang string) []string {
	srcTag, tgtTag := langTags(srcLang, tgtLang)

	// Удаляем reasoning-блок Groq (<think>…</think>) целиком. Внутри него
	// строки могут содержать «EN:», «RU:» и «|» (модель рассуждает о формате),
	// поэтому построчная фильтрация по формату не справится — блок вырезаем.
	raw = stripThinking(raw)

	lines := strings.Split(raw, "\n")
	var hints []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		clean := stripBulletPrefix(trimmed)
		if clean == "" {
			continue
		}
		// Отбрасываем reasoning-мусор (chain-of-thought): оставляем только
		// строки в формате подсказки «SRC: <source> | TGT: <target>».
		if !isHintLine(clean, srcTag, tgtTag) {
			continue
		}
		hints = append(hints, clean)
	}
	// Ограничиваем количество подсказок (защита от «83 подсказок»-мусора).
	if len(hints) > maxAnswerHints {
		hints = hints[:maxAnswerHints]
	}
	// Fallback: если строгая фильтрация отбросила все строки, а сырой ответ
	// непустой — вернуть его целиком как единственную подсказку (Source без
	// перевода), чтобы валидный ответ не терялся из-за нестрогого формата LLM.
	if len(hints) == 0 {
		if t := strings.TrimSpace(raw); t != "" {
			return []string{t}
		}
	}
	return hints
}

// langTags возвращает верхнерегистровые теги языков для формата подсказки.
func langTags(srcLang, tgtLang string) (srcTag, tgtTag string) {
	if srcLang == "" {
		srcLang = DefaultSourceLang
	}
	if tgtLang == "" {
		tgtLang = DefaultTargetLang
	}
	return strings.ToUpper(srcLang), strings.ToUpper(tgtLang)
}

// stripThinking удаляет из ответа все блоки <think>…</think>. Groq для
// qwen3.6-27b кладёт chain-of-thought прямо в content именно в таких тегах
// (поля reasoning/reasoning_content при этом пустые). Удаляем блоки целиком,
// так как отдельные строки внутри могут выглядеть как валидные подсказки.
func stripThinking(s string) string {
	for {
		start := strings.Index(s, "<think>")
		if start == -1 {
			return s
		}
		end := strings.Index(s[start:], "</think>")
		if end == -1 {
			// Нет закрывающего тега — вырезаем всё до конца строки.
			return s[:start]
		}
		end += start + len("</think>")
		s = s[:start] + s[end:]
	}
}

// isHintLine определяет, соответствует ли строка формату подсказки
// «<SRC>: <source> | <TGT>: <target>» для заданных тегов языков.
// Reasoning-строки (chain-of-thought) такого формата не содержат и отбрасываются.
func isHintLine(line, srcTag, tgtTag string) bool {
	// Groq для qwen3.6-27b кладёт reasoning (chain-of-thought) прямо в
	// content внутри тегов <think>…</think>. Отбрасываем такие строки явно —
	// они точно не являются подсказкой, даже если случайно содержат «|».
	if strings.Contains(line, "<think>") || strings.Contains(line, "</think>") {
		return false
	}
	if !strings.Contains(line, "|") {
		return false
	}
	upper := strings.ToUpper(line)
	return strings.Contains(upper, srcTag+":") &&
		strings.Contains(upper, tgtTag+":")
}

func stripBulletPrefix(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			continue
		}
		if i > 0 && (s[i] == '.' || s[i] == ')') {
			if i+1 < len(s) && s[i+1] == ' ' {
				return strings.TrimSpace(s[i+2:])
			}
			return ""
		}
		break
	}
	if len(s) >= 2 && s[1] == ' ' {
		switch s[0] {
		case '-', '*':
			return strings.TrimSpace(s[2:])
		}
	}
	if len(s) == 1 && (s[0] == '-' || s[0] == '*') {
		return ""
	}
	if strings.HasPrefix(s, "• ") {
		return strings.TrimSpace(s[3:])
	}
	if s == "•" {
		return ""
	}
	return s
}

var _ LLMProvider = (*ChatProvider)(nil)
var _ StreamingAnswersProvider = (*ChatProvider)(nil)
