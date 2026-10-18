// Package common provides shared types, events, and configuration
// used across all internal packages of the Translator application.
package common

import (
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

// Config holds all configuration parameters for the Translator application.
// Every field has a yaml tag in UPPER_SNAKE_CASE — the same string is used
// both by yaml.Unmarshal (case-insensitive) and as the env var name.
type Config struct {
	// API keys.
	GladiaAPIKey string `yaml:"GLADIA_API_KEY"`
	LLMAPIKey    string `yaml:"LLM_API_KEY"`

	// LLM settings.
	LLMBaseURL string `yaml:"LLM_BASE_URL"`
	LLMModel   string `yaml:"LLM_MODEL"`
	MaxTokens  int    `yaml:"LLM_MAX_TOKENS"`

	// Languages (ISO 639-1).
	SourceLang string `yaml:"SOURCE_LANG"`
	TargetLang string `yaml:"TARGET_LANG"`

	// Logging.
	LogDir    string `yaml:"LOG_DIR"`
	LogLevel  string `yaml:"LOG_LEVEL"`
	SaveAudio bool   `yaml:"SAVE_AUDIO"`

	// Audio devices.
	LoopbackDeviceName string `yaml:"LOOPBACK_DEVICE"`
	MicDeviceName      string `yaml:"MIC_DEVICE"`

	// Debug — режим ручного ввода текста в overlay (без транскрибатора):
	// окно кликабельно, внизу поле ввода; DEBUG_INPUT=1 включает.
	DebugInput bool `yaml:"DEBUG_INPUT"`

	// Overlay.
	OverlayWidth  int `yaml:"OVERLAY_WIDTH"`
	OverlayHeight int `yaml:"OVERLAY_HEIGHT"`

	// Candidate context file (база CV) — путь к текстовому файлу с фактами кандидата.
	CandidateContextFile string `yaml:"CANDIDATE_CONTEXT_FILE"`

	// Candidate context — fact-level retrieval (каталог с manifest.json + sections/).
	CandidateContext CandidateContextConfig `yaml:"candidate_context"`

	// Interview — параметры интервью (целевой стек для штрафов retrieval).
	Interview InterviewConfig `yaml:"interview"`

	// Hints — подсказки-ответы на предсказуемые вопросы интервьюера
	// (каталог в формате candidate_context).
	Hints HintsConfig `yaml:"hints"`

	// Conversation context — ограничение истории интервью.
	Conversation ConversationConfig `yaml:"conversation"`
}

// ConversationConfig — параметры conversation context (история интервью).
type ConversationConfig struct {
	RecentTurns      int `yaml:"recent_turns"`       // максимум turns в context
	MaxContextTokens int `yaml:"max_context_tokens"` // лимит размера context

	// AutoAnswer — авто-генерация ответа сразу при обнаружении вопроса.
	// nil/отсутствует → true (режим по умолчанию: вопрос → немедленная генерация).
	// false — вопрос только создаёт метку в таймлайне, ответ генерируется по F1.
	AutoAnswer *bool `yaml:"auto_answer"`
}

// CandidateContextConfig — параметры fact-level candidate context retrieval.
type CandidateContextConfig struct {
	Dir              string  `yaml:"dir"`                // каталог candidate_context (manifest.json + sections/)
	MaxTokens        int     `yaml:"max_tokens"`         // бюджет токенов на факты
	MaxProfileTokens int     `yaml:"max_profile_tokens"` // бюджет токенов на профиль
	MinScore         float64 `yaml:"min_score"`          // минимальный score отобранного факта
	TopK             int     `yaml:"top_k"`              // максимум отобранных фактов

	// RelativeThreshold — относительный порог отсечения фактов (доля score
	// лучшего факта). Отрицательное значение (< 0) ВЫКЛЮЧАЕТ порог:
	// applyDefaults срабатывает только на нулевое значение, поэтому «выключено»
	// кодируется именно отрицательным числом, а не нулём (−1 → выключено).
	RelativeThreshold float64 `yaml:"relative_threshold"`

	// MaxMatrixTokens — бюджет токенов на Technical Matrix.
	MaxMatrixTokens int `yaml:"max_matrix_tokens"`

	// AllowLegacyFallback — разрешить загрузку из legacy-файла, если каталог
	// candidate_context не загрузился (D14). По умолчанию false: без явного
	// разрешения ошибка загрузки каталога валит старт.
	AllowLegacyFallback bool `yaml:"allow_legacy_fallback"`

	// OnLoadError — поведение при ошибке загрузки каталога: "fail" (по
	// умолчанию — ошибка валит старт) | "degraded" (работать без контекста,
	// D15 — только по явной настройке).
	OnLoadError string `yaml:"on_load_error"`
}

// InterviewConfig — параметры интервью.
type InterviewConfig struct {
	// TargetStack — целевой стек интервью (например, java, spring).
	// По умолчанию пусто — штрафов за «чужой» стек нет; перед интервью
	// выставляется вручную (D2).
	TargetStack []string `yaml:"target_stack"`
}

// interviewConfigRaw — промежуточная форма для ручного разбора: target_stack
// допускает и YAML-список, и строку с разделителем-запятой.
type interviewConfigRaw struct {
	TargetStack yaml.Node `yaml:"target_stack"`
}

// UnmarshalYAML принимает target_stack как YAML-список (`[java, php]` /
// блочный список) и как строку `"java, php"` — строковая форма нормализуется
// тем же splitList, что и env-переменная INTERVIEW_TARGET_STACK.
func (c *InterviewConfig) UnmarshalYAML(node *yaml.Node) error {
	var raw interviewConfigRaw
	if err := node.Decode(&raw); err != nil {
		return fmt.Errorf("interview: %w", err)
	}

	switch raw.TargetStack.Kind {
	case 0:
		// Поле отсутствует.
	case yaml.ScalarNode:
		c.TargetStack = splitList(raw.TargetStack.Value)
	case yaml.SequenceNode:
		var list []string
		if err := raw.TargetStack.Decode(&list); err != nil {
			return fmt.Errorf("interview.target_stack: %w", err)
		}
		c.TargetStack = normalizeList(list)
	default:
		return fmt.Errorf("interview.target_stack: ожидается список или строка, получено %s", raw.TargetStack.Tag)
	}
	return nil
}

// HintsConfig — параметры подсказок-ответов (готовые ответы на предсказуемые
// вопросы интервьюера; каталог в формате candidate_context).
type HintsConfig struct {
	Dir      string  `yaml:"dir"`       // каталог hints (manifest.json + sections/); "" = выключено
	MinScore float64 `yaml:"min_score"` // строгий порог: score ниже — подсказка не выводится
	TopK     int     `yaml:"top_k"`     // максимум подсказок на вопрос
}

func setDefault[T comparable](v *T, def T) {
	var zero T
	if *v == zero {
		*v = def
	}
}

func (c *Config) applyDefaults() {
	setDefault(&c.SourceLang, "en")
	setDefault(&c.TargetLang, "ru")
	setDefault(&c.LogDir, "./logs")
	setDefault(&c.OverlayWidth, 800)
	setDefault(&c.OverlayHeight, 650)
	setDefault(&c.LogLevel, "info")
	setDefault(&c.Conversation.RecentTurns, 6)
	setDefault(&c.Conversation.MaxContextTokens, 4000)
	setDefault(&c.CandidateContext.MaxTokens, 2000)
	setDefault(&c.CandidateContext.MaxProfileTokens, 150)
	setDefault(&c.CandidateContext.MinScore, 0.0)
	setDefault(&c.CandidateContext.TopK, 5)
	setDefault(&c.CandidateContext.RelativeThreshold, 0.45)
	setDefault(&c.CandidateContext.MaxMatrixTokens, 100)
	setDefault(&c.CandidateContext.OnLoadError, "fail")
	// AllowLegacyFallback намеренно не имеет default: false — нулевое значение
	// и есть нужный дефолт (D14: fallback только по явному разрешению).
	// Interview.TargetStack по умолчанию пуст — штрафов нет (D2).
	setDefault(&c.Hints.MinScore, 0.0)
	setDefault(&c.Hints.TopK, 1)
}

// loadFromEnv loads .env via godotenv, then reads environment variables
// using yaml struct tags directly as env var names.
// Возвращает ошибку только на невалидное значение в ключах, где молчаливое
// игнорирование опасно (CANDIDATE_CONTEXT_ON_LOAD_ERROR) — остальные ключи
// сохраняют прежний тихий стиль (невалидное число игнорируется).
func (c *Config) loadFromEnv() error {
	if err := godotenv.Load(); err != nil {
		slog.Warn("godotenv: .env не загружен", "err", err)
	}

	// Основные поля — через reflection по yaml-тегам.
	_ = loadFromYAMLTags(c)

	// Вложенная секция conversation — env-override вручную
	// (reflection не заходит во вложенные структуры).
	if v := os.Getenv("CONVERSATION_RECENT_TURNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Conversation.RecentTurns = n
		}
	}
	if v := os.Getenv("CONVERSATION_MAX_CONTEXT_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Conversation.MaxContextTokens = n
		}
	}
	// CONVERSATION_AUTO_ANSWER=false — вопросы только копятся как метки,
	// ответ генерируется вручную по F1. Не задан/true — авто-генерация.
	if v := os.Getenv("CONVERSATION_AUTO_ANSWER"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.Conversation.AutoAnswer = &b
		}
	}

	// Вложенная секция candidate_context — env-override вручную
	// (reflection не заходит во вложенные структуры и не поддерживает float64).
	if v := os.Getenv("CANDIDATE_CONTEXT_DIR"); v != "" {
		c.CandidateContext.Dir = v
	}
	if v := os.Getenv("CANDIDATE_CONTEXT_MAX_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.CandidateContext.MaxTokens = n
		}
	}
	if v := os.Getenv("CANDIDATE_CONTEXT_MAX_PROFILE_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.CandidateContext.MaxProfileTokens = n
		}
	}
	if v := os.Getenv("CANDIDATE_CONTEXT_MIN_SCORE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			c.CandidateContext.MinScore = f
		}
	}
	if v := os.Getenv("CANDIDATE_CONTEXT_TOP_K"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.CandidateContext.TopK = n
		}
	}
	if v := os.Getenv("CANDIDATE_CONTEXT_RELATIVE_THRESHOLD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			c.CandidateContext.RelativeThreshold = f
		}
	}
	if v := os.Getenv("CANDIDATE_CONTEXT_MAX_MATRIX_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.CandidateContext.MaxMatrixTokens = n
		}
	}
	if v := os.Getenv("CANDIDATE_CONTEXT_ALLOW_LEGACY_FALLBACK"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.CandidateContext.AllowLegacyFallback = b
		}
	}
	// CANDIDATE_CONTEXT_ON_LOAD_ERROR — единственный ключ секции, где неверное
	// значение не игнорируется: опечатка молча вернула бы fail-режим (или,
	// хуже, создала иллюзию настроенного degraded), поэтому ошибка громкая.
	if v := os.Getenv("CANDIDATE_CONTEXT_ON_LOAD_ERROR"); v != "" {
		switch mode := strings.ToLower(strings.TrimSpace(v)); mode {
		case "fail", "degraded":
			c.CandidateContext.OnLoadError = mode
		default:
			return fmt.Errorf(
				"CANDIDATE_CONTEXT_ON_LOAD_ERROR: недопустимое значение %q (ожидается \"fail\" или \"degraded\")",
				v,
			)
		}
	}

	// Секция interview — env-override вручную (reflection не поддерживает []string).
	// INTERVIEW_TARGET_STACK=java,php → ["java", "php"]; пустые элементы
	// отбрасываются, значения приводятся к нижнему регистру.
	if v := os.Getenv("INTERVIEW_TARGET_STACK"); v != "" {
		c.Interview.TargetStack = splitList(v)
	}

	// Вложенная секция hints — env-override вручную (аналогично candidate_context).
	if v := os.Getenv("HINTS_DIR"); v != "" {
		c.Hints.Dir = v
	}
	if v := os.Getenv("HINTS_MIN_SCORE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			c.Hints.MinScore = f
		}
	}
	if v := os.Getenv("HINTS_TOP_K"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Hints.TopK = n
		}
	}

	return nil
}

