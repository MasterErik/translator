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
	if cfg.DeepgramModel != "flux-general-en" {
		t.Errorf("DeepgramModel default: got %q, want %q", cfg.DeepgramModel, "flux-general-en")
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
	if cfg.DeepgramModel != "flux-general-en" {
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
	if cfg.DeepgramModel != "flux-general-en" {
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

// TestSaveAudioDefault проверяет, что по умолчанию SaveAudio == false.
func TestSaveAudioDefault(t *testing.T) {
	os.Unsetenv("SAVE_AUDIO")

	cfg := LoadConfig()

	if cfg.SaveAudio != false {
		t.Errorf("SaveAudio default: got %v, want false", cfg.SaveAudio)
	}
}

// TestSaveAudioEnvTrue проверяет, что SAVE_AUDIO=true включает сохранение.
func TestSaveAudioEnvTrue(t *testing.T) {
	tests := []string{"true", "1", "yes", "TRUE", "YES"}

	for _, v := range tests {
		t.Run(v, func(t *testing.T) {
			os.Setenv("SAVE_AUDIO", v)
			t.Cleanup(func() { os.Unsetenv("SAVE_AUDIO") })

			cfg := LoadConfig()

			if cfg.SaveAudio != true {
				t.Errorf("SAVE_AUDIO=%q: got %v, want true", v, cfg.SaveAudio)
			}
		})
	}
}

// TestSaveAudioEnvFalse проверяет, что не-true значения → false.
func TestSaveAudioEnvFalse(t *testing.T) {
	tests := []string{"false", "0", "no", "", "anything"}

	for _, v := range tests {
		t.Run(v, func(t *testing.T) {
			os.Setenv("SAVE_AUDIO", v)
			t.Cleanup(func() { os.Unsetenv("SAVE_AUDIO") })

			cfg := LoadConfig()

			if cfg.SaveAudio != false {
				t.Errorf("SAVE_AUDIO=%q: got %v, want false", v, cfg.SaveAudio)
			}
		})
	}
}

// TestSaveAudioYAML проверяет чтение save_audio из YAML-конфига.
func TestSaveAudioYAML(t *testing.T) {
	os.Unsetenv("SAVE_AUDIO")

	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "config.yaml")

	// save_audio: true в YAML.
	content := `
save_audio: true
openai_model: "gpt-4"
`
	if err := os.WriteFile(yamlPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfigFromYAML(yamlPath)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.SaveAudio != true {
		t.Errorf("SaveAudio from YAML: got %v, want true", cfg.SaveAudio)
	}

	// Env должен переопределять YAML.
	os.Setenv("SAVE_AUDIO", "false")
	cfg, err = LoadConfigFromYAML(yamlPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SaveAudio != false {
		t.Errorf("SAVE_AUDIO env should override YAML true: got %v, want false", cfg.SaveAudio)
	}
}

// TestLLMBaseURLDefault проверяет значение по умолчанию для LLMBaseURL.
func TestLLMBaseURLDefault(t *testing.T) {
	os.Unsetenv("LLM_BASE_URL")

	cfg := LoadConfig()

	if cfg.LLMBaseURL != "https://api.openai.com/v1" {
		t.Errorf("LLMBaseURL default: got %q, want %q", cfg.LLMBaseURL, "https://api.openai.com/v1")
	}
}

// TestLLMBaseURLEnv проверяет переопределение LLMBaseURL через env.
func TestLLMBaseURLEnv(t *testing.T) {
	os.Setenv("LLM_BASE_URL", "https://api.z.ai/api/paas/v4/")
	t.Cleanup(func() { os.Unsetenv("LLM_BASE_URL") })

	cfg := LoadConfig()

	if cfg.LLMBaseURL != "https://api.z.ai/api/paas/v4/" {
		t.Errorf("LLMBaseURL env override: got %q, want %q", cfg.LLMBaseURL, "https://api.z.ai/api/paas/v4/")
	}
}

// TestLLMAPIKeyFallback проверяет fallback LLM_API_KEY → OPENAI_API_KEY.
func TestLLMAPIKeyFallback(t *testing.T) {
	os.Setenv("OPENAI_API_KEY", "sk-openai-key")
	os.Unsetenv("LLM_API_KEY")
	t.Cleanup(func() {
		os.Unsetenv("OPENAI_API_KEY")
	})

	cfg := LoadConfig()

	if cfg.LLMAPIKey != "sk-openai-key" {
		t.Errorf("LLMAPIKey fallback: got %q, want %q", cfg.LLMAPIKey, "sk-openai-key")
	}
}

// TestLLMAPIKeyOverride проверяет, что LLM_API_KEY имеет приоритет над OPENAI_API_KEY.
func TestLLMAPIKeyOverride(t *testing.T) {
	os.Setenv("OPENAI_API_KEY", "sk-openai-key")
	os.Setenv("LLM_API_KEY", "sk-glm-key")
	t.Cleanup(func() {
		os.Unsetenv("OPENAI_API_KEY")
		os.Unsetenv("LLM_API_KEY")
	})

	cfg := LoadConfig()

	if cfg.LLMAPIKey != "sk-glm-key" {
		t.Errorf("LLMAPIKey override: got %q, want %q", cfg.LLMAPIKey, "sk-glm-key")
	}
	// OpenAIAPIKey всё ещё должно быть sk-openai-key.
	if cfg.OpenAIAPIKey != "sk-openai-key" {
		t.Errorf("OpenAIAPIKey should be unchanged: got %q", cfg.OpenAIAPIKey)
	}
}

// TestLLMBaseURLFromYAML проверяет чтение llm_base_url из YAML.
func TestLLMBaseURLFromYAML(t *testing.T) {
	os.Unsetenv("LLM_BASE_URL")
	os.Unsetenv("OPENAI_MODEL")

	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "config.yaml")
	content := `
llm_base_url: "https://api.z.ai/api/paas/v4/"
openai_model: "glm-4-flash"
`
	if err := os.WriteFile(yamlPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfigFromYAML(yamlPath)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.LLMBaseURL != "https://api.z.ai/api/paas/v4/" {
		t.Errorf("LLMBaseURL from YAML: got %q, want %q", cfg.LLMBaseURL, "https://api.z.ai/api/paas/v4/")
	}
	if cfg.OpenAIModel != "glm-4-flash" {
		t.Errorf("OpenAIModel from YAML: got %q, want %q", cfg.OpenAIModel, "glm-4-flash")
	}
}
