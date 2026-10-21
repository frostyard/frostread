// Package rpcserver serves readd's API on lodge's kit: rpc.hello, which
// readies a connection, and rpc.ping. The kit does the framing, the gating
// on rpc.hello, concurrent dispatch, panics and oversized replies.
package rpcserver

import (
	"context"
	"log/slog"
	"net"

	"github.com/frostyard/lodge/kit/jsonrpc"

	"github.com/frostyard/frostread/api"
)

// Options configures a Server.
type Options struct {
	// Name is reported by rpc.hello, such as "readd v0.1.0".
	Name   string
	Logger *slog.Logger
}

// Server implements api.RPCService, and serves a Router built with it.
type Server struct {
	opts Options
	rpc  *jsonrpc.Server[api.ErrorCode]
}

var _ api.RPCService = (*Server)(nil)

// New returns a Server.
func New(opts Options) *Server {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	return &Server{opts: opts, rpc: jsonrpc.NewServer[api.ErrorCode](jsonrpc.ServerOptions{
		Hello: "rpc.hello", Logger: opts.Logger})}
}

// Serve accepts connections on the private socket (jsonrpc.Listen) until
// ctx ends, each as a local caller.
func (s *Server) Serve(ctx context.Context, ln net.Listener, r *api.Router) error {
	return s.rpc.Serve(ctx, ln, r, func(ctx context.Context) context.Context {
		return api.WithCaller(ctx, api.CallerLocal)
	})
}

// ServeConn serves one connection whose listener has authenticated it as
// caller: lodge's /api as a session.
func (s *Server) ServeConn(ctx context.Context, nc net.Conn, r *api.Router, caller api.CallerKind) {
	s.rpc.ServeConn(api.WithCaller(ctx, caller), nc, r)
}

// Hello implements rpc.hello.
func (s *Server) Hello(ctx context.Context, p *api.RPCHelloParams) (*api.Hello, error) {
	if p.Protocol != api.Protocol {
		return nil, api.ProtocolMismatch("readd speaks protocol %d, not %d", api.Protocol, p.Protocol)
	}
	caller := api.CallerFrom(ctx)
	if c := jsonrpc.ConnFrom(ctx); c != nil {
		c.SetReady(p.Client)
		s.opts.Logger.Debug("client connected", "client", p.Client, "caller", caller)
	}
	return &api.Hello{Protocol: api.Protocol, Server: s.opts.Name, Caller: caller}, nil
}

// Ping implements rpc.ping.
func (s *Server) Ping(context.Context, *api.RPCPingParams) error { return nil }
