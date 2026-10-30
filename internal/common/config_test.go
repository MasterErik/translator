package common

import (
	"os"
	"path/filepath"
	"strings"
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
			"LOG_DIR", "WINDOW_SIZE", "LLM_BASE_URL", "LLM_API_KEY", "SAVE_AUDIO")

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
			"WINDOW_SIZE", "LLM_BASE_URL")

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

// TestDebugInput — режим ручного ввода: env DEBUG_INPUT (bool через yaml-тег).
func TestDebugInput(t *testing.T) {
	t.Run("default false", func(t *testing.T) {
		cfg := minimalYAML(t, "")
		if cfg.DebugInput {
			t.Errorf("DEBUG_INPUT не задан: got true, want false")
		}
	})

	t.Run("truthy", func(t *testing.T) {
		for _, v := range []string{"true", "TRUE", "1", "t"} {
			t.Run(v, func(t *testing.T) {
				t.Setenv("DEBUG_INPUT", v)
				cfg := minimalYAML(t, "")
				if !cfg.DebugInput {
					t.Errorf("DEBUG_INPUT=%q: got false, want true", v)
				}
			})
		}
	})

	t.Run("falsy", func(t *testing.T) {
		for _, v := range []string{"false", "FALSE", "0", "f", "no"} {
			t.Run(v, func(t *testing.T) {
				t.Setenv("DEBUG_INPUT", v)
				cfg := minimalYAML(t, "")
				if cfg.DebugInput {
					t.Errorf("DEBUG_INPUT=%q: got true, want false", v)
				}
			})
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

// TestCandidateContextConfig — параметры fact-level candidate context (defaults, YAML, env-override).
func TestCandidateContextConfig(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		cleanEnv(t, "CANDIDATE_CONTEXT_DIR", "CANDIDATE_CONTEXT_MAX_TOKENS",
			"CANDIDATE_CONTEXT_MAX_PROFILE_TOKENS", "CANDIDATE_CONTEXT_MIN_SCORE",
			"CANDIDATE_CONTEXT_TOP_K")
		cfg := minimalYAML(t, "")
		if cfg.CandidateContext.Dir != "" {
			t.Errorf("Dir default: got %q, want empty", cfg.CandidateContext.Dir)
		}
		if cfg.CandidateContext.MaxTokens != 2000 {
			t.Errorf("MaxTokens default: got %d, want 2000", cfg.CandidateContext.MaxTokens)
		}
		if cfg.CandidateContext.MaxProfileTokens != 150 {
			t.Errorf("MaxProfileTokens default: got %d, want 150", cfg.CandidateContext.MaxProfileTokens)
		}
		if cfg.CandidateContext.MinScore != 0 {
			t.Errorf("MinScore default: got %v, want 0", cfg.CandidateContext.MinScore)
		}
		if cfg.CandidateContext.TopK != 5 {
			t.Errorf("TopK default: got %d, want 5", cfg.CandidateContext.TopK)
		}
	})

	t.Run("from_yaml", func(t *testing.T) {
		cleanEnv(t, "CANDIDATE_CONTEXT_DIR", "CANDIDATE_CONTEXT_MAX_TOKENS",
			"CANDIDATE_CONTEXT_MAX_PROFILE_TOKENS", "CANDIDATE_CONTEXT_MIN_SCORE",
			"CANDIDATE_CONTEXT_TOP_K")
		cfg := minimalYAML(t, `
candidate_context:
  dir: "candidate_context"
  max_tokens: 3000
  max_profile_tokens: 200
  min_score: 0.5
  top_k: 7
`)
		if cfg.CandidateContext.Dir != "candidate_context" {
			t.Errorf("Dir from YAML: got %q, want %q", cfg.CandidateContext.Dir, "candidate_context")
		}
		if cfg.CandidateContext.MaxTokens != 3000 {
			t.Errorf("MaxTokens from YAML: got %d, want 3000", cfg.CandidateContext.MaxTokens)
		}
		if cfg.CandidateContext.MaxProfileTokens != 200 {
			t.Errorf("MaxProfileTokens from YAML: got %d, want 200", cfg.CandidateContext.MaxProfileTokens)
		}
		if cfg.CandidateContext.MinScore != 0.5 {
			t.Errorf("MinScore from YAML: got %v, want 0.5", cfg.CandidateContext.MinScore)
		}
		if cfg.CandidateContext.TopK != 7 {
			t.Errorf("TopK from YAML: got %d, want 7", cfg.CandidateContext.TopK)
		}
	})

	t.Run("env_override", func(t *testing.T) {
		cleanEnv(t, "CANDIDATE_CONTEXT_DIR", "CANDIDATE_CONTEXT_MAX_TOKENS",
			"CANDIDATE_CONTEXT_MAX_PROFILE_TOKENS", "CANDIDATE_CONTEXT_MIN_SCORE",
			"CANDIDATE_CONTEXT_TOP_K")
		t.Setenv("CANDIDATE_CONTEXT_DIR", "my_context")
		t.Setenv("CANDIDATE_CONTEXT_MAX_TOKENS", "4000")
		t.Setenv("CANDIDATE_CONTEXT_MAX_PROFILE_TOKENS", "250")
		t.Setenv("CANDIDATE_CONTEXT_MIN_SCORE", "0.8")
		t.Setenv("CANDIDATE_CONTEXT_TOP_K", "10")
		cfg := minimalYAML(t, "")
		if cfg.CandidateContext.Dir != "my_context" {
			t.Errorf("Dir env override: got %q, want %q", cfg.CandidateContext.Dir, "my_context")
		}
		if cfg.CandidateContext.MaxTokens != 4000 {
			t.Errorf("MaxTokens env override: got %d, want 4000", cfg.CandidateContext.MaxTokens)
		}
		if cfg.CandidateContext.MaxProfileTokens != 250 {
			t.Errorf("MaxProfileTokens env override: got %d, want 250", cfg.CandidateContext.MaxProfileTokens)
		}
		if cfg.CandidateContext.MinScore != 0.8 {
			t.Errorf("MinScore env override: got %v, want 0.8", cfg.CandidateContext.MinScore)
		}
		if cfg.CandidateContext.TopK != 10 {
			t.Errorf("TopK env override: got %d, want 10", cfg.CandidateContext.TopK)
		}
	})
}

// TestInterviewConfig — целевой стек интервью: YAML, env-override, разбор списка.
func TestInterviewConfig(t *testing.T) {
	// envVars — все env, способные повлиять на новое поле (для чистого окружения).
	envVars := []string{"INTERVIEW_TARGET_STACK"}

	t.Run("default empty", func(t *testing.T) {
		cleanEnv(t, envVars...)
		cfg := minimalYAML(t, "")
		if len(cfg.Interview.TargetStack) != 0 {
			t.Errorf("TargetStack default: got %v, want empty", cfg.Interview.TargetStack)
		}
	})

	t.Run("yaml list", func(t *testing.T) {
		cleanEnv(t, envVars...)
		cfg := minimalYAML(t, `
interview:
  target_stack:
    - java
    - php
`)
		want := []string{"java", "php"}
		if !equalStrings(cfg.Interview.TargetStack, want) {
			t.Errorf("TargetStack from YAML list: got %v, want %v", cfg.Interview.TargetStack, want)
		}
	})

	t.Run("yaml string", func(t *testing.T) {
		cleanEnv(t, envVars...)
		cfg := minimalYAML(t, `
interview:
  target_stack: "java,php"
`)
		want := []string{"java", "php"}
		if !equalStrings(cfg.Interview.TargetStack, want) {
			t.Errorf("TargetStack from YAML string: got %v, want %v", cfg.Interview.TargetStack, want)
		}
	})

	// env-разбор — table-driven.
	envCases := []struct {
		name string
		env  string
		set  bool
		want []string
	}{
		{name: "env spaces and case", env: " java , PHP ,, ", set: true, want: []string{"java", "php"}},
		{name: "env single", env: "java", set: true, want: []string{"java"}},
		{name: "env commas only", env: " , ,, ", set: true, want: nil},
		{name: "env empty", env: "", set: true, want: nil},
		{name: "env absent", set: false, want: nil},
	}
	for _, tc := range envCases {
		t.Run(tc.name, func(t *testing.T) {
			cleanEnv(t, envVars...)
			if tc.set {
				t.Setenv("INTERVIEW_TARGET_STACK", tc.env)
			}
			cfg := minimalYAML(t, "")
			if !equalStrings(cfg.Interview.TargetStack, tc.want) {
				t.Errorf("TargetStack env %q: got %v, want %v", tc.env, cfg.Interview.TargetStack, tc.want)
			}
		})
	}

	t.Run("env overrides yaml", func(t *testing.T) {
		cleanEnv(t, envVars...)
		t.Setenv("INTERVIEW_TARGET_STACK", "golang")
		cfg := minimalYAML(t, `
interview:
  target_stack: "java,php"
`)
		want := []string{"golang"}
		if !equalStrings(cfg.Interview.TargetStack, want) {
			t.Errorf("TargetStack env override: got %v, want %v", cfg.Interview.TargetStack, want)
		}
	})
}

// TestCandidateContextWave1Config — поля волны 1: relative_threshold,
// max_matrix_tokens, allow_legacy_fallback, on_load_error.
func TestCandidateContextWave1Config(t *testing.T) {
	envVars := []string{
		"CANDIDATE_CONTEXT_RELATIVE_THRESHOLD", "CANDIDATE_CONTEXT_MAX_MATRIX_TOKENS",
		"CANDIDATE_CONTEXT_ALLOW_LEGACY_FALLBACK", "CANDIDATE_CONTEXT_ON_LOAD_ERROR",
	}

	t.Run("defaults", func(t *testing.T) {
		cleanEnv(t, envVars...)
		cfg := minimalYAML(t, "")
		if cfg.CandidateContext.RelativeThreshold != 0.6 {
			t.Errorf("RelativeThreshold default: got %v, want 0.6", cfg.CandidateContext.RelativeThreshold)
		}
		if cfg.CandidateContext.MaxMatrixTokens != 100 {
			t.Errorf("MaxMatrixTokens default: got %d, want 100", cfg.CandidateContext.MaxMatrixTokens)
		}
		if cfg.CandidateContext.AllowLegacyFallback {
			t.Errorf("AllowLegacyFallback default: got true, want false")
		}
		if cfg.CandidateContext.OnLoadError != "fail" {
			t.Errorf("OnLoadError default: got %q, want %q", cfg.CandidateContext.OnLoadError, "fail")
		}
	})

	t.Run("yaml negative threshold preserved", func(t *testing.T) {
		cleanEnv(t, envVars...)
		cfg := minimalYAML(t, `
candidate_context:
  relative_threshold: -1
`)
		if cfg.CandidateContext.RelativeThreshold != -1 {
			t.Errorf("RelativeThreshold from YAML: got %v, want -1 (порог выключен)", cfg.CandidateContext.RelativeThreshold)
		}
	})

	t.Run("yaml values", func(t *testing.T) {
		cleanEnv(t, envVars...)
		cfg := minimalYAML(t, `
candidate_context:
  relative_threshold: 0.3
  max_matrix_tokens: 250
  allow_legacy_fallback: true
  on_load_error: degraded
`)
		if cfg.CandidateContext.RelativeThreshold != 0.3 {
			t.Errorf("RelativeThreshold from YAML: got %v, want 0.3", cfg.CandidateContext.RelativeThreshold)
		}
		if cfg.CandidateContext.MaxMatrixTokens != 250 {
			t.Errorf("MaxMatrixTokens from YAML: got %d, want 250", cfg.CandidateContext.MaxMatrixTokens)
		}
		if !cfg.CandidateContext.AllowLegacyFallback {
			t.Errorf("AllowLegacyFallback from YAML: got false, want true")
		}
		if cfg.CandidateContext.OnLoadError != "degraded" {
			t.Errorf("OnLoadError from YAML: got %q, want %q", cfg.CandidateContext.OnLoadError, "degraded")
		}
	})

	t.Run("env override", func(t *testing.T) {
		cleanEnv(t, envVars...)
		t.Setenv("CANDIDATE_CONTEXT_RELATIVE_THRESHOLD", "-2")
		t.Setenv("CANDIDATE_CONTEXT_MAX_MATRIX_TOKENS", "50")
		t.Setenv("CANDIDATE_CONTEXT_ALLOW_LEGACY_FALLBACK", "true")
		cfg := minimalYAML(t, "")
		if cfg.CandidateContext.RelativeThreshold != -2 {
			t.Errorf("RelativeThreshold env override: got %v, want -2", cfg.CandidateContext.RelativeThreshold)
		}
		if cfg.CandidateContext.MaxMatrixTokens != 50 {
			t.Errorf("MaxMatrixTokens env override: got %d, want 50", cfg.CandidateContext.MaxMatrixTokens)
		}
		if !cfg.CandidateContext.AllowLegacyFallback {
			t.Errorf("AllowLegacyFallback env override: got false, want true")
		}
	})

	t.Run("env invalid number ignored", func(t *testing.T) {
		cleanEnv(t, envVars...)
		t.Setenv("CANDIDATE_CONTEXT_RELATIVE_THRESHOLD", "abc")
		t.Setenv("CANDIDATE_CONTEXT_MAX_MATRIX_TOKENS", "xyz")
		cfg := minimalYAML(t, "")
		if cfg.CandidateContext.RelativeThreshold != 0.6 {
			t.Errorf("RelativeThreshold with invalid env: got %v, want default 0.6", cfg.CandidateContext.RelativeThreshold)
		}
		if cfg.CandidateContext.MaxMatrixTokens != 100 {
			t.Errorf("MaxMatrixTokens with invalid env: got %d, want default 100", cfg.CandidateContext.MaxMatrixTokens)
		}
	})

	// on_load_error — table-driven по env.
	onLoadErrCases := []struct {
		name    string
		env     string
		want    string
		wantErr bool
	}{
		{name: "degraded", env: "degraded", want: "degraded"},
		{name: "fail", env: "fail", want: "fail"},
		{name: "case and spaces", env: "  DEGRADED ", want: "degraded"},
		{name: "invalid", env: "oops", wantErr: true},
		{name: "empty-ish invalid", env: "silent", wantErr: true},
	}
	for _, tc := range onLoadErrCases {
		t.Run("on_load_error/"+tc.name, func(t *testing.T) {
			cleanEnv(t, envVars...)
			t.Setenv("CANDIDATE_CONTEXT_ON_LOAD_ERROR", tc.env)
			cfg, err := loadTempConfig(t, "")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("CANDIDATE_CONTEXT_ON_LOAD_ERROR=%q: expected error, got nil", tc.env)
				}
				for _, sub := range []string{"loading config from env", "CANDIDATE_CONTEXT_ON_LOAD_ERROR", tc.env} {
					if !strings.Contains(err.Error(), sub) {
						t.Errorf("error for %q should contain %q; got %q", tc.env, sub, err.Error())
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("CANDIDATE_CONTEXT_ON_LOAD_ERROR=%q: unexpected error: %v", tc.env, err)
			}
			if cfg.CandidateContext.OnLoadError != tc.want {
				t.Errorf("OnLoadError: got %q, want %q", cfg.CandidateContext.OnLoadError, tc.want)
			}
		})
	}
}

// loadTempConfig создаёт временный config.yaml с переданным содержимым и
// вызывает LoadConfigFromYAML, возвращая Config и ошибку без t.Fatal.
func loadTempConfig(t *testing.T, content string) (*Config, error) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return LoadConfigFromYAML(path)
}

// equalStrings сравнивает два слайса строк (nil и пустой слайс эквивалентны).
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// cleanEnv удаляет переменные окружения из списка.
func cleanEnv(t *testing.T, vars ...string) {
	t.Helper()
	for _, v := range vars {
		os.Unsetenv(v)
	}
}

// TestSystemPromptFileConfig — SYSTEM_PROMPT_FILE: путь к шаблону system-промпта
// (пусто → встроенный дефолт), yaml-ключ и env-override того же имени.
func TestSystemPromptFileConfig(t *testing.T) {
	const envVar = "SYSTEM_PROMPT_FILE"

	t.Run("default empty", func(t *testing.T) {
		cleanEnv(t, envVar)
		cfg := minimalYAML(t, "")
		if cfg.SystemPromptFile != "" {
			t.Errorf("SystemPromptFile default: got %q, want \"\" (встроенный дефолт)", cfg.SystemPromptFile)
		}
	})

	t.Run("yaml value", func(t *testing.T) {
		cleanEnv(t, envVar)
		cfg := minimalYAML(t, "SYSTEM_PROMPT_FILE: \"prompts/system_prompt.md\"\n")
		if cfg.SystemPromptFile != "prompts/system_prompt.md" {
			t.Errorf("SystemPromptFile from YAML: got %q, want %q", cfg.SystemPromptFile, "prompts/system_prompt.md")
		}
	})

	t.Run("env override", func(t *testing.T) {
		cleanEnv(t, envVar)
		t.Setenv(envVar, "custom/prompt.md")
		cfg := minimalYAML(t, "SYSTEM_PROMPT_FILE: \"prompts/system_prompt.md\"\n")
		if cfg.SystemPromptFile != "custom/prompt.md" {
			t.Errorf("SystemPromptFile env override: got %q, want %q", cfg.SystemPromptFile, "custom/prompt.md")
		}
	})
}
