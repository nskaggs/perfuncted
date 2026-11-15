//go:build linux

package gnomebridge

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

// Capability admission is based on the negotiated bridge's advertised
// abilities, not merely its presence. An authorized connection remains owned
// by the caller, while transport failure cannot invent a live client.
func TestBridgeCapabilityConnectionAdmitsOnlyAdvertisedLiveClient(t *testing.T) {
	rt := env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=unix:path=/fixture"})
	connected := &Client{caps: []string{CapabilityWindows}}
	calls := 0
	got, err := connectForCapability(context.Background(), rt, CapabilityWindows,
		func(ctx context.Context, observed env.Runtime) (*Client, error) {
			calls++
			if ctx.Err() != nil || observed.Get("DBUS_SESSION_BUS_ADDRESS") != "unix:path=/fixture" {
				t.Fatalf("wrong admission context or runtime: %v %+v", ctx.Err(), observed)
			}
			return connected, nil
		},
	)
	if err != nil || got != connected || connected.closed || calls != 1 {
		t.Fatalf("authorized bridge was lost: got=%p err=%v closed=%v calls=%d", got, err, connected.closed, calls)
	}
	if err := got.Close(); err != nil {
		t.Fatalf("caller unable to close admitted connection: %v", err)
	}
	refused := errors.New("synthetic failed private D-Bus negotiation")
	got, err = connectForCapability(context.Background(), rt, CapabilityWindows,
		func(context.Context, env.Runtime) (*Client, error) { return nil, refused },
	)
	if got != nil || !errors.Is(err, refused) {
		t.Fatalf("failed negotiation authorized a connection: got=%p err=%v", got, err)
	}
}
