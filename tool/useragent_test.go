package tool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type uaRecorder struct {
	mu  sync.Mutex
	got []string
}

func (r *uaRecorder) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.got = append(r.got, req.Header.Get("User-Agent"))
		r.mu.Unlock()
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>ok</body></html>"))
	})
}

func fetchOnce(t *testing.T, cfg WebFetchConfig, url string) string {
	t.Helper()
	in, _ := json.Marshal(map[string]any{"url": url})
	res, err := NewWebFetch(cfg).Call(context.Background(), in, &ToolContext{})
	if err != nil {
		t.Fatal(err)
	}
	return res.Flatten()
}

func TestWebFetchUserAgentOptIn(t *testing.T) {
	rec := &uaRecorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	fetchOnce(t, WebFetchConfig{AllowPrivate: true}, srv.URL)
	// A configured value is ignored unless the boolean is set.
	fetchOnce(t, WebFetchConfig{AllowPrivate: true, UserAgent: "acme/1.0"}, srv.URL)
	fetchOnce(t, WebFetchConfig{AllowPrivate: true, CustomUserAgent: true, UserAgent: "acme/1.0"}, srv.URL)
	want := []string{"norma/0.4", "norma/0.4", "acme/1.0"}
	if strings.Join(rec.got, "|") != strings.Join(want, "|") {
		t.Fatalf("user agents = %q, want %q", rec.got, want)
	}
}

func TestWebFetchUserAgentRejectsBadValues(t *testing.T) {
	for _, ua := range []string{"", "a\r\nX-Evil: 1", "a\nb", "a\x00b", strings.Repeat("x", 257)} {
		out := fetchOnce(t, WebFetchConfig{CustomUserAgent: true, UserAgent: ua}, "http://127.0.0.1:1/")
		if !strings.Contains(out, "misconfigured") {
			t.Errorf("UserAgent %q accepted: %q", ua, out)
		}
	}
	// With the boolean off, an invalid value is never used, so it is not an error.
	if out := fetchOnce(t, WebFetchConfig{UserAgent: "a\r\nb"}, "http://127.0.0.1:1/"); strings.Contains(out, "misconfigured") {
		t.Errorf("flag off must not validate: %q", out)
	}
}

func TestUserAgentOnDirectRetry(t *testing.T) {
	rec := &uaRecorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()
	// The dead proxy fails with a proxy-like error, triggering the direct retry.
	out := fetchOnce(t, WebFetchConfig{AllowPrivate: true, Proxy: deadProxy, CustomUserAgent: true, UserAgent: "acme/1.0"}, srv.URL)
	if len(rec.got) != 1 || rec.got[0] != "acme/1.0" {
		t.Fatalf("direct retry UA = %q (out %q)", rec.got, out)
	}
}

func TestSearchUserAgentOptIn(t *testing.T) {
	rec := &uaRecorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()
	run := func(cfg WebSearchConfig) {
		prov, err := newSearchProvider(cfg)
		if err != nil {
			t.Fatal(err)
		}
		d := prov.(*ddgsProvider)
		d.endpoint = srv.URL
		_, _ = d.Search(context.Background(), "q", 3)
	}
	run(WebSearchConfig{})
	run(WebSearchConfig{CustomUserAgent: true, UserAgent: "acme/1.0"})
	if len(rec.got) != 2 || !strings.HasPrefix(rec.got[0], "Mozilla/5.0") || rec.got[1] != "acme/1.0" {
		t.Fatalf("search UAs = %q", rec.got)
	}
	if _, err := newSearchProvider(WebSearchConfig{CustomUserAgent: true, UserAgent: "a\r\nb"}); err == nil {
		t.Fatal("bad UserAgent must be rejected")
	}
}
