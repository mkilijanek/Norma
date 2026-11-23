package tool

import (
	"fmt"
	"net/http"
)

const (
	defaultFetchUserAgent = "norma/0.4"
	maxUserAgentBytes     = 256
)

// validateUserAgent checks the opt-in User-Agent. With custom=false nothing is
// checked and nothing changes.
func validateUserAgent(custom bool, ua string) error {
	if !custom {
		return nil
	}
	if ua == "" {
		return fmt.Errorf("CustomUserAgent is set but UserAgent is empty")
	}
	if len(ua) > maxUserAgentBytes {
		return fmt.Errorf("UserAgent exceeds %d bytes", maxUserAgentBytes)
	}
	for _, r := range ua {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("UserAgent must not contain control characters")
		}
	}
	return nil
}

// fetchUserAgent is the User-Agent WebFetch sends: the configured one only when
// the opt-in boolean is set, else the historical default.
func fetchUserAgent(cfg WebFetchConfig) string {
	if cfg.CustomUserAgent {
		return cfg.UserAgent
	}
	return defaultFetchUserAgent
}

type uaTransport struct {
	base http.RoundTripper
	ua   string
}

func (t uaTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("User-Agent", t.ua)
	return t.base.RoundTrip(r)
}

// withUserAgent returns c with every request (redirect hops included) carrying ua.
func withUserAgent(c *http.Client, ua string) *http.Client {
	base := c.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	cc := *c
	cc.Transport = uaTransport{base: base, ua: ua}
	return &cc
}
