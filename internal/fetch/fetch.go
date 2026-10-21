// Package fetch is frostread's only outbound HTTP client (ADR-0003). It
// asks lodge's kit/netguard whether an address is public before it dials
// it, so a feed, a page or an image inside the owner's network is refused,
// redirects and DNS rebinding included.
//
// Nothing else in frostread may fetch a URL: a second client would not
// carry the guard.
package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/frostyard/lodge/kit/netguard"
)

// The size caps, in bytes.
const (
	// MaxFeed caps a feed's body.
	MaxFeed = 10 << 20
	// MaxPage caps a page's body.
	MaxPage = 10 << 20
	// MaxImage caps one image.
	MaxImage = 10 << 20
	// MaxItemImages caps the images of one item, together.
	MaxItemImages = 40 << 20
)

// DefaultTimeout caps one request, redirects and the body included.
const DefaultTimeout = 30 * time.Second

// MaxRedirects is the most redirects one request follows.
const MaxRedirects = 5

var (
	// ErrTooLarge means a body was over its cap. It is never truncated.
	ErrTooLarge = errors.New("fetch: the body is too large")
	// ErrRefused means the URL is not one this client may fetch: a
	// scheme other than http or https, a userinfo, or an inside name.
	ErrRefused = errors.New("fetch: the url is refused")
)

// Fetcher is the part of Client a caller uses, so a test can stand in
// for the network.
type Fetcher interface {
	Get(ctx context.Context, url string, o Options) (*Response, error)
}

// Options are one request's.
type Options struct {
	// ETag and LastModified make the request conditional.
	ETag         string
	LastModified string
	// Accept is the Accept header; empty sends */*.
	Accept string
	// MaxBytes caps the body; 0 means MaxPage.
	MaxBytes int64
}

// Response is what a request answered.
type Response struct {
	// Status is the HTTP status.
	Status int
	// URL is the final URL, after redirects.
	URL string
	// Header is the response's headers.
	Header http.Header
	// Body is the body, at most the request's cap.
	Body []byte
	// NotModified is true for a 304 answer to a conditional request.
	NotModified bool
	// ETag and LastModified are the validators to send next time.
	ETag         string
	LastModified string
	// RetryAfter is a 429 or 503 answer's Retry-After, 0 when it has none.
	RetryAfter time.Duration
}

// Client fetches public URLs.
type Client struct {
	h  *http.Client
	tr *http.Transport
	ua string
}

// New returns a Client that sends the User-Agent frostread/version and
// caps each request at timeout (DefaultTimeout when 0).
func New(version string) *Client {
	tr := netguard.Transport(DefaultTimeout)
	c := &Client{tr: tr, ua: "frostread/" + version}
	c.h = &http.Client{Transport: tr, Timeout: DefaultTimeout, CheckRedirect: c.checkRedirect}
	return c
}

// checkRedirect runs before each redirect: its scheme, its userinfo and
// its name are checked, and the count is bounded.
func (c *Client) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > MaxRedirects {
		return fmt.Errorf("more than %d redirects: %w", MaxRedirects, ErrRefused)
	}
	return checkURL(req.URL)
}

// Check reports whether this client may fetch rawurl: an http or https
// scheme, no userinfo, and a name netguard.Name admits. It makes no
// request, so a caller can refuse a URL before it is stored.
func Check(rawurl string) error {
	u, err := url.Parse(rawurl)
	if err != nil {
		return fmt.Errorf("parse %s: %w", rawurl, ErrRefused)
	}
	return checkURL(u)
}

// checkURL refuses a URL that is not http or https, that names a user, or
// whose host netguard.Name refuses.
func checkURL(u *url.URL) error {
	if u == nil {
		return fmt.Errorf("no url: %w", ErrRefused)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("scheme %q is not http or https: %w", u.Scheme, ErrRefused)
	}
	if u.User != nil {
		return fmt.Errorf("a url may not name a user: %w", ErrRefused)
	}
	if err := netguard.Name(u.Hostname()); err != nil {
		return fmt.Errorf("%s: %w", u.Hostname(), err)
	}
	return nil
}

// Get fetches rawurl. A 304 answer is not an error, and neither is a 4xx
// or 5xx status: the caller decides what a status means, because a 429 or
// a 503 carries a Retry-After it should honour. A body over the cap, a
// refused address, and a network fault are errors.
func (c *Client) Get(ctx context.Context, rawurl string, o Options) (*Response, error) {
	u, err := url.Parse(rawurl)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", rawurl, ErrRefused)
	}
	if err := checkURL(u); err != nil {
		return nil, fmt.Errorf("fetch %s: %w", rawurl, err)
	}
	max := o.MaxBytes
	if max <= 0 {
		max = MaxPage
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", rawurl, err)
	}
	req.Header.Set("User-Agent", c.ua)
	if o.Accept != "" {
		req.Header.Set("Accept", o.Accept)
	}
	if o.ETag != "" {
		req.Header.Set("If-None-Match", o.ETag)
	}
	if o.LastModified != "" {
		req.Header.Set("If-Modified-Since", o.LastModified)
	}
	resp, err := c.h.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", rawurl, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out := &Response{
		Status:       resp.StatusCode,
		URL:          resp.Request.URL.String(),
		Header:       resp.Header,
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
	}
	if resp.StatusCode == http.StatusNotModified {
		out.NotModified = true
		out.ETag = first(resp.Header.Get("ETag"), o.ETag)
		out.LastModified = first(resp.Header.Get("Last-Modified"), o.LastModified)
		out.RetryAfter = retryAfter(resp.Header.Get("Retry-After"), time.Now())
		return out, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, fmt.Errorf("fetch %s: read body: %w", rawurl, err)
	}
	if int64(len(body)) > max {
		return nil, fmt.Errorf("fetch %s: over %d bytes: %w", rawurl, max, ErrTooLarge)
	}
	out.Body = body
	out.RetryAfter = retryAfter(resp.Header.Get("Retry-After"), time.Now())
	return out, nil
}

// first returns a, or b when a is empty.
func first(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// retryAfter reads a Retry-After header: seconds, or an HTTP date, capped
// at 24 hours.
func retryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	var d time.Duration
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		d = time.Duration(secs) * time.Second
	} else if t, err := http.ParseTime(v); err == nil {
		d = t.Sub(now)
	}
	if d < 0 {
		return 0
	}
	if d > 24*time.Hour {
		return 24 * time.Hour
	}
	return d
}
