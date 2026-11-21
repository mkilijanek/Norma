// Package envscrub removes secret-looking variables from the environment that
// Bash tasks and MCP servers inherit.
//
// By default children inherit the full environment (unchanged behaviour). Set
// NORMA_SCRUB_ENV=secrets to drop credentials such as ANTHROPIC_API_KEY, and
// NORMA_ENV_ALLOW=NAME1,NAME2 to keep specific variables that match.
package envscrub

import (
	"os"
	"strings"
)

const (
	// ModeEnv selects the scrub mode: "secrets" scrubs, "" / "off" does not.
	ModeEnv = "NORMA_SCRUB_ENV"
	// AllowEnv is a comma-separated list of variables never scrubbed.
	AllowEnv = "NORMA_ENV_ALLOW"
)

var secretSuffixes = []string{
	"_API_KEY", "_APIKEY", "_TOKEN", "_SECRET", "_SECRET_KEY",
	"_PASSWORD", "_PASSWD", "_CREDENTIALS", "_ACCESS_KEY", "_PRIVATE_KEY",
}

var secretPrefixes = []string{"AWS_", "AZURE_CLIENT_", "GOOGLE_APPLICATION_CREDENTIALS"}

var secretExact = map[string]bool{
	"API_KEY": true, "TOKEN": true, "SECRET": true, "PASSWORD": true,
	"SSH_AUTH_SOCK": true, "GITHUB_PAT": true,
}

// Enabled reports whether scrubbing is switched on.
func Enabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(ModeEnv)), "secrets")
}

// IsSecret reports whether name looks like a credential variable.
func IsSecret(name string) bool {
	n := strings.ToUpper(name)
	if secretExact[n] {
		return true
	}
	for _, s := range secretSuffixes {
		if strings.HasSuffix(n, s) {
			return true
		}
	}
	for _, p := range secretPrefixes {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}

// Environ returns os.Environ(), minus secret variables when scrubbing is
// enabled (except those named in NORMA_ENV_ALLOW).
func Environ() []string {
	env := os.Environ()
	if !Enabled() {
		return env
	}
	allow := map[string]bool{}
	for _, a := range strings.Split(os.Getenv(AllowEnv), ",") {
		if a = strings.ToUpper(strings.TrimSpace(a)); a != "" {
			allow[a] = true
		}
	}
	out := env[:0:0]
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if IsSecret(k) && !allow[strings.ToUpper(k)] {
			continue
		}
		out = append(out, kv)
	}
	return out
}
