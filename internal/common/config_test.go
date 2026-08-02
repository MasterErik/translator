package common

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigDefaults(t *testing.T) {
	// Clear relevant env vars to isolate defaults.
	for _, v := range []string{
		"GLADIA_API_KEY", "OPENAI_API_KEY", "LLM_MODEL",
		"TARGET_LANG", "TARGET_LANGUAGE", "LOG_DIR",
		"WINDOW_SIZE", "CV_CONTEXT",
		"LLM_BASE_URL", "LLM_API_KEY",
	} {
		os.Unsetenv(v)
	}

	cfg := LoadConfig()

	if cfg.TargetLang != "ru" {
		t.Errorf("TargetLang default: got %q, want %q", cfg.TargetLang, "ru")
	}
	if cfg.TargetLanguage != "ru" {
		t.Errorf("TargetLanguage default: got %q, want %q", cfg.TargetLanguage, "ru")
	}
	if cfg.LogDir != "./logs" {
		t.Errorf("LogDir default: got %q, want %q", cfg.LogDir, "./logs")
	}
	if cfg.WindowSize != 5 {
		t.Errorf("WindowSize default: got %d, want %d", cfg.WindowSize, 5)
	}
}

func TestLoadConfigEnvOverride(t *testing.T) {
	// Clear relevant env vars first.
	for _, v := range []string{
		"GLADIA_API_KEY", "LLM_MODEL",
		"TARGET_LANG", "SOURCE_LANG", "LOG_DIR",
		"WINDOW_SIZE", "CV_CONTEXT",
		"LLM_BASE_URL", "LLM_API_KEY",
	} {
		os.Unsetenv(v)
	}

	os.Setenv("LLM_MODEL", "llama-3.3-70b-versatile")
	os.Setenv("TARGET_LANGUAGE", "fr")
	os.Setenv("CV_CONTEXT", "Senior Go Developer")

	cfg := LoadConfig()

	if cfg.LLMModel != "llama-3.3-70b-versatile" {
		t.Errorf("LLMModel env override: got %q, want %q", cfg.LLMModel, "llama-3.3-70b-versatile")
	}
	if cfg.TargetLanguage != "fr" {
		t.Errorf("TargetLanguage env override: got %q, want %q", cfg.TargetLanguage, "fr")
	}
	if cfg.CVContext != "Senior Go Developer" {
		t.Errorf("CVContext env override: got %q, want %q", cfg.CVContext, "Senior Go Developer")
	}
	// Defaults should still apply for unset fields.
	if cfg.TargetLang != "ru" {
		t.Errorf("TargetLang should be default: got %q", cfg.TargetLang)
	}
}

func TestLoadConfigFromYAML(t *testing.T) {
	// Clear relevant env vars.
	for _, v := range []string{
		"GLADIA_API_KEY", "OPENAI_API_KEY", "LLM_MODEL",
		"TARGET_LANG", "TARGET_LANGUAGE", "LOG_DIR",
		"WINDOW_SIZE", "CV_CONTEXT",
		"LLM_BASE_URL", "LLM_API_KEY",
	} {
		os.Unsetenv(v)
	}

	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "config.yaml")
	content := `
llm_model: "qwen-2.5-72b"
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

	if cfg.LLMModel != "qwen-2.5-72b" {
		t.Errorf("LLMModel from YAML: got %q, want %q", cfg.LLMModel, "qwen-2.5-72b")
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
	if cfg.TargetLang != "ru" {
		t.Errorf("TargetLang should be default: got %q", cfg.TargetLang)
	}
}

func TestLoadConfigFromYAMLEnvOverride(t *testing.T) {
	for _, v := range []string{
		"GLADIA_API_KEY", "OPENAI_API_KEY", "LLM_MODEL",
		"TARGET_LANG", "TARGET_LANGUAGE", "LOG_DIR",
		"WINDOW_SIZE", "CV_CONTEXT",
		"LLM_BASE_URL", "LLM_API_KEY",
	} {
		os.Unsetenv(v)
	}

	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "config.yaml")
	content := `
llm_model: "qwen-2.5-72b"
target_language: "de"
`
	if err := os.WriteFile(yamlPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Env vars should override YAML.
	os.Setenv("LLM_MODEL", "deepseek-v3")
	os.Setenv("TARGET_LANGUAGE", "es")

	cfg, err := LoadConfigFromYAML(yamlPath)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.LLMModel != "deepseek-v3" {
		t.Errorf("Env should override YAML: got %q, want %q", cfg.LLMModel, "deepseek-v3")
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
llm_model: "llama-3.3-70b-versatile"
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
	os.Setenv("OPENAI_API_KEY", "«redacted:sk-…»")
	os.Unsetenv("LLM_API_KEY")
	t.Cleanup(func() {
		os.Unsetenv("OPENAI_API_KEY")
	})

	cfg := LoadConfig()

	if cfg.LLMAPIKey != "«redacted:sk-…»" {
		t.Errorf("LLMAPIKey fallback: got %q, want %q", cfg.LLMAPIKey, "«redacted:sk-…»")
	}
}

// TestLLMAPIKeyOverride проверяет, что LLM_API_KEY имеет приоритет над OPENAI_API_KEY.
func TestLLMAPIKeyOverride(t *testing.T) {
	os.Setenv("OPENAI_API_KEY", "«redacted:sk-…»")
	os.Setenv("LLM_API_KEY", "sk-glm-key")
	t.Cleanup(func() {
		os.Unsetenv("OPENAI_API_KEY")
		os.Unsetenv("LLM_API_KEY")
	})

	cfg := LoadConfig()

	if cfg.LLMAPIKey != "sk-glm-key" {
		t.Errorf("LLMAPIKey override: got %q, want %q", cfg.LLMAPIKey, "sk-glm-key")
	}
	// OpenAIAPIKey всё ещё должно быть «redacted:sk-…».
	if cfg.OpenAIAPIKey != "«redacted:sk-…»" {
		t.Errorf("OpenAIAPIKey should be unchanged: got %q", cfg.OpenAIAPIKey)
	}
}

// TestLLMBaseURLFromYAML проверяет чтение llm_base_url из YAML.
func TestLLMBaseURLFromYAML(t *testing.T) {
	os.Unsetenv("LLM_BASE_URL")
	os.Unsetenv("LLM_MODEL")

	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "config.yaml")
	content := `
