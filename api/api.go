// Package api is readd's RPC contract: JSON-RPC 2.0, one message per
// line, on lodge's kit (github.com/frostyard/lodge/kit/jsonrpc). rpc.go is
// what a generator writes from schema/rpc, by hand until the app has one;
// this file is the hand-written runtime it relies on.
package api

import (
	"context"
	"encoding/json/jsontext"
	"fmt"
	"net"
	"slices"

	"github.com/frostyard/lodge/kit/jsonrpc"
)

// ErrorCode is a JSON-RPC error code; the constants in rpc.go name them.
type ErrorCode int

// Error is a JSON-RPC error object. Handlers return one to choose the code;
// any other error reaches the client as CodeInternal.
type Error = jsonrpc.Error[ErrorCode]

// Errorf returns an *Error with a formatted message.
func Errorf(code ErrorCode, format string, args ...any) *Error {
	return jsonrpc.Errorf(code, format, args...)
}

// CallerKind is who a connection serves: the private socket's local
// clients, or lodge's callers. Each method admits some.
type CallerKind string

const (
	// CallerLocal is a process of the same user on readd's private socket.
	CallerLocal CallerKind = "local"
	// CallerSession is a browser through lodge's /app/read/api.
	CallerSession CallerKind = "session"
	// CallerAgent is an MCP session through lodge's /mcp/read.
	CallerAgent CallerKind = "agent"
)

type callerKey struct{}

// WithCaller returns ctx carrying the connection's caller kind; the RPC
// server sets it for every connection.
func WithCaller(ctx context.Context, k CallerKind) context.Context {
	return context.WithValue(ctx, callerKey{}, k)
}

// CallerFrom returns the caller kind in ctx, or "", which admits nothing.
func CallerFrom(ctx context.Context) CallerKind {
	k, _ := ctx.Value(callerKey{}).(CallerKind)
	return k
}

type handlerFunc = jsonrpc.HandlerFunc

// Router maps wire method names to services, with the callers each admits.
// A method that does not exist, or does not admit the caller, answers
// methodNotFound alike.
type Router struct {
	*jsonrpc.Router[CallerKind, ErrorCode]
}

// NewRouter registers every service; a nil service is an error.
func NewRouter(s Services) (*Router, error) {
	r := &Router{jsonrpc.NewRouter[CallerKind, ErrorCode](jsonrpc.RouterOptions[CallerKind]{Caller: CallerFrom})}
	if err := s.register(r); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Router) handle(method string, callers []CallerKind, h handlerFunc) {
	r.Handle(method, jsonrpc.Route[CallerKind]{Callers: callers, Handler: h})
}

func decodeParams(raw jsontext.Value, p any, required []string) error {
	return jsonrpc.DecodeParams[ErrorCode](raw, p, required)
}

// HasMethod reports whether the contract defines method.
func HasMethod(method string) bool { return slices.Contains(Methods, method) }

// Client is a readd connection, safe for concurrent use.
type Client struct {
	*jsonrpc.Client[ErrorCode]
}

// NewClient starts a client on an established connection: lodge's /api
// WebSocket, or the private socket. The caller calls rpc.hello first.
func NewClient(conn net.Conn) *Client {
	return &Client{jsonrpc.NewClientWith[ErrorCode](conn, jsonrpc.ClientOptions{Name: "readd"})}
}

// Dial connects to readd's private socket and completes rpc.hello.
func Dial(ctx context.Context, socket, client string) (*Client, *Hello, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to readd: %w", err)
	}
	c := NewClient(conn)
	h, err := c.RPC().Hello(ctx, &RPCHelloParams{Protocol: Protocol, Client: client})
	if err != nil {
		_ = c.Close()
		return nil, nil, err
	}
	return c, h, nil
}
