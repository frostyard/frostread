// Package rpcserver serves readd's API on lodge's kit: rpc.hello, which
// readies a connection, rpc.ping, and events.subscribe, which makes a
// session's connection receive items.changed. The kit does the framing,
// the gating on rpc.hello, concurrent dispatch, panics and oversized
// replies.
package rpcserver

import (
	"context"
	"encoding/json/v2"
	"log/slog"
	"net"
	"sync"

	"github.com/frostyard/lodge/kit/jsonrpc"

	"github.com/frostyard/frostread/api"
)

// Options configures a Server.
type Options struct {
	// Name is reported by rpc.hello, such as "readd v0.1.0".
	Name   string
	Logger *slog.Logger
}

// Server implements api.RPCService and api.EventsService, and serves a
// Router built with it.
type Server struct {
	opts Options
	rpc  *jsonrpc.Server[api.ErrorCode]

	mu   sync.Mutex
	subs map[*jsonrpc.Conn]struct{}
}

var (
	_ api.RPCService    = (*Server)(nil)
	_ api.EventsService = (*Server)(nil)
)

// New returns a Server.
func New(opts Options) *Server {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	return &Server{opts: opts, subs: map[*jsonrpc.Conn]struct{}{},
		rpc: jsonrpc.NewServer[api.ErrorCode](jsonrpc.ServerOptions{Hello: "rpc.hello", Logger: opts.Logger})}
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

// Subscribe implements events.subscribe: changes reach this connection
// from the answer on. It subscribes before it answers, so a change made as
// soon as the client sees the answer is not missed; the client reads a
// notification that comes early as readily as one that comes late.
func (s *Server) Subscribe(ctx context.Context, _ *api.EventsSubscribeParams) (*api.EventsSubscribeResult, error) {
	c := jsonrpc.ConnFrom(ctx)
	if c == nil {
		return nil, api.Errorf(api.CodeInvalidRequest, "events need a connection")
	}
	s.mu.Lock()
	s.subs[c] = struct{}{}
	s.mu.Unlock()
	c.OnClose(func() {
		s.mu.Lock()
		delete(s.subs, c)
		s.mu.Unlock()
	})
	return &api.EventsSubscribeResult{Subscribed: true}, nil
}

// Broadcast sends items.changed to every subscribed connection. It never
// blocks, so a change made while a client is slow is not held up.
func (s *Server) Broadcast(views []string) {
	if len(views) == 0 {
		return
	}
	s.mu.Lock()
	conns := make([]*jsonrpc.Conn, 0, len(s.subs))
	for c := range s.subs {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	raw, err := json.Marshal(api.Changed{Views: views})
	if err != nil {
		s.opts.Logger.Error("encode a change", "err", err)
		return
	}
	e := jsonrpc.EventEnvelope{Event: api.EventMethod, Data: raw}
	for _, c := range conns {
		if err := c.Notify(jsonrpc.EventMethod, e); err != nil {
			s.opts.Logger.Debug("a change was not delivered", "err", err)
		}
	}
}
