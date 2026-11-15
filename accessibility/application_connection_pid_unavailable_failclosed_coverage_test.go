package accessibility

import (
	"context"
	"errors"
	"testing"
)

// A missing application identity or a disconnected accessibility bus cannot
// establish process ownership. In both cases, no PID may be invented from
// stale state or an unrelated process.
func TestApplicationConnectionPIDMissingIdentityAndTransportDoNotInventOwner(t *testing.T) {
	var missing *dbusBackend
	if pid, err := missing.connectionPID(context.Background(), "org.example.Application"); err != nil || pid != 0 {
		t.Fatalf("nil accessibility backend invented process ownership: pid=%d err=%v", pid, err)
	}
	disconnected := &dbusBackend{}
	for _, bus := range []string{"", "   ", "org.example.Application"} {
		if pid, err := disconnected.connectionPID(context.Background(), bus); err != nil || pid != 0 {
			t.Fatalf("unavailable bus %q invented process ownership: pid=%d err=%v", bus, pid, err)
		}
	}
	live, _, _ := newSyntheticATSPIBackend(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if pid, err := live.connectionPID(ctx, registryName); pid != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled PID lookup returned stale application owner: pid=%d err=%v", pid, err)
	}
}
