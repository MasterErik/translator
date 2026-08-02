// Package common provides shared types, events, and configuration
// used across all internal packages of the Translator application.
package common

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

// Config holds all configuration parameters for the Translator application.
// API keys are read from environment variables; non-secret parameters
// can be loaded from a YAML config file or set via environment variables.
type Config struct {
	// GladiaAPIKey is the API key for Gladia STT + Translation service.
	// Read from GLADIA_API_KEY environment variable.
	GladiaAPIKey string

	// OpenAIAPIKey is the API key for OpenAI LLM service.
	// Read from OPENAI_API_KEY environment variable.
	OpenAIAPIKey string

	// LLMBaseURL is the base URL for the OpenAI-compatible API.
	// Supports any OpenAI-compatible provider (OpenAI, Z.AI GLM, etc.).
	// Read from LLM_BASE_URL environment variable. Default: "https://api.openai.com/v1".
	LLMBaseURL string `yaml:"llm_base_url"`

	// LLMAPIKey is the API key for the LLM service.
	// Read from LLM_API_KEY environment variable. Falls back to OPENAI_API_KEY.
	LLMAPIKey string

	// LLMModel specifies the LLM model to use for translation and answer generation.
	// Default: "llama-3.3-70b-versatile".
	LLMModel string `yaml:"llm_model"`

	// SourceLang is the source language code for Gladia STT (ISO 639-1).
	// Default: "en". Read from SOURCE_LANG environment variable.
	SourceLang string `yaml:"source_lang"`

	// TargetLang is the language code for Gladia translation (ISO 639-1).
	// Default: "ru".
	TargetLang string `yaml:"target_lang"`

	// TargetLanguage is the language to translate into (ISO 639-1 code).
	// Default: "ru".
	TargetLanguage string `yaml:"target_language"`

	// LogDir is the directory where session logs and audio chunks are stored.
	// Default: "./logs".
	LogDir string `yaml:"log_dir"`

	// AudioSampleRate is the sample rate in Hz for audio capture.
	// Default: 16000.
	AudioSampleRate int `yaml:"audio_sample_rate"`

	// AudioChannels is the number of audio channels (1 = mono).
	// Default: 1.
	AudioChannels int `yaml:"audio_channels"`

	// WindowSize is the number of recent utterances kept in the sliding translation window.
	// Default: 5.
	WindowSize int `yaml:"window_size"`

	// CVContext is the CV/resume context for answer generation.
	// Can be set via CV_CONTEXT environment variable or YAML config.
	CVContext string `yaml:"cv_context"`

	// LoopbackDeviceName is the name of the WASAPI loopback device (e.g. "CABLE Output").
	// If empty, the default playback device is used. Read from LOOPBACK_DEVICE env or YAML.
	LoopbackDeviceName string `yaml:"loopback_device"`

	// MicDeviceName is the name of the microphone device.
	// If empty, the default capture device is used. Read from MIC_DEVICE env or YAML.
	MicDeviceName string `yaml:"mic_device"`

	// SaveAudio enables saving raw PCM audio chunks to disk (audio/ directory).
	// Default: false. Read from SAVE_AUDIO env or YAML (true/1/yes → enabled).
	// Приоритет: env > .env > yaml.
	SaveAudio bool `yaml:"save_audio"`

	// MaxTokens limits the maximum number of output tokens for LLM requests.
	// 0 means provider default (no limit). Read from LLM_MAX_TOKENS env.
	MaxTokens int

	// OverlayWidth is the overlay window width in pixels. Default: 800.
	// Read from OVERLAY_WIDTH env.
	OverlayWidth int

	// OverlayHeight is the overlay window height in pixels. Default: 650.
	// Read from OVERLAY_HEIGHT env.
	OverlayHeight int

	// LogLevel sets the minimum log level for slog (debug, info, warn, error).
	// Default: "info". Read from LOG_LEVEL environment variable.
	LogLevel string
}

