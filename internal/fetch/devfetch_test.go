//go:build devfetch

package fetch

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/frostyard/lodge/kit/netguard"
)

// A devfetch build admits the one host READ_DEV_ALLOW names, and no other
// loopback port.
func TestDevFetchAdmitsOneHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("dev feed"))
	}))
	defer srv.Close()
	hostport := srv.URL[len("http://"):]
	t.Setenv("READ_DEV_ALLOW", hostport)
	c := New("test")
	res, err := c.Get(t.Context(), srv.URL, Options{})
	if err != nil {
		t.Fatalf("a dev host = %v, want it admitted", err)
	}
	if string(res.Body) != "dev feed" {
		t.Fatalf("body = %q", res.Body)
	}
	// Another loopback address is still refused, guard and all.
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer other.Close()
	if _, err := c.Get(t.Context(), other.URL, Options{}); !errors.Is(err, netguard.ErrInside) {
		t.Fatalf("another loopback host = %v, want ErrInside", err)
	}
}
