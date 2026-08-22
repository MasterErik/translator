package common

import (
	"os"
	"path/filepath"
	"testing"
)

// minimalYAML создаёт временный config.yaml с переданным содержимым,
// вызывает LoadConfigFromYAML и возвращает Config. t.Fatal при ошибках.
func minimalYAML(t *testing.T, content string) *Config {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfigFromYAML(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// TestLoadConfig — тест для LoadConfigFromYAML:
// проверяет defaults, env-переопределения, applyDefaults,
// сохранение существующих значений и пустые LLM-поля без env.
func TestLoadConfig(t *testing.T) {
	// defaults
	t.Run("defaults", func(t *testing.T) {
		cleanEnv(t, "GLADIA_API_KEY", "LLM_MODEL", "TARGET_LANG", "TARGET_LANGUAGE",
			"LOG_DIR", "WINDOW_SIZE", "CV_CONTEXT", "LLM_BASE_URL", "LLM_API_KEY", "SAVE_AUDIO")

		cfg := minimalYAML(t, "")

		if cfg.TargetLang != "ru" {
			t.Errorf("TargetLang default: got %q, want %q", cfg.TargetLang, "ru")
		}
		if cfg.LogDir != "./logs" {
			t.Errorf("LogDir default: got %q, want %q", cfg.LogDir, "./logs")
		}
		if cfg.SaveAudio != false {
			t.Errorf("SaveAudio default: got %v, want false", cfg.SaveAudio)
		}
	})

	// env_overrides
	t.Run("env_overrides", func(t *testing.T) {
		cleanEnv(t, "GLADIA_API_KEY", "LLM_MODEL", "LLM_API_KEY",
			"TARGET_LANG", "LLM_BASE_URL")

		t.Run("LLMBaseURL", func(t *testing.T) {
			t.Setenv("LLM_BASE_URL", "https://api.z.ai/api/paas/v4/")
			cfg := minimalYAML(t, "")
			if cfg.LLMBaseURL != "https://api.z.ai/api/paas/v4/" {
				t.Errorf("LLMBaseURL: got %q, want %q", cfg.LLMBaseURL, "https://api.z.ai/api/paas/v4/")
			}
		})

		t.Run("LLMAPIKey", func(t *testing.T) {
			t.Setenv("LLM_API_KEY", "sk-glm-key")
			cfg := minimalYAML(t, "")
			if cfg.LLMAPIKey != "sk-glm-key" {
				t.Errorf("LLMAPIKey: got %q, want %q", cfg.LLMAPIKey, "sk-glm-key")
			}
		})

		t.Run("TargetLang", func(t *testing.T) {
			t.Setenv("TARGET_LANG", "en")
			cfg := minimalYAML(t, "")
			if cfg.TargetLang != "en" {
				t.Errorf("TargetLang: got %q, want %q", cfg.TargetLang, "en")
			}
		})

		t.Run("GladiaAPIKey", func(t *testing.T) {
			t.Setenv("GLADIA_API_KEY", "gladia-test-key")
			cfg := minimalYAML(t, "")
			if cfg.GladiaAPIKey != "gladia-test-key" {
				t.Errorf("GladiaAPIKey: got %q, want %q", cfg.GladiaAPIKey, "gladia-test-key")
			}
		})
	})

	// apply_defaults
	t.Run("apply_defaults", func(t *testing.T) {
		cfg := Config{}
		cfg.applyDefaults()

		if cfg.TargetLang != "ru" {
			t.Errorf("TargetLang: got %q, want %q", cfg.TargetLang, "ru")
		}
		if cfg.LogDir != "./logs" {
			t.Errorf("LogDir: got %q, want %q", cfg.LogDir, "./logs")
		}
	})

	// apply_preserves
	t.Run("apply_preserves", func(t *testing.T) {
		cfg := Config{
			LLMBaseURL: "https://custom.api.com",
			LLMModel:   "custom-model",
		}
		cfg.applyDefaults()

		if cfg.LLMBaseURL != "https://custom.api.com" {
			t.Errorf("LLMBaseURL overwritten: got %q", cfg.LLMBaseURL)
		}
		if cfg.LLMModel != "custom-model" {
			t.Errorf("LLMModel overwritten: got %q", cfg.LLMModel)
		}
	})

	// empty_without_env — без env переменных LLM-поля должны быть пусты
	t.Run("empty_without_env", func(t *testing.T) {
		cleanEnv(t, "LLM_BASE_URL", "LLM_MODEL", "LLM_API_KEY")

		cfg := minimalYAML(t, "")

		if cfg.LLMBaseURL != "" {
			t.Errorf("LLMBaseURL should be empty, got %q", cfg.LLMBaseURL)
		}
		if cfg.LLMModel != "" {
			t.Errorf("LLMModel should be empty, got %q", cfg.LLMModel)
		}
	})
}

// TestLoadConfigFromYAML — тест для LoadConfigFromYAML с YAML-контентом.
func TestLoadConfigFromYAML(t *testing.T) {
	// basic
	t.Run("basic", func(t *testing.T) {
		cleanEnv(t, "GLADIA_API_KEY", "LLM_MODEL", "TARGET_LANG", "TARGET_LANGUAGE",
			"LOG_DIR", "WINDOW_SIZE", "CANDIDATE_CONTEXT_FILE", "LLM_BASE_URL", "LLM_API_KEY")

		cfg := minimalYAML(t, `
LLM_MODEL: "qwen-2.5-72b"
TARGET_LANG: "de"
CANDIDATE_CONTEXT_FILE: "candidate_context.md"
`)

		if cfg.LLMModel != "qwen-2.5-72b" {
			t.Errorf("LLMModel from YAML: got %q, want %q", cfg.LLMModel, "qwen-2.5-72b")
		}
		if cfg.CandidateContextFile != "candidate_context.md" {
			t.Errorf("CandidateContextFile from YAML: got %q, want %q", cfg.CandidateContextFile, "candidate_context.md")
		}
		if cfg.TargetLang != "de" {
			t.Errorf("TargetLang from YAML: got %q, want %q", cfg.TargetLang, "de")
		}
	})

	// env_override
	t.Run("env_override", func(t *testing.T) {
		cleanEnv(t, "GLADIA_API_KEY", "LLM_MODEL", "TARGET_LANG", "TARGET_LANGUAGE",
			"LOG_DIR", "WINDOW_SIZE", "CANDIDATE_CONTEXT_FILE", "LLM_BASE_URL", "LLM_API_KEY")

		t.Setenv("LLM_MODEL", "deepseek-v3")

		cfg := minimalYAML(t, `
LLM_MODEL: "qwen-2.5-72b"
TARGET_LANG: "de"
`)

		if cfg.LLMModel != "deepseek-v3" {
			t.Errorf("Env should override YAML: got %q, want %q", cfg.LLMModel, "deepseek-v3")
		}
	})

	// missing_file
	t.Run("missing_file", func(t *testing.T) {
		_, err := LoadConfigFromYAML("/nonexistent/path/config.yaml")
		if err == nil {
			t.Fatal("expected error for missing file, got nil")
		}
	})

	// save_audio_yaml
	t.Run("save_audio_yaml", func(t *testing.T) {
		cleanEnv(t, "SAVE_AUDIO")

		cfg := minimalYAML(t, `
SAVE_AUDIO: true
LLM_MODEL: "llama-3.3-70b-versatile"
`)
		if cfg.SaveAudio != true {
			t.Errorf("SaveAudio from YAML: got %v, want true", cfg.SaveAudio)
		}

		// Env переопределяет YAML.
		t.Setenv("SAVE_AUDIO", "false")
		cfg = minimalYAML(t, `
SAVE_AUDIO: true
LLM_MODEL: "llama-3.3-70b-versatile"
`)
		if cfg.SaveAudio != false {
			t.Errorf("SAVE_AUDIO env should override YAML: got %v, want false", cfg.SaveAudio)
		}
	})

	// llm_base_url
	t.Run("llm_base_url", func(t *testing.T) {
		cleanEnv(t, "LLM_BASE_URL", "LLM_MODEL")

		cfg := minimalYAML(t, `
LLM_BASE_URL: "https://api.z.ai/api/paas/v4/"
LLM_MODEL: "glm-4-flash"
`)

		if cfg.LLMBaseURL != "https://api.z.ai/api/paas/v4/" {
			t.Errorf("LLMBaseURL from YAML: got %q", cfg.LLMBaseURL)
		}
		if cfg.LLMModel != "glm-4-flash" {
			t.Errorf("LLMModel from YAML: got %q", cfg.LLMModel)
		}
	})

	// target_lang
	t.Run("target_lang", func(t *testing.T) {
		cleanEnv(t, "TARGET_LANG")

		cfg := minimalYAML(t, `TARGET_LANG: "fr"`)

		if cfg.TargetLang != "fr" {
			t.Errorf("TargetLang from YAML: got %q, want %q", cfg.TargetLang, "fr")
		}
	})

	// broken_env_file
	t.Run("broken_env_file", func(t *testing.T) {
		cleanEnv(t, "GLADIA_API_KEY", "LLM_MODEL", "LLM_API_KEY",
			"TARGET_LANG", "TARGET_LANGUAGE", "LOG_DIR",
			"WINDOW_SIZE", "CV_CONTEXT", "LLM_BASE_URL")

		dir := t.TempDir()
		yamlPath := filepath.Join(dir, "config.yaml")
		yamlContent := `LLM_BASE_URL: "https://api.groq.com/openai/v1"
LLM_MODEL: "llama-3.3-70b-versatile"
`
		if err := os.WriteFile(yamlPath, []byte(yamlContent), 0644); err != nil {
			t.Fatal(err)
		}

		envContent := `LLM_API_KEY=test-llm-key
GLADIA_API_KEY=test-gladia-key
CANDIDATE_CONTEXT_FILE=candidate_context.md
`
		if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(envContent), 0644); err != nil {
			t.Fatal(err)
		}

		origDir, _ := os.Getwd()
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		defer os.Chdir(origDir)

		cfg, err := LoadConfigFromYAML(yamlPath)
		if err != nil {
			t.Fatalf("LoadConfigFromYAML: %v", err)
		}

		if cfg.LLMAPIKey != "test-llm-key" {
			t.Errorf("LLMAPIKey: got %q, want %q", cfg.LLMAPIKey, "test-llm-key")
		}
		if cfg.GladiaAPIKey != "test-gladia-key" {
			t.Errorf("GladiaAPIKey: got %q, want %q", cfg.GladiaAPIKey, "test-gladia-key")
		}
		if cfg.CandidateContextFile != "candidate_context.md" {
			t.Errorf("CandidateContextFile: got %q, want %q", cfg.CandidateContextFile, "candidate_context.md")
		}
	})
}

// TestSaveAudio — table-driven тест для SAVE_AUDIO env-переменной.
func TestSaveAudio(t *testing.T) {
	t.Run("truthy", func(t *testing.T) {
		for _, v := range []string{"true", "TRUE", "True", "1", "t", "T"} {
			t.Run(v, func(t *testing.T) {
				t.Setenv("SAVE_AUDIO", v)
				cfg := minimalYAML(t, "")
				if cfg.SaveAudio != true {
					t.Errorf("SAVE_AUDIO=%q: got %v, want true", v, cfg.SaveAudio)
				}
			})
		}
	})

	t.Run("falsy", func(t *testing.T) {
		for _, v := range []string{"false", "FALSE", "False", "0", "f", "F"} {
			t.Run(v, func(t *testing.T) {
				t.Setenv("SAVE_AUDIO", v)
				cfg := minimalYAML(t, "")
				if cfg.SaveAudio != false {
					t.Errorf("SAVE_AUDIO=%q: got %v, want false", v, cfg.SaveAudio)
				}
			})
		}
	})
}

// TestConversationConfig — параметры conversation context (defaults, YAML, env-override).
func TestConversationConfig(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		cleanEnv(t, "CONVERSATION_RECENT_TURNS", "CONVERSATION_MAX_CONTEXT_TOKENS")
		cfg := minimalYAML(t, "")
		if cfg.Conversation.RecentTurns != 6 {
			t.Errorf("RecentTurns default: got %d, want 6", cfg.Conversation.RecentTurns)
		}
		if cfg.Conversation.MaxContextTokens != 4000 {
			t.Errorf("MaxContextTokens default: got %d, want 4000", cfg.Conversation.MaxContextTokens)
		}
	})

	t.Run("from_yaml", func(t *testing.T) {
		cleanEnv(t, "CONVERSATION_RECENT_TURNS", "CONVERSATION_MAX_CONTEXT_TOKENS")
		cfg := minimalYAML(t, `
conversation:
  recent_turns: 3
  max_context_tokens: 1000
`)
		if cfg.Conversation.RecentTurns != 3 {
			t.Errorf("RecentTurns from YAML: got %d, want 3", cfg.Conversation.RecentTurns)
		}
		if cfg.Conversation.MaxContextTokens != 1000 {
			t.Errorf("MaxContextTokens from YAML: got %d, want 1000", cfg.Conversation.MaxContextTokens)
		}
	})

	t.Run("env_override", func(t *testing.T) {
		cleanEnv(t, "CONVERSATION_RECENT_TURNS", "CONVERSATION_MAX_CONTEXT_TOKENS")
		t.Setenv("CONVERSATION_RECENT_TURNS", "9")
		cfg := minimalYAML(t, "")
		if cfg.Conversation.RecentTurns != 9 {
			t.Errorf("RecentTurns env override: got %d, want 9", cfg.Conversation.RecentTurns)
		}
	})
}

// cleanEnv удаляет переменные окружения из списка.
func cleanEnv(t *testing.T, vars ...string) {
	t.Helper()
	for _, v := range vars {
		os.Unsetenv(v)
	}
}
