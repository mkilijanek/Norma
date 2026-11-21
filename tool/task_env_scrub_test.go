package tool

import (
	"strings"
	"testing"
)

func TestWithEnvScrubsSecretsWhenEnabled(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-secret")
	has := func() bool {
		for _, kv := range withEnv(nil) {
			if strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") {
				return true
			}
		}
		return false
	}
	t.Setenv("NORMA_SCRUB_ENV", "")
	if !has() {
		t.Fatal("default must inherit")
	}
	t.Setenv("NORMA_SCRUB_ENV", "secrets")
	if has() {
		t.Fatal("secret leaked to child env")
	}
}
