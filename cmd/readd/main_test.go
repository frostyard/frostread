package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"testing"

	"github.com/frostyard/lodge/kit/kittest"
	"github.com/frostyard/lodge/kit/manifest"
	"github.com/frostyard/lodge/kit/rpctest"

	"github.com/frostyard/frostread/api"
)

func start(t *testing.T) (*app, *kittest.Lodge) {
	t.Helper()
	a, err := newApp(slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	return a, kittest.Start(t, ID, a.lodge)
}

// The manifest lodge installs is valid, and names this daemon.
func installed(t *testing.T) manifest.Manifest {
	t.Helper()
	data, err := os.ReadFile("../../packaging/lodge/app.json")
	if err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != ID || m.Contract != manifest.Contract {
		t.Fatalf("app.json names %q, contract %d", m.ID, m.Contract)
	}
	return m
}

// The socket keeps the app contract's rules, as lodge sees it.
func TestSocketRules(t *testing.T) {
	_, l := start(t)
	kittest.CheckRules(t, l, installed(t))
}

// A browser session's /api speaks the contract, behind rpc.hello.
func TestAPI(t *testing.T) {
	_, l := start(t)
	nc, err := l.DialAPI(t.Context(), kittest.Session())
	if err != nil {
		t.Fatal(err)
	}
	c := api.NewClient(nc)
	defer c.Close()
	if err := c.RPC().Ping(t.Context(), &api.RPCPingParams{}); !errors.Is(err, api.ErrNotReady) {
		t.Errorf("ping before hello = %v, want notReady", err)
	}
	h, err := c.RPC().Hello(t.Context(), &api.RPCHelloParams{Protocol: api.Protocol, Client: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if h.Caller != api.CallerSession || h.Protocol != api.Protocol {
		t.Errorf("hello = %+v, want a session", h)
	}
	if err := c.RPC().Ping(t.Context(), &api.RPCPingParams{}); err != nil {
		t.Errorf("ping: %v", err)
	}
}

// The private socket serves local clients.
func TestPrivateSocket(t *testing.T) {
	a, _ := start(t)
	run := rpctest.Serve(t, func(ctx context.Context, ln net.Listener) error { return a.rpc.Serve(ctx, ln, a.router) })
	c, h, err := api.Dial(t.Context(), run.Socket, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if h.Caller != api.CallerLocal {
		t.Errorf("hello on the private socket = %+v, want local", h)
	}
}
