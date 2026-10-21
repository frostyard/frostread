//go:build devfetch

// This file exists only in a binary built with -tags devfetch. It lets the
// fetcher reach the development feeds on 127.0.0.1, and nothing else: a
// release binary has no devFetchHosts at all, which
// TestReleaseHasNoDevFetch proves with go tool nm (docs/specs/api.md,
// Development-only).

package fetch

import (
	"context"
	"net"
	"net/http"
	"os"
	"strings"
)

// devFetchHosts lists the host:ports READ_DEV_ALLOW names, such as
// 127.0.0.1:8088.
func devFetchHosts() []string {
	v := os.Getenv("READ_DEV_ALLOW")
	if v == "" {
		return nil
	}
	var hosts []string
	for _, h := range strings.Split(v, ",") {
		if h = strings.TrimSpace(h); h != "" {
			hosts = append(hosts, h)
		}
	}
	return hosts
}

// devAllowed reports whether host:port is one READ_DEV_ALLOW names.
func devAllowed(hostport string) bool {
	for _, h := range devFetchHosts() {
		if h == hostport {
			return true
		}
	}
	return false
}

// devTransport lets a dev host through the guard, and only it. Every other
// dial still goes through netguard's check.
func devTransport(t *http.Transport) *http.Transport {
	guarded := t.DialContext
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if devAllowed(addr) {
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		}
		return guarded(ctx, network, addr)
	}
	return t
}
