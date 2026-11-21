package envscrub

import (
	"strings"
	"testing"
)

func has(env []string, k string) bool {
	for _, kv := range env {
		if strings.HasPrefix(kv, k+"=") {
			return true
		}
	}
	return false
}

func TestDefaultKeepsEverything(t *testing.T) {
	t.Setenv(ModeEnv, "")
	t.Setenv("ANTHROPIC_API_KEY", "x")
	if !has(Environ(), "ANTHROPIC_API_KEY") {
		t.Fatal("default must not scrub")
	}
}

func TestScrubDropsSecretsKeepsRest(t *testing.T) {
	t.Setenv(ModeEnv, "secrets")
	t.Setenv(AllowEnv, "GITHUB_TOKEN")
	t.Setenv("ANTHROPIC_API_KEY", "x")
	t.Setenv("OLLAMA_API_KEY", "x")
	t.Setenv("AWS_SESSION_TOKEN", "x")
	t.Setenv("GITHUB_TOKEN", "x")
	t.Setenv("NORMA_FOO", "ok")
	env := Environ()
	for _, k := range []string{"ANTHROPIC_API_KEY", "OLLAMA_API_KEY", "AWS_SESSION_TOKEN"} {
		if has(env, k) {
			t.Fatalf("%s leaked", k)
		}
	}
	for _, k := range []string{"GITHUB_TOKEN", "NORMA_FOO", "PATH"} {
		if !has(env, k) {
			t.Fatalf("%s wrongly dropped", k)
		}
	}
}

func TestIsSecret(t *testing.T) {
	for n, want := range map[string]bool{"openai_api_key": true, "PATH": false, "HOME": false, "DB_PASSWORD": true, "TOKENIZERS_PARALLELISM": false} {
		if IsSecret(n) != want {
			t.Errorf("IsSecret(%q) != %v", n, want)
		}
	}
}