// splitList разбивает строку через запятую на нормализованный список:
// элементы обрезаются от пробелов и приводятся к нижнему регистру, пустые
// отбрасываются. Строка из одних запятых/пробелов даёт пустой слайс (nil).
func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		out = append(out, part)
	}
	return normalizeList(out)
}

// normalizeList нормализует элементы: trim, нижний регистр, отбрасывание
// пустых. Возвращает nil, если после нормализации ничего не осталось.
func normalizeList(items []string) []string {
	var out []string
	for _, item := range items {
		item = strings.ToLower(strings.TrimSpace(item))
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

// loadFromYAMLTags reads env vars using yaml struct tags as env var names.
// Only string, int, and bool fields are supported.
// Invalid values are silently ignored (no error returned).
func loadFromYAMLTags(cfg *Config) error {
	v := reflect.ValueOf(cfg).Elem()
	t := v.Type()

	for i := 0; i < t.NumField(); i++ {
		field := v.Field(i)
		envName := t.Field(i).Tag.Get("yaml")
		if envName == "" {
			continue
		}

		value, exists := os.LookupEnv(envName)
		if !exists || value == "" {
			continue
		}

		switch field.Kind() {
		case reflect.String:
			field.SetString(value)

		case reflect.Int:
			n, err := strconv.ParseInt(value, 10, field.Type().Bits())
			if err != nil {
				return fmt.Errorf("%s: %w", envName, err)
			}
			field.SetInt(n)

		case reflect.Bool:
			b, err := strconv.ParseBool(value)
			if err != nil {
				return fmt.Errorf("%s: %w", envName, err)
			}
			field.SetBool(b)
		}
	}

	return nil
}

// SlogLevel converts the LogLevel string to slog.Level.
// Defaults to slog.LevelInfo for unrecognized values.
func (c *Config) SlogLevel() slog.Level {
	switch c.LogLevel {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// LoadConfigFromYAML reads a YAML configuration file, applies defaults for
// any missing fields, and then overrides with environment variables.
// Environment variables always take precedence over YAML values.
func LoadConfigFromYAML(path string) (*Config, error) {
	cfg := &Config{}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file %s: %w", path, err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config file %s: %w", path, err)
	}

	// Re-apply defaults for fields that were explicitly set to zero values in YAML.
	cfg.applyDefaults()

	// Environment variables override YAML values.
	if err := cfg.loadFromEnv(); err != nil {
		return nil, fmt.Errorf("loading config from env: %w", err)
	}

	return cfg, nil
}
