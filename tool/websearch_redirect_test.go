package tool

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func searchProviderClient(t *testing.T, p searchProvider) *http.Client {
	t.Helper()
	switch v := p.(type) {
	case *ddgsProvider:
		return v.client
	case *braveProvider:
		return v.client
	case *tavilyProvider:
		return v.client
	case *keenableProvider:
		return v.client
	case *deepseekProvider:
		return v.client
	}
	t.Fatalf("unexpected provider type %T", p)
	return nil
}

// A search backend call carries credentials (API key headers, POST bodies). The
// client must hand a redirect back to the caller instead of following it, for
// every backend and whether or not a custom User-Agent wraps the client.
func TestSearchClientRefusesCredentialRedirect(t *testing.T) {
	backends := map[string]WebSearchConfig{
		"ddgs":       {Backend: "ddgs"},
		"brave-free": {Backend: "brave-free", BraveAPIKey: "fixture"},
		"tavily":     {Backend: "tavily", TavilyAPIKey: "fixture"},
		"keenable":   {Backend: "keenable", KeenableAPIKey: "fixture"},
		"deepseek":   {Backend: "deepseek", DeepSeekAPIKey: "fixture", DeepSeekBaseURL: "https://api.example.invalid/anthropic", DeepSeekModel: "m"},
	}
	for name, cfg := range backends {
		for _, customUA := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/customUA=%v", name, customUA), func(t *testing.T) {
				var leaked atomic.Int32
				target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					leaked.Add(1)
					w.WriteHeader(200)
				}))
				defer target.Close()
				var gotUA atomic.Value
				origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					gotUA.Store(r.Header.Get("User-Agent"))
					http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
				}))
				defer origin.Close()

				if customUA {
					cfg.CustomUserAgent, cfg.UserAgent = true, "fixture-agent/1.0"
				}
				p, err := newSearchProvider(cfg)
				if err != nil {
					t.Fatal(err)
				}
				client := searchProviderClient(t, p)
				req, err := http.NewRequest(http.MethodPost, origin.URL, nil)
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("X-Subscription-Token", "fixture")
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				if n := leaked.Load(); n != 0 || resp.StatusCode != http.StatusTemporaryRedirect {
					t.Fatalf("redirect leaked: calls=%d status=%d", n, resp.StatusCode)
				}
				if customUA {
					if ua, _ := gotUA.Load().(string); ua != "fixture-agent/1.0" {
						t.Fatalf("custom User-Agent not applied, got %q", ua)
					}
				}
			})
		}
	}
}
