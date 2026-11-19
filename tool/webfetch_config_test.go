package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateFetchConfig(t *testing.T) {
	dir := t.TempDir()
	badCA := filepath.Join(dir, "bad.pem")
	if err := os.WriteFile(badCA, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		cfg     WebFetchConfig
		wantErr string // substring; "" means valid
	}{
		{"no proxy", WebFetchConfig{}, ""},
		{"http proxy", WebFetchConfig{Proxy: "http://127.0.0.1:8080"}, ""},
		{"socks5 proxy", WebFetchConfig{Proxy: "socks5://127.0.0.1:1080"}, ""},
		{"missing scheme", WebFetchConfig{Proxy: "localhost:8080"}, "scheme"},
		{"bare host", WebFetchConfig{Proxy: "127.0.0.1:8080"}, "invalid proxy"},
		{"unsupported scheme", WebFetchConfig{Proxy: "ftp://127.0.0.1:21"}, "scheme"},
		{"missing host", WebFetchConfig{Proxy: "http://"}, "missing host"},
		{"unparsable", WebFetchConfig{Proxy: "http://[::1"}, "invalid proxy"},
		{"missing CA file", WebFetchConfig{CACert: filepath.Join(dir, "nope.pem")}, "cannot read CA"},
		{"CA without PEM", WebFetchConfig{CACert: badCA}, "no valid PEM"},
		{"bad CA ignored under InsecureTLS", WebFetchConfig{CACert: badCA, InsecureTLS: true}, ""},
	}
	for _, c := range cases {
		err := validateFetchConfig(c.cfg)
		switch {
		case c.wantErr == "" && err != nil:
			t.Errorf("%s: unexpected error: %v", c.name, err)
		case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
			t.Errorf("%s: err = %v, want substring %q", c.name, err, c.wantErr)
		}
	}
}

// A misconfigured proxy must make the tool refuse to fetch, never fall back to a
// direct request.
func TestWebFetchFailsClosedOnBadProxy(t *testing.T) {
	run := webFetchRunner(WebFetchConfig{Proxy: "localhost:8080"})
	in, _ := json.Marshal(map[string]any{"url": "http://127.0.0.1:1/"})
	res, err := run(context.Background(), in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Flatten(), "misconfigured") {
		t.Fatalf("want a misconfiguration error, got %+v", res)
	}
}

func TestWebSearchRejectsBadProxy(t *testing.T) {
	if _, err := newSearchProvider(WebSearchConfig{Backend: "ddgs", Proxy: "localhost:8080"}); err == nil {
		t.Fatal("want an error for an unusable proxy")
	}
}
