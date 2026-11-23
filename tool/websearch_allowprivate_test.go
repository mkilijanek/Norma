package tool

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// isolateGuard removes every switch that skips the SSRF guard by itself, so the
// tests exercise the guard the way a default process does.
func isolateGuard(t *testing.T) {
	t.Helper()
	t.Setenv(WebFetchAllowPrivateEnv, "")
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(k, "")
	}
}

func deepseekSearchConfig(base string, allowPrivate bool) WebSearchConfig {
	return WebSearchConfig{Backend: "deepseek", DeepSeekAPIKey: "fixture", DeepSeekBaseURL: base, DeepSeekModel: "m", AllowPrivate: allowPrivate}
}

func postTo(t *testing.T, c *http.Client, url string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	return c.Do(req)
}

// Default: a DeepSeek-format endpoint on a loopback address is refused, as in
// v0.7.0, both through the client and through a real Search call.
func TestWebSearchDeepSeekPrivateEndpointRefusedByDefault(t *testing.T) {
	isolateGuard(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()

	p, err := newSearchProvider(deepseekSearchConfig(srv.URL, false))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Search(context.Background(), "q", 5); err == nil || !strings.Contains(err.Error(), "refusing to connect to non-public address") {
		t.Fatalf("want guard refusal, got %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("server was reached %d times", hits.Load())
	}
}

// With AllowPrivate the same endpoint is reachable.
func TestWebSearchDeepSeekAllowPrivateReachesLoopback(t *testing.T) {
	isolateGuard(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p, err := newSearchProvider(deepseekSearchConfig(srv.URL, true))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := postTo(t, searchProviderClient(t, p), srv.URL)
	if err != nil {
		t.Fatalf("allowed private endpoint failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || hits.Load() != 1 {
		t.Fatalf("status=%d hits=%d", resp.StatusCode, hits.Load())
	}
}

// AllowPrivate switches the client to the guard-off path; the redirect refusal
// must hold there too, and no second request may reach the redirect target.
func TestWebSearchAllowPrivateStillRefusesRedirect(t *testing.T) {
	isolateGuard(t)
	var leaked atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { leaked.Add(1) }))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer origin.Close()

	for _, ua := range []bool{false, true} {
		cfg := deepseekSearchConfig(origin.URL, true)
		if ua {
			cfg.CustomUserAgent, cfg.UserAgent = true, "fixture-agent/1.0"
		}
		p, err := newSearchProvider(cfg)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := postTo(t, searchProviderClient(t, p), origin.URL)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusFound || leaked.Load() != 0 {
			t.Fatalf("customUA=%v: redirect followed: status=%d calls=%d", ua, resp.StatusCode, leaked.Load())
		}
	}
}

// Only the DeepSeek-format backend honours AllowPrivate; every other backend
// keeps the guard even when the config sets it.
func TestWebSearchAllowPrivateIgnoredByOtherBackends(t *testing.T) {
	isolateGuard(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()

	for _, cfg := range []WebSearchConfig{
		{Backend: "ddgs"},
		{Backend: "brave-free", BraveAPIKey: "fixture"},
		{Backend: "tavily", TavilyAPIKey: "fixture"},
		{Backend: "keenable"},
		{Backend: "keenable", KeenableAPIKey: "fixture"},
		{Backend: ""},
	} {
		cfg.AllowPrivate = true
		t.Run(cfg.Backend, func(t *testing.T) {
			p, err := newSearchProvider(cfg)
			if err != nil {
				t.Fatal(err)
			}
			_, err = postTo(t, searchProviderClient(t, p), srv.URL)
			if err == nil || !strings.Contains(err.Error(), "refusing to connect to non-public address") {
				t.Fatalf("guard was lifted for %q: err=%v", cfg.Backend, err)
			}
		})
	}
	if hits.Load() != 0 {
		t.Fatalf("server was reached %d times", hits.Load())
	}
}
