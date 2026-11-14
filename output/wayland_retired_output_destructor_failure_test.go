package output

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// Shutdown must not silently discard destructor failures from a compositor
// transport. All retired outputs must still lose their proxy registrations,
// even when the transport refuses to send their destruction requests.
func TestWaylandRetiredOutputsPropagateTransportCancellationAndReleaseProxies(t *testing.T) {
	connection := &wl.Context{}
	makeOutput := func(global uint32, version uint32, withXDG bool) *waylandOutput {
		out := newWaylandOutput(wl.GlobalEvent{Name: global, Interface: "wl_output", Version: version})
		proxy := &wl.RawProxy{}
		connection.Register(proxy)
		out.proxy = proxy
		if withXDG {
			xdg := &wl.RawProxy{}
			connection.Register(xdg)
			out.xdgProxy = xdg
		}
		return out
	}
	first := makeOutput(11, 4, true)
	second := makeOutput(12, 2, false)
	lister := &WaylandLister{session: &wl.Session{Ctx: connection}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	errs := lister.releaseRemovedOutputs(ctx, []*waylandOutput{first, second})
	if len(errs) != 1 || !errors.Is(errs[0], context.Canceled) {
		t.Fatalf("retired Wayland destructor failures = %v, want one cancellation error", errs)
	}
	// Each retired output must relinquish its registration even on a failed
	// protocol write, so a late compositor event cannot mutate retired state.
	for _, proxy := range []*wl.RawProxy{first.proxy, first.xdgProxy, second.proxy} {
		if proxy.ID() == 0 {
			t.Fatal("test proxy had no registered identity")
		}
		replacement := &wl.RawProxy{}
		connection.SetProxy(proxy.ID(), replacement)
		if replacement.ID() != proxy.ID() {
			t.Fatalf("retired proxy %d still holds its registry identity", proxy.ID())
		}
	}
}