// applyDefaults sets reasonable default values for any unconfigured fields.
func (c *Config) applyDefaults() {
	if c.SourceLang == "" {
		c.SourceLang = "en"
	}
	if c.TargetLang == "" {
		c.TargetLang = "ru"
	}
	if c.TargetLanguage == "" {
		c.TargetLanguage = "ru"
	}
	if c.LogDir == "" {
		c.LogDir = "./logs"
	}
	if c.AudioSampleRate == 0 {
		c.AudioSampleRate = 16000
	}
	if c.AudioChannels == 0 {
		c.AudioChannels = 1
	}
	if c.WindowSize == 0 {
		c.WindowSize = 5
	}
	if c.OverlayWidth == 0 {
		c.OverlayWidth = 800
	}
	if c.OverlayHeight == 0 {
		c.OverlayHeight = 650
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
}

// loadFromEnv reads API keys and overridable settings from environment variables.
// Also loads .env file from project root if it exists.
func (c *Config) loadFromEnv() {
	// Try to load .env file from common locations.
	_ = godotenv.Load()                           // current dir
	_ = godotenv.Load(filepath.Join(".", ".env")) // explicit

	if v := os.Getenv("GLADIA_API_KEY"); v != "" {
		c.GladiaAPIKey = v
	}
	if v := os.Getenv("OPENAI_API_KEY"); v != "" {
		c.OpenAIAPIKey = v
	}
	if v := os.Getenv("LLM_BASE_URL"); v != "" {
		c.LLMBaseURL = v
	}
	if v := os.Getenv("LLM_API_KEY"); v != "" {
		c.LLMAPIKey = v
	}
	// Fallback: LLM_API_KEY → OPENAI_API_KEY.
	if c.LLMAPIKey == "" {
		c.LLMAPIKey = c.OpenAIAPIKey
	}
	if v := os.Getenv("LLM_MODEL"); v != "" {
		c.LLMModel = v
	}
	if v := os.Getenv("TARGET_LANG"); v != "" {
		c.TargetLang = v
	}
	if v := os.Getenv("TARGET_LANGUAGE"); v != "" {
		c.TargetLanguage = v
	}
	if v := os.Getenv("SOURCE_LANG"); v != "" {
		c.SourceLang = v
	}
	if v := os.Getenv("LOG_DIR"); v != "" {
		c.LogDir = v
	}
	if v := os.Getenv("AUDIO_SAMPLE_RATE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.AudioSampleRate = n
		}
	}
	if v := os.Getenv("AUDIO_CHANNELS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.AudioChannels = n
		}
	}
	if v := os.Getenv("WINDOW_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.WindowSize = n
		}
	}
	if v := os.Getenv("CV_CONTEXT"); v != "" {
		c.CVContext = v
	}
	if v := os.Getenv("LOOPBACK_DEVICE"); v != "" {
		c.LoopbackDeviceName = v
	}
	if v := os.Getenv("MIC_DEVICE"); v != "" {
		c.MicDeviceName = v
	}
	if v := os.Getenv("SAVE_AUDIO"); v != "" {
		c.SaveAudio = isTruthy(v)
	}
	if v := os.Getenv("LLM_MAX_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.MaxTokens = n
		}
	}
	if v := os.Getenv("OVERLAY_WIDTH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.OverlayWidth = n
		}
	}
	if v := os.Getenv("OVERLAY_HEIGHT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.OverlayHeight = n
		}
	}
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		c.LogLevel = strings.ToLower(v)
	}
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

// isTruthy returns true for typical boolean-like true values.
func isTruthy(v string) bool {
	switch v {
	case "true", "1", "yes", "TRUE", "YES", "True", "Yes":
		return true
	default:
		return false
	}
}

// LoadConfig creates a Config with sensible defaults, then reads API keys
// and overrides from environment variables. This is the simplest way to
// obtain a working configuration without a YAML file.
//
// Example:
//
//	cfg := common.LoadConfig()
//	fmt.Println(cfg.LLMModel) // "llama-3.3-70b-versatile" unless overridden
func LoadConfig() *Config {
	cfg := &Config{}
	cfg.applyDefaults()
	cfg.loadFromEnv()
	return cfg
}

// LoadConfigFromYAML reads a YAML configuration file, applies defaults for
// any missing fields, and then overrides with environment variables.
// Environment variables always take precedence over YAML values.
//
// Example:
//
//	cfg, err := common.LoadConfigFromYAML("config.yaml")
//	if err != nil {
//	    log.Fatal(err)
//	}
func LoadConfigFromYAML(path string) (*Config, error) {
	cfg := &Config{}
	cfg.applyDefaults()

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
