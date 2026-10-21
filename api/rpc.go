// What a generator writes from schema/rpc, written by hand until the app
// has one. Keep it in step with schema/rpc; once a generator writes
// zz_generated.go, delete this file.

package api

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
)

// Protocol is the protocol major version this package speaks.
const Protocol = 1

// Error codes (schema/rpc/errors.yaml). JSON-RPC 2.0 defines the negative
// ones; 1001 to 1005 are what every lodge app means by them.
const (
	CodeParseError       ErrorCode = -32700
	CodeInvalidRequest   ErrorCode = -32600
	CodeMethodNotFound   ErrorCode = -32601
	CodeInvalidParams    ErrorCode = -32602
	CodeInternal         ErrorCode = -32603
	CodeNotFound         ErrorCode = 1001
	CodeConflict         ErrorCode = 1002
	CodeProtocolMismatch ErrorCode = 1003
	CodeNotReady         ErrorCode = 1004
	CodeUnavailable      ErrorCode = 1005
)

// Sentinels for errors.Is.
var (
	ErrMethodNotFound   = &Error{Code: CodeMethodNotFound}
	ErrInternal         = &Error{Code: CodeInternal}
	ErrNotFound         = &Error{Code: CodeNotFound}
	ErrProtocolMismatch = &Error{Code: CodeProtocolMismatch}
	ErrNotReady         = &Error{Code: CodeNotReady}
)

// String is the code's schema name.
func (c ErrorCode) String() string {
	switch c {
	case CodeParseError:
		return "parseError"
	case CodeInvalidRequest:
		return "invalidRequest"
	case CodeMethodNotFound:
		return "methodNotFound"
	case CodeInvalidParams:
		return "invalidParams"
	case CodeInternal:
		return "internal"
	case CodeNotFound:
		return "notFound"
	case CodeConflict:
		return "conflict"
	case CodeProtocolMismatch:
		return "protocolMismatch"
	case CodeNotReady:
		return "notReady"
	case CodeUnavailable:
		return "unavailable"
	}
	return fmt.Sprintf("ErrorCode(%d)", int(c))
}

// ProtocolMismatch returns an error with CodeProtocolMismatch.
func ProtocolMismatch(format string, args ...any) *Error {
	return Errorf(CodeProtocolMismatch, format, args...)
}

// ---- rpc ----

// Hello is the server's identity and the negotiated protocol.
type Hello struct {
	// Protocol major version the server speaks.
	Protocol int64 `json:"protocol"`
	// Server name and version, for logs.
	Server string `json:"server"`
	// The connection's caller.
	Caller CallerKind `json:"caller"`
}

// RPCHelloParams holds the params of rpc.hello.
type RPCHelloParams struct {
	// Protocol major version the client speaks.
	Protocol int64 `json:"protocol"`
	// Client name and version, for logs.
	Client string `json:"client"`
}

// RPCPingParams holds the params of rpc.ping.
type RPCPingParams struct{}

// RPCService is connection setup: rpc.hello first on every connection,
// and rpc.ping, which a web client sends at least every 30 s while it is
// visible, so lodge keeps its session.
type RPCService interface {
	Hello(ctx context.Context, p *RPCHelloParams) (*Hello, error)
	Ping(ctx context.Context, p *RPCPingParams) error
}

func registerRPC(r *Router, s RPCService) {
	callers := []CallerKind{CallerLocal, CallerSession, CallerAgent}
	r.handle("rpc.hello", callers, func(ctx context.Context, raw jsontext.Value) (any, error) {
		var p RPCHelloParams
		if err := decodeParams(raw, &p, []string{"protocol", "client"}); err != nil {
			return nil, err
		}
		return s.Hello(ctx, &p)
	})
	r.handle("rpc.ping", callers, func(ctx context.Context, raw jsontext.Value) (any, error) {
		var p RPCPingParams
		if err := decodeParams(raw, &p, nil); err != nil {
			return nil, err
		}
		return nil, s.Ping(ctx, &p)
	})
}

// RPCClient calls the rpc methods; it implements RPCService.
type RPCClient struct{ c *Client }

// RPC returns the rpc methods.
func (c *Client) RPC() RPCClient { return RPCClient{c} }

var _ RPCService = RPCClient{}

// Hello calls rpc.hello.
func (x RPCClient) Hello(ctx context.Context, p *RPCHelloParams) (*Hello, error) {
	var r Hello
	if err := x.c.Call(ctx, "rpc.hello", p, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Ping calls rpc.ping.
func (x RPCClient) Ping(ctx context.Context, p *RPCPingParams) error {
	return x.c.Call(ctx, "rpc.ping", p, nil)
}

// Services is every domain's implementation.
type Services struct {
	RPC RPCService
}

func (s Services) register(r *Router) error {
	if s.RPC == nil {
		return errors.New("api: Services.RPC is nil")
	}
	registerRPC(r, s.RPC)
	return nil
}

// Methods is every method the contract defines.
var Methods = []string{"rpc.hello", "rpc.ping"}
