package tool

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"
	"syscall"
	"time"
)

// WebFetchAllowPrivateEnv opts out of the default SSRF guard when set to 1/true.
const WebFetchAllowPrivateEnv = "NORMA_WEBFETCH_ALLOW_PRIVATE"

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// blockedAddr reports whether ip is loopback, private, link-local (incl. the
// 169.254.169.254 cloud metadata address), CGNAT, multicast or unspecified.
func blockedAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() ||
		ip.IsInterfaceLocalMulticast() || cgnat.Contains(ip)
}

// ssrfControl runs on the resolved address of every outgoing connection, so DNS
// rebinding and redirects to internal hosts are caught: each dial is re-checked.
func ssrfControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("webfetch: refusing unparsable address %q", host)
	}
	if blockedAddr(ip) {
		return fmt.Errorf("webfetch: refusing to connect to non-public address %s (set %s=1 to allow)", ip, WebFetchAllowPrivateEnv)
	}
	return nil
}

func guardedDialContext() func(ctx context.Context, network, addr string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: ssrfControl}
	return d.DialContext
}

func allowPrivateEnv() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(WebFetchAllowPrivateEnv)))
	return v == "1" || v == "true" || v == "yes"
}

// envProxySet reports whether the environment routes HTTP through a proxy; the
// proxy then resolves the destination, so a client-side address check is moot.
func envProxySet() bool {
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		if os.Getenv(k) != "" {
			return true
		}
	}
	return false
}
