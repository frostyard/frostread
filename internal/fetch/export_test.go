package fetch

import (
	"context"
	"net"
)

// SetDialerForTest points c at dial for every connection, so a test can
// reach its httptest server while the address check still runs on the URL.
// It exists only in the test build: a production client keeps netguard's
// dialer.
func SetDialerForTest(c *Client, dial func(ctx context.Context, network, addr string) (net.Conn, error)) {
	c.tr.DialContext = dial
}
