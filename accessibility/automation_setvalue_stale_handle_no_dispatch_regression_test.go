package accessibility

import (
	"context"
	"errors"
	"testing"
)

// A finite SetValue request is not authority to mutate an obsolete or
// disconnected AT-SPI node. Reject revoked handles before invoking the
// provider, even through the testable mutation transport override.
func TestSetValueRefusesRetiredHandlesBeforeProviderDispatch(t *testing.T) {
	live := NodeID{BusName: "org.example.Widget", ObjectPath: "/entry", Generation: 7}
	stale := live
	stale.Generation = 6
	malformed := live
	malformed.ObjectPath = "not-an-object-path"
	for _, tc := range []struct {
		name                 string
		id                   NodeID
		closed, disconnected bool
		want                 error
	}{
		{name: "stale generation", id: stale, want: ErrStaleNode},
		{name: "malformed path", id: malformed, want: ErrStaleNode},
		{name: "closed backend", id: live, closed: true, want: ErrDisconnected},
		{name: "disconnected backend", id: live, disconnected: true, want: ErrDisconnected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			b := &dbusBackend{
				generation:   7,
				closed:       tc.closed,
				disconnected: tc.disconnected,
				callOverride: func(context.Context, NodeID, string, []any) (any, error) {
					calls++
					return nil, nil
				},
			}
			err := b.SetValue(context.Background(), tc.id, 3.25)
			if !errors.Is(err, tc.want) || calls != 0 {
				t.Fatalf("retired handle mutation result=%v, want %v; provider dispatch count=%d", err, tc.want, calls)
			}
		})
	}
}
