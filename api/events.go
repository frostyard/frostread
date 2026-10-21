// What a generator writes from schema/rpc/events.yaml, written by hand
// until the app has one.

package api

import (
	"context"
	"encoding/json/jsontext"

	"github.com/frostyard/lodge/kit/jsonrpc"
)

// EventMethod is the event a change arrives as: a notification whose
// params are the kit's EventEnvelope, its Event this name and its Data a
// Changed.
const EventMethod = "items.changed"

// Changed names the views a change affects.
type Changed struct {
	Views []string `json:"views"`
}

// EventsSubscribeParams holds the params of events.subscribe.
type EventsSubscribeParams struct{}

// EventsSubscribeResult is events.subscribe's result.
type EventsSubscribeResult struct {
	Subscribed bool `json:"subscribed"`
}

// EventsService sends changes to a subscribed connection.
type EventsService interface {
	Subscribe(ctx context.Context, p *EventsSubscribeParams) (*EventsSubscribeResult, error)
}

func registerEvents(r *Router, s EventsService) {
	r.handle("events.subscribe", []CallerKind{CallerLocal, CallerSession},
		func(ctx context.Context, raw jsontext.Value) (any, error) {
			var p EventsSubscribeParams
			if err := decodeParams(raw, &p, nil); err != nil {
				return nil, err
			}
			return s.Subscribe(ctx, &p)
		})
}

// EventsClient calls the events methods.
type EventsClient struct{ c *Client }

// Events returns the events methods.
func (c *Client) Events() EventsClient { return EventsClient{c} }

// Subscribe calls events.subscribe.
func (x EventsClient) Subscribe(ctx context.Context) error {
	var out EventsSubscribeResult
	if err := x.c.Call(ctx, "events.subscribe", &EventsSubscribeParams{}, &out); err != nil {
		return err
	}
	if !out.Subscribed {
		return Errorf(CodeNotReady, "the server did not subscribe this connection")
	}
	return nil
}

// Notifications returns a connection's notifications, such as
// items.changed, whose params are a Changed.
func (c *Client) Notifications() <-chan jsonrpc.EventEnvelope { return c.Client.Notifications() }
