package output

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// A canceled registry bind must not add an output, invoke the optional XDG
// binder, or retain the newly registered proxy.
func TestWaylandBindAddedOutputsRejectsCanceledRegistryBindWithoutPublishing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	connection := &wl.Context{}
	registry := &wl.Registry{}
	connection.Register(registry)
	lister := &WaylandLister{session: &wl.Session{Ctx: connection, Registry: registry}}
	event := wl.GlobalEvent{Name: 55, Interface: "wl_output", Version: 4}
	output := newWaylandOutput(event)
	xdgCalls := 0
	errs := lister.bindAddedOutputsWith(ctx, []*waylandOutput{output}, map[uint32]wl.GlobalEvent{55: event}, func(context.Context, *waylandOutput) error {
		xdgCalls++
		return nil
	})
	if len(errs) != 1 || !errors.Is(errs[0], context.Canceled) {
		t.Fatalf("canceled registry bind errors = %v, want context.Canceled", errs)
	}
	if len(lister.outputs) != 0 || output.proxy != nil || xdgCalls != 0 {
		t.Fatalf("failed registry bind published output/proxy or invoked XDG: outputs=%d proxy=%v xdgCalls=%d", len(lister.outputs), output.proxy, xdgCalls)
	}
}
