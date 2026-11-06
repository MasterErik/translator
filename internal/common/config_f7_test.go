package common

import (
	"testing"
	"time"
)

// ── F7: LLM_ANSWER_TIMEOUT, LLM_FALLBACK_MODELS, LLM_JSON_MODE ──────────────

// TestParseAnswerTimeout — пустая строка даёт дефолт, «0» — без лимита,
// валидная duration парсится, невалидная — ошибка (fail-fast на старте).
func TestParseAnswerTimeout(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    time.Duration
		wantErr bool
	}{
		{"пустая — дефолт", "", DefaultAnswerTimeout, false},
		{"ноль — без лимита", "0", 0, false},
		{"секунды", "12s", 12 * time.Second, false},
		{"с минутами", "1m30s", 90 * time.Second, false},
		{"мусор", "fast", 0, true},
		{"отрицательная", "-5s", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Config{AnswerTimeout: tt.input}
			got, err := c.ParseAnswerTimeout()
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseAnswerTimeout(%q) err = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("ParseAnswerTimeout(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// TestLoadConfig_LLMSets — LLM_REASONING_EFFORT / LLM_FALLBACK_MODELS /
// LLM_JSON_MODE читаются из env; JSON_MODE пустой даёт дефолт prompt.
func TestLoadConfig_LLMSets(t *testing.T) {
	t.Run("env override", func(t *testing.T) {
		cleanEnv(t, "LLM_REASONING_EFFORT", "LLM_FALLBACK_MODELS", "LLM_JSON_MODE", "LLM_ANSWER_TIMEOUT")
		t.Setenv("LLM_REASONING_EFFORT", "low")
		t.Setenv("LLM_FALLBACK_MODELS", "qwen/qwen3.6-27b, glm-4.7-flash")
		t.Setenv("LLM_ANSWER_TIMEOUT", "30s")

		cfg := minimalYAML(t, "")

		if cfg.LLMReasoningEffort != "low" {
			t.Errorf("LLMReasoningEffort = %q, want %q", cfg.LLMReasoningEffort, "low")
		}
		if len(cfg.LLMFallbackModels) != 2 || cfg.LLMFallbackModels[0] != "qwen/qwen3.6-27b" || cfg.LLMFallbackModels[1] != "glm-4.7-flash" {
			t.Errorf("LLMFallbackModels = %v, want [qwen/qwen3.6-27b glm-4.7-flash]", cfg.LLMFallbackModels)
		}
		timeout, err := cfg.ParseAnswerTimeout()
		if err != nil || timeout != 30*time.Second {
			t.Errorf("ParseAnswerTimeout() = %v, %v; want 30s, nil", timeout, err)
		}
	})

	t.Run("defaults", func(t *testing.T) {
		cleanEnv(t, "LLM_REASONING_EFFORT", "LLM_FALLBACK_MODELS", "LLM_JSON_MODE", "LLM_ANSWER_TIMEOUT")

		cfg := minimalYAML(t, "")

		if cfg.LLMReasoningEffort != "" {
			t.Errorf("LLMReasoningEffort = %q, want пусто", cfg.LLMReasoningEffort)
		}
		if len(cfg.LLMFallbackModels) != 0 {
			t.Errorf("LLMFallbackModels = %v, want пусто", cfg.LLMFallbackModels)
		}
		if cfg.LLMJSONMode != "prompt" {
			t.Errorf("LLMJSONMode = %q, want %q (дефолт applyDefaults)", cfg.LLMJSONMode, "prompt")
		}
		if _, err := cfg.ParseAnswerTimeout(); err != nil {
			t.Errorf("ParseAnswerTimeout() err = %v, want nil", err)
		}
	})
}

// TestLLMFallbackModelsFromYAML — fallback-модели читаются из config.yaml.
func TestLLMFallbackModelsFromYAML(t *testing.T) {
	cleanEnv(t, "LLM_FALLBACK_MODELS")
	t.Setenv("GLADIA_API_KEY", "test")

	cfg := minimalYAML(t, `
LLM_FALLBACK_MODELS:
  - qwen/qwen3.6-27b
  - glm-4.7-flash
`)

	if len(cfg.LLMFallbackModels) != 2 {
		t.Fatalf("LLMFallbackModels = %v, want 2 элемента", cfg.LLMFallbackModels)
	}
	if cfg.LLMFallbackModels[0] != "qwen/qwen3.6-27b" {
		t.Errorf("LLMFallbackModels[0] = %q, want %q", cfg.LLMFallbackModels[0], "qwen/qwen3.6-27b")
	}
}

// TestLLMFallbacksFromYAML — резервные провайдеры (F10.1) читаются из
// config.yaml: base_url/model/api_key_env.
func TestLLMFallbacksFromYAML(t *testing.T) {
	cleanEnv(t, "OPENROUTER_API_KEY")
	t.Setenv("GLADIA_API_KEY", "test")

	cfg := minimalYAML(t, `
LLM_FALLBACKS:
  - base_url: "https://openrouter.ai/api/v1"
    model: "meta-llama/llama-3.3-70b-instruct:free"
    api_key_env: "OPENROUTER_API_KEY"
`)

	if len(cfg.LLMFallbacks) != 1 {
		t.Fatalf("LLMFallbacks = %+v, want 1 элемент", cfg.LLMFallbacks)
	}
	fb := cfg.LLMFallbacks[0]
	if fb.BaseURL != "https://openrouter.ai/api/v1" || fb.Model != "meta-llama/llama-3.3-70b-instruct:free" || fb.APIKeyEnv != "OPENROUTER_API_KEY" {
		t.Errorf("LLMFallbacks[0] = %+v", fb)
	}
}
