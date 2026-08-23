// Package common provides shared types, events, and configuration
// used across all internal packages of the Translator application.
package common

import (
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"strconv"

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

	// Overlay.
	OverlayWidth  int `yaml:"OVERLAY_WIDTH"`
	OverlayHeight int `yaml:"OVERLAY_HEIGHT"`

	// Candidate context file (база CV) — путь к текстовому файлу с фактами кандидата.
	CandidateContextFile string `yaml:"CANDIDATE_CONTEXT_FILE"`

	// Candidate context — fact-level retrieval (каталог с manifest.json + sections/).
	CandidateContext CandidateContextConfig `yaml:"candidate_context"`

	// Conversation context — ограничение истории интервью.
	Conversation ConversationConfig `yaml:"conversation"`
}

// ConversationConfig — параметры conversation context (история интервью).
type ConversationConfig struct {
	RecentTurns      int `yaml:"recent_turns"`       // максимум turns в context
	MaxContextTokens int `yaml:"max_context_tokens"` // лимит размера context
}

// CandidateContextConfig — параметры fact-level candidate context retrieval.
type CandidateContextConfig struct {
	Dir              string  `yaml:"dir"`                // каталог candidate_context (manifest.json + sections/)
	MaxTokens        int     `yaml:"max_tokens"`         // бюджет токенов на факты
	MaxProfileTokens int     `yaml:"max_profile_tokens"` // бюджет токенов на профиль
	MinScore         float64 `yaml:"min_score"`          // минимальный score отобранного факта
	TopK             int     `yaml:"top_k"`              // максимум отобранных фактов
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
}

// loadFromEnv loads .env via godotenv, then reads environment variables
// using yaml struct tags directly as env var names.
func (c *Config) loadFromEnv() {
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
	cfg.loadFromEnv()

	return cfg, nil
}