llm_base_url: "https://api.z.ai/api/paas/v4/"
llm_model: "glm-4-flash"
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
	if cfg.LLMModel != "glm-4-flash" {
		t.Errorf("LLMModel from YAML: got %q, want %q", cfg.LLMModel, "glm-4-flash")
	}
}

// TestTargetLangDefault проверяет значение по умолчанию для TargetLang.
func TestTargetLangDefault(t *testing.T) {
	os.Unsetenv("TARGET_LANG")

	cfg := LoadConfig()

	if cfg.TargetLang != "ru" {
		t.Errorf("TargetLang default: got %q, want %q", cfg.TargetLang, "ru")
	}
}

// TestTargetLangEnv проверяет переопределение TargetLang через env.
func TestTargetLangEnv(t *testing.T) {
	os.Setenv("TARGET_LANG", "en")
	t.Cleanup(func() { os.Unsetenv("TARGET_LANG") })

	cfg := LoadConfig()

	if cfg.TargetLang != "en" {
		t.Errorf("TargetLang env override: got %q, want %q", cfg.TargetLang, "en")
	}
}

// TestTargetLangFromYAML проверяет чтение target_lang из YAML.
func TestTargetLangFromYAML(t *testing.T) {
	os.Unsetenv("TARGET_LANG")

	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "config.yaml")
	content := `
target_lang: "fr"
`
	if err := os.WriteFile(yamlPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfigFromYAML(yamlPath)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.TargetLang != "fr" {
		t.Errorf("TargetLang from YAML: got %q, want %q", cfg.TargetLang, "fr")
	}
}

// TestGladiaAPIKeyEnv проверяет чтение GLADIA_API_KEY из env.
func TestGladiaAPIKeyEnv(t *testing.T) {
	os.Setenv("GLADIA_API_KEY", "gladia-test-key")
	t.Cleanup(func() { os.Unsetenv("GLADIA_API_KEY") })

	cfg := LoadConfig()

	if cfg.GladiaAPIKey != "gladia-test-key" {
		t.Errorf("GladiaAPIKey: got %q, want %q", cfg.GladiaAPIKey, "gladia-test-key")
	}
}

// TestApplyDefaults_NumericAndString проверяет стандартные defaults (TargetLang, LogDir, etc.).
// LLMBaseURL/LLMModel НЕ имеют defaults — должны настраиваться через .env/config.
func TestApplyDefaults_NumericAndString(t *testing.T) {
	cfg := Config{}
	cfg.applyDefaults()

	if cfg.TargetLang != "ru" {
		t.Errorf("TargetLang: got %q, want %q", cfg.TargetLang, "ru")
	}
	if cfg.LogDir != "./logs" {
		t.Errorf("LogDir: got %q, want %q", cfg.LogDir, "./logs")
	}
}

// TestApplyDefaults_PreservesExistingValues проверяет, что applyDefaults()
// НЕ перезаписывает уже установленные значения.
func TestApplyDefaults_PreservesExistingValues(t *testing.T) {
	cfg := Config{
		LLMBaseURL: "https://custom.api.com",
		LLMModel:   "custom-model",
	}
	cfg.applyDefaults()

	if cfg.LLMBaseURL != "https://custom.api.com" {
		t.Errorf("LLMBaseURL was overwritten: got %q, want %q", cfg.LLMBaseURL, "https://custom.api.com")
	}
	if cfg.LLMModel != "custom-model" {
		t.Errorf("LLMModel was overwritten: got %q, want %q", cfg.LLMModel, "custom-model")
	}
}

// TestLoadConfig_LLMFieldsEmptyWithoutEnv проверяет, что без .env LLMBaseURL/LLMModel
// остаются пустыми — пользователь должен настроить их явно.
func TestLoadConfig_LLMFieldsEmptyWithoutEnv(t *testing.T) {
	os.Unsetenv("LLM_BASE_URL")
	os.Unsetenv("LLM_MODEL")
	os.Unsetenv("LLM_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")

	cfg := LoadConfig()

	if cfg.LLMBaseURL != "" {
		t.Errorf("LLMBaseURL should be empty without env, got %q", cfg.LLMBaseURL)
	}
	if cfg.LLMModel != "" {
		t.Errorf("LLMModel should be empty without env, got %q", cfg.LLMModel)
	}
}
