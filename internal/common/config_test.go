package common

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigDefaults(t *testing.T) {
	// Clear relevant env vars to isolate defaults.
	for _, v := range []string{
		"DEEPGRAM_API_KEY", "OPENAI_API_KEY", "OPENAI_MODEL",
		"DEEPGRAM_MODEL", "TARGET_LANGUAGE", "LOG_DIR",
		"AUDIO_SAMPLE_RATE", "AUDIO_CHANNELS", "WINDOW_SIZE", "CV_CONTEXT",
	} {
		os.Unsetenv(v)
	}

	cfg := LoadConfig()

	if cfg.OpenAIModel != "gpt-4o-mini" {
		t.Errorf("OpenAIModel default: got %q, want %q", cfg.OpenAIModel, "gpt-4o-mini")
	}
	if cfg.DeepgramModel != "nova-2" {
		t.Errorf("DeepgramModel default: got %q, want %q", cfg.DeepgramModel, "nova-2")
	}
	if cfg.TargetLanguage != "ru" {
		t.Errorf("TargetLanguage default: got %q, want %q", cfg.TargetLanguage, "ru")
	}
	if cfg.LogDir != "./logs" {
		t.Errorf("LogDir default: got %q, want %q", cfg.LogDir, "./logs")
	}
	if cfg.AudioSampleRate != 16000 {
		t.Errorf("AudioSampleRate default: got %d, want %d", cfg.AudioSampleRate, 16000)
	}
	if cfg.AudioChannels != 1 {
		t.Errorf("AudioChannels default: got %d, want %d", cfg.AudioChannels, 1)
	}
	if cfg.WindowSize != 5 {
		t.Errorf("WindowSize default: got %d, want %d", cfg.WindowSize, 5)
	}
}

func TestLoadConfigEnvOverride(t *testing.T) {
	// Clear relevant env vars first.
	for _, v := range []string{
		"DEEPGRAM_API_KEY", "OPENAI_API_KEY", "OPENAI_MODEL",
		"DEEPGRAM_MODEL", "TARGET_LANGUAGE", "LOG_DIR",
		"AUDIO_SAMPLE_RATE", "AUDIO_CHANNELS", "WINDOW_SIZE", "CV_CONTEXT",
	} {
		os.Unsetenv(v)
	}

	os.Setenv("OPENAI_MODEL", "gpt-4")
	os.Setenv("TARGET_LANGUAGE", "fr")
	os.Setenv("AUDIO_SAMPLE_RATE", "48000")
	os.Setenv("CV_CONTEXT", "Senior Go Developer")

	cfg := LoadConfig()

	if cfg.OpenAIModel != "gpt-4" {
		t.Errorf("OpenAIModel env override: got %q, want %q", cfg.OpenAIModel, "gpt-4")
	}
	if cfg.TargetLanguage != "fr" {
		t.Errorf("TargetLanguage env override: got %q, want %q", cfg.TargetLanguage, "fr")
	}
	if cfg.AudioSampleRate != 48000 {
		t.Errorf("AudioSampleRate env override: got %d, want %d", cfg.AudioSampleRate, 48000)
	}
	if cfg.CVContext != "Senior Go Developer" {
		t.Errorf("CVContext env override: got %q, want %q", cfg.CVContext, "Senior Go Developer")
	}
	// Defaults should still apply for unset fields.
	if cfg.DeepgramModel != "nova-2" {
		t.Errorf("DeepgramModel should be default: got %q", cfg.DeepgramModel)
	}
}

func TestLoadConfigFromYAML(t *testing.T) {
	// Clear relevant env vars.
	for _, v := range []string{
		"DEEPGRAM_API_KEY", "OPENAI_API_KEY", "OPENAI_MODEL",
		"DEEPGRAM_MODEL", "TARGET_LANGUAGE", "LOG_DIR",
		"AUDIO_SAMPLE_RATE", "AUDIO_CHANNELS", "WINDOW_SIZE", "CV_CONTEXT",
	} {
		os.Unsetenv(v)
	}

	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "config.yaml")
	content := `
openai_model: "gpt-4"
target_language: "de"
window_size: 10
cv_context: "Backend Engineer"
`
	if err := os.WriteFile(yamlPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfigFromYAML(yamlPath)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.OpenAIModel != "gpt-4" {
		t.Errorf("OpenAIModel from YAML: got %q, want %q", cfg.OpenAIModel, "gpt-4")
	}
	if cfg.TargetLanguage != "de" {
		t.Errorf("TargetLanguage from YAML: got %q, want %q", cfg.TargetLanguage, "de")
	}
	if cfg.WindowSize != 10 {
		t.Errorf("WindowSize from YAML: got %d, want %d", cfg.WindowSize, 10)
	}
	if cfg.CVContext != "Backend Engineer" {
		t.Errorf("CVContext from YAML: got %q, want %q", cfg.CVContext, "Backend Engineer")
	}
	// Defaults should still apply for unspecified fields.
	if cfg.DeepgramModel != "nova-2" {
		t.Errorf("DeepgramModel should be default: got %q", cfg.DeepgramModel)
	}
}

func TestLoadConfigFromYAMLEnvOverride(t *testing.T) {
	for _, v := range []string{
		"DEEPGRAM_API_KEY", "OPENAI_API_KEY", "OPENAI_MODEL",
		"DEEPGRAM_MODEL", "TARGET_LANGUAGE", "LOG_DIR",
		"AUDIO_SAMPLE_RATE", "AUDIO_CHANNELS", "WINDOW_SIZE", "CV_CONTEXT",
	} {
		os.Unsetenv(v)
	}

	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "config.yaml")
	content := `
openai_model: "gpt-4"
target_language: "de"
`
	if err := os.WriteFile(yamlPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Env vars should override YAML.
	os.Setenv("OPENAI_MODEL", "gpt-4o")
	os.Setenv("TARGET_LANGUAGE", "es")

	cfg, err := LoadConfigFromYAML(yamlPath)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.OpenAIModel != "gpt-4o" {
		t.Errorf("Env should override YAML: got %q, want %q", cfg.OpenAIModel, "gpt-4o")
	}
	if cfg.TargetLanguage != "es" {
		t.Errorf("Env should override YAML: got %q, want %q", cfg.TargetLanguage, "es")
	}
}

func TestLoadConfigFromYAMLMissingFile(t *testing.T) {
	_, err := LoadConfigFromYAML("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}
