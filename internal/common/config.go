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

	// CV context for answer generation.
	CVContext string `yaml:"CV_CONTEXT"`
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
}

// loadFromEnv loads .env via godotenv, then reads environment variables
// using yaml struct tags directly as env var names.
func (c *Config) loadFromEnv() {
	if err := godotenv.Load(); err != nil {
		slog.Warn("godotenv: .env не загружен", "err", err)
	}

	// Основные поля — через reflection по yaml-тегам.
	_ = loadFromYAMLTags(c)
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
