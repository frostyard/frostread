package fetch

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frostyard/lodge/kit/netguard"
)

// dialTo points c at the test server, whatever host its URL names. It is
// this package's own test hook, so the guard stays on in production and in
// every test that does not call it.
func dialTo(c *Client, srv *httptest.Server) {
	addr := strings.TrimPrefix(srv.URL, "http://")
	c.tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", addr)
	}
}

func TestRefusesInsideAddress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("a refused request reached the server")
	}))
	defer srv.Close()
	c := New("test")
	if _, err := c.Get(t.Context(), srv.URL, Options{}); !errors.Is(err, netguard.ErrInside) {
		t.Fatalf("fetching 127.0.0.1 = %v, want ErrInside", err)
	}
}

func TestRefusesBadURLs(t *testing.T) {
	c := New("test")
	for _, raw := range []string{
		"http://alice:secret@example.com/feed.xml",
		"file:///etc/passwd",
		"ftp://example.com/feed.xml",
		"gopher://example.com/",
		"http://localhost:8080/feed.xml",
		"http://lodge.example.ts.net/feed.xml",
		"http://nas.local/feed.xml",
		"http://router/feed.xml",
		"http://192.0.2.1/feed.xml",
		"http://[::1]/feed.xml",
		"not a url",
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := c.Get(t.Context(), raw, Options{}); !errors.Is(err, ErrRefused) && !errors.Is(err, netguard.ErrInside) {
				t.Fatalf("%s = %v, want a refusal", raw, err)
			}
		})
	}
}

// A name that resolves inside is refused at the dial, which is the same
// check that refuses a rebinding: netguard.Control runs after DNS.
func TestTheDialRefusesAnInsideAddress(t *testing.T) {
	d := netguard.Dialer(time.Second)
	_, err := d.DialContext(t.Context(), "tcp", "127.0.0.1:9")
	if !errors.Is(err, netguard.ErrInside) {
		t.Fatalf("dialling 127.0.0.1 = %v, want ErrInside", err)
	}
}

func TestBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "frostread/test" {
			t.Errorf("User-Agent = %q", got)
		}
		if got := r.Header.Get("Cookie"); got != "" {
			t.Errorf("a cookie was sent: %q", got)
		}
		if got := r.Header.Get("Accept"); got != "application/rss+xml" {
			t.Errorf("Accept = %q", got)
		}
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()
	c := New("test")
	dialTo(c, srv)
	res, err := c.Get(t.Context(), "http://example.com/feed.xml", Options{Accept: "application/rss+xml"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != 200 || string(res.Body) != "hello" || res.URL != "http://example.com/feed.xml" {
		t.Fatalf("response = %+v", res)
	}
}

func TestSizeCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 100)))
	}))
	defer srv.Close()
	c := New("test")
	dialTo(c, srv)
	if _, err := c.Get(t.Context(), "http://example.com/big", Options{MaxBytes: 10}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("an over-cap body = %v, want ErrTooLarge", err)
	}
}

func TestConditionalGet(t *testing.T) {
	var saw []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		saw = append(saw, r.Header.Get("If-None-Match")+"|"+r.Header.Get("If-Modified-Since"))
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.Header().Set("ETag", `"v1"`)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Last-Modified", "Tue, 20 Oct 2026 09:00:00 GMT")
		_, _ = w.Write([]byte("body"))
	}))
	defer srv.Close()
	c := New("test")
	dialTo(c, srv)
	first, err := c.Get(t.Context(), "http://example.com/feed.xml", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if first.ETag != `"v1"` || first.LastModified == "" {
		t.Fatalf("validators = %q, %q", first.ETag, first.LastModified)
	}
	second, err := c.Get(t.Context(), "http://example.com/feed.xml",
		Options{ETag: first.ETag, LastModified: first.LastModified})
	if err != nil {
		t.Fatal(err)
	}
	if !second.NotModified || second.ETag != `"v1"` || len(second.Body) != 0 {
		t.Fatalf("conditional answer = %+v", second)
	}
	if want := `"v1"|Tue, 20 Oct 2026 09:00:00 GMT`; saw[len(saw)-1] != want {
		t.Fatalf("second request sent %q, want %q", saw[len(saw)-1], want)
	}
}

func TestRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /hop?max=N&n=K redirects until n reaches N; /inside redirects
		// to an inside address.
		switch r.URL.Path {
		case "/inside":
			http.Redirect(w, r, "http://127.0.0.1/secret", http.StatusFound)
		case "/hop":
			max, _ := strconv.Atoi(r.URL.Query().Get("max"))
			n, _ := strconv.Atoi(r.URL.Query().Get("n"))
			if n >= max {
				_, _ = w.Write([]byte("done"))
				return
			}
			http.Redirect(w, r, fmt.Sprintf("/hop?max=%d&n=%d", max, n+1), http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New("test")
	dialTo(c, srv)
	res, err := c.Get(t.Context(), "http://example.com/hop?max=5&n=0", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Body) != "done" || res.URL != "http://example.com/hop?max=5&n=5" {
		t.Fatalf("five redirects = %+v", res)
	}
	if _, err := c.Get(t.Context(), "http://example.com/hop?max=6&n=0", Options{}); !errors.Is(err, ErrRefused) {
		t.Fatalf("six redirects = %v, want ErrRefused", err)
	}
	if _, err := c.Get(t.Context(), "http://example.com/inside", Options{}); !errors.Is(err, netguard.ErrInside) {
		t.Fatalf("a redirect to 127.0.0.1 = %v, want ErrInside", err)
	}
}

func TestRetryAfter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	c := New("test")
	dialTo(c, srv)
	res, err := c.Get(t.Context(), "http://example.com/feed.xml", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != http.StatusTooManyRequests || res.RetryAfter != 2*time.Minute {
		t.Fatalf("429 = %+v", res)
	}
}
