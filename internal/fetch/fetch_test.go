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

// A refusal is one sentence for the owner, and the typed error carries the
// address that was refused; the detail stays in the error for the log.
func TestRefusalSentence(t *testing.T) {
	c := New("test")
	_, err := c.Get(t.Context(), "http://127.0.0.1:9/feed.xml", Options{})
	if err == nil {
		t.Fatal("127.0.0.1 was fetched")
	}
	sentence, ok := Refusal(err)
	if !ok {
		t.Fatalf("%v is not a refusal", err)
	}
	if want := "Read does not fetch addresses inside your network (127.0.0.1)."; sentence != want {
		t.Fatalf("sentence = %q, want %q", sentence, want)
	}
	var r *Refused
	if !errors.As(err, &r) || r.URL != "http://127.0.0.1:9/feed.xml" {
		t.Fatalf("the refusal does not carry the URL: %+v", r)
	}
	if !errors.Is(err, netguard.ErrInside) {
		t.Fatalf("errors.Is(err, netguard.ErrInside) is false: %v", err)
	}

	_, err = c.Get(t.Context(), "file:///etc/passwd", Options{})
	sentence, ok = Refusal(err)
	if !ok || sentence != "Read fetches only public http and https addresses (file:///etc/passwd)." {
		t.Fatalf("file: refusal = %q, %v", sentence, ok)
	}
}

// secret is the made-up key a feed's URL carries below: a private feed's
// URL can hold one, in its path or its query, and no error text may name
// it.
const secret = "s3cret"

const secretURL = "http://feeds.example.com/feeds/s3cret-path/rss?key=s3cret-key"

// namesNoSecret fails when err's text carries the secret, and when it
// should name a host and does not.
func namesNoSecret(t *testing.T, err error, host string) {
	t.Helper()
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("the error names the URL: %v", err)
	}
	if host != "" && !strings.Contains(err.Error(), host) {
		t.Fatalf("the error names no host, want %s: %v", host, err)
	}
}

// Every error Get makes names a URL by its host alone, and errors.Is still
// finds the sentinel under it.
func TestErrorsNameOnlyTheHost(t *testing.T) {
	t.Run("a network fault", func(t *testing.T) {
		// The server takes the connection and closes it: the client's
		// *url.Error would repeat the whole URL, query included.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			_ = conn.Close()
		}))
		defer srv.Close()
		c := New("test")
		dialTo(c, srv)
		_, err := c.Get(t.Context(), secretURL, Options{})
		if err == nil {
			t.Fatal("a closed connection was fetched")
		}
		namesNoSecret(t, err, "feeds.example.com")
	})
	t.Run("a body over the cap", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(strings.Repeat("x", 100)))
		}))
		defer srv.Close()
		c := New("test")
		dialTo(c, srv)
		_, err := c.Get(t.Context(), secretURL, Options{MaxBytes: 10})
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("an over-cap body = %v, want ErrTooLarge", err)
		}
		namesNoSecret(t, err, "feeds.example.com")
	})
	t.Run("a refused address", func(t *testing.T) {
		c := New("test")
		_, err := c.Get(t.Context(), "http://127.0.0.1:9/s3cret-path?key=s3cret-key", Options{})
		if !errors.Is(err, netguard.ErrInside) {
			t.Fatalf("an inside address = %v, want ErrInside", err)
		}
		namesNoSecret(t, err, "127.0.0.1")
	})
	t.Run("a refused scheme", func(t *testing.T) {
		c := New("test")
		_, err := c.Get(t.Context(), "ftp://feeds.example.com/s3cret-path?key=s3cret-key", Options{})
		if !errors.Is(err, ErrRefused) {
			t.Fatalf("an ftp: URL = %v, want ErrRefused", err)
		}
		namesNoSecret(t, err, "feeds.example.com")
	})
}

// Host is the host name alone, and "" for a URL that does not parse.
func TestHost(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"https://feeds.example.com/a/b?key=1#frag", "feeds.example.com"},
		{"https://alice:pass@feeds.example.com:8443/x", "feeds.example.com"},
		{"http://127.0.0.1:9/x", "127.0.0.1"},
		{"file:///etc/passwd", ""},
		{"not a url", ""},
		{"", ""},
	} {
		if got := Host(tc.raw); got != tc.want {
			t.Errorf("Host(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}
