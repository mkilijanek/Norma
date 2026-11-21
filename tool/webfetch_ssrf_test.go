package tool

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestBlockedAddr(t *testing.T) {
	for ip, want := range map[string]bool{
		"127.0.0.1": true, "::1": true, "10.1.2.3": true, "192.168.0.1": true,
		"172.16.0.1": true, "169.254.169.254": true, "fd00:ec2::254": true,
		"100.64.0.1": true, "0.0.0.0": true, "::ffff:127.0.0.1": true,
		"8.8.8.8": false, "1.1.1.1": false, "2606:4700::1111": false,
	} {
		if got := blockedAddr(netip.MustParseAddr(ip)); got != want {
			t.Errorf("blockedAddr(%s)=%v want %v", ip, got, want)
		}
	}
}

func fetchGuarded(t *testing.T, cfg WebFetchConfig, url string) (string, error) {
	t.Helper()
	c := newFetchClient(cfg)
	resp, err := c.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	return resp.Status, nil
}

func TestSSRFGuardBlocksLoopbackByDefault(t *testing.T) {
	t.Setenv(WebFetchAllowPrivateEnv, "")
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(k, "")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	if _, err := fetchGuarded(t, WebFetchConfig{}, srv.URL); err == nil || !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("loopback must be refused, err=%v", err)
	}
	// With the TLS option path (dedicated transport) too.
	if _, err := fetchGuarded(t, WebFetchConfig{InsecureTLS: true}, srv.URL); err == nil || !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("loopback must be refused on TLS-option transport, err=%v", err)
	}
	// Opt-outs.
	if _, err := fetchGuarded(t, WebFetchConfig{AllowPrivate: true}, srv.URL); err != nil {
		t.Fatalf("AllowPrivate: %v", err)
	}
	t.Setenv(WebFetchAllowPrivateEnv, "1")
	if _, err := fetchGuarded(t, WebFetchConfig{}, srv.URL); err != nil {
		t.Fatalf("env opt-out: %v", err)
	}
}

// Redirects and DNS rebinding reach the same dial path, so the control function
// is what protects them: it sees the resolved address of every connection.
func TestSSRFControlChecksResolvedAddress(t *testing.T) {
	if err := ssrfControl("tcp", "169.254.169.254:80", nil); err == nil {
		t.Fatal("metadata address must be refused")
	}
	if err := ssrfControl("tcp", "93.184.216.34:80", nil); err != nil {
		t.Fatalf("public address refused: %v", err)
	}
}
