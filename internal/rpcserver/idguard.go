package rpcserver

// The id guard: readd refuses a request whose id is too long to answer
// under the bound on items.render's answer, before the kit dispatches it.
// The kit hands a handler no request id, so a handler cannot measure the
// answer's envelope itself (ADR-0008, The answer stays under the
// transport's limit; docs/specs/api.md, Rendering for the desktop).

import (
	"bufio"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"io"
	"log/slog"
	"net"

	"github.com/frostyard/lodge/kit/jsonrpc"

	"github.com/frostyard/frostread/internal/content"
)

// idless is the line the kit answers invalidRequest to, with id null: a
// request that is not valid JSON-RPC, because it has no method.
const idless = `{"jsonrpc":"2.0","id":null}`

// guardConn refuses a request whose encoded id is longer than
// content.MaxRequestID, and hands every other line to the kit unchanged. It
// reads the connection line by line, as the kit's own connection does.
type guardConn struct {
	net.Conn
	log  *slog.Logger
	sc   *bufio.Scanner
	rest []byte // what the current line still has to give
}

// newGuardConn wraps nc, whose lines the kit reads through Read.
func newGuardConn(nc net.Conn, log *slog.Logger) *guardConn {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	c := &guardConn{Conn: nc, log: log, sc: bufio.NewScanner(nc)}
	c.sc.Buffer(make([]byte, 64<<10), jsonrpc.MaxMessageSize)
	return c
}

// Read answers the next bytes of the connection's lines, as the kit sees
// them: one line at a time when p is small, and several when it is not.
func (c *guardConn) Read(p []byte) (int, error) {
	for len(c.rest) == 0 {
		if !c.sc.Scan() {
			// A line over jsonrpc.MaxMessageSize ends the connection, as
			// it does for the kit's own reader.
			if err := c.sc.Err(); err != nil {
				return 0, err
			}
			return 0, io.EOF
		}
		c.rest = c.line(c.sc.Bytes())
	}
	n := copy(p, c.rest)
	c.rest = c.rest[n:]
	return n, nil
}

// line is what the kit sees for one line of the client's: the line as it
// came, its newline included, or an id-less request in the place of one
// whose id is too long to answer under the bound.
func (c *guardConn) line(b []byte) []byte {
	var req struct {
		ID jsontext.Value `json:"id"`
	}
	// A line that does not parse reaches the kit as it came, which answers
	// parseError.
	if err := json.Unmarshal(b, &req); err == nil && len(req.ID) > content.MaxRequestID {
		c.log.Info("a request's id is too long to answer under the bound", "bytes", len(req.ID))
		return []byte(idless + "\n")
	}
	// The scanner reuses its buffer for the next line.
	line := make([]byte, len(b)+1)
	copy(line, b)
	line[len(b)] = '\n'
	return line
}

// guardListener wraps every connection a listener accepts, so the kit
// dispatches no request with a longer id. Accept stays the listener's own,
// which is where the private socket checks who connects.
type guardListener struct {
	net.Listener
	log *slog.Logger
}

// Accept accepts a connection, and guards its lines.
func (l guardListener) Accept() (net.Conn, error) {
	nc, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return newGuardConn(nc, l.log), nil
}
