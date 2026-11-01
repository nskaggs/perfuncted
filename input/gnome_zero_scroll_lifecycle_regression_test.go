//go:build linux

package input

import (
	"context"
	"errors"
	"testing"
)

func TestGnomeNativeZeroScrollHonorsCancellationAndUnavailableBridge(t *testing.T) {
	for _, direction := range []struct {
		name string
		run  func(*GnomeNativeBackend, context.Context) error
	}{
		{"up", func(b *GnomeNativeBackend, ctx context.Context) error { return b.ScrollUp(ctx, 0) }},
		{"down", func(b *GnomeNativeBackend, ctx context.Context) error { return b.ScrollDown(ctx, 0) }},
		{"left", func(b *GnomeNativeBackend, ctx context.Context) error { return b.ScrollLeft(ctx, 0) }},
		{"right", func(b *GnomeNativeBackend, ctx context.Context) error { return b.ScrollRight(ctx, 0) }},
	} {
		t.Run(direction.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			bridge := &lifecycleGnomeBridge{}
			backend := &GnomeNativeBackend{bridge: bridge}
			if err := direction.run(backend, ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled no-op scroll acknowledged: %v", err)
			}
			if err := direction.run(&GnomeNativeBackend{}, context.Background()); err == nil {
				t.Fatal("unavailable bridge acknowledged no-op scroll")
			}
			if err := direction.run(backend, context.Background()); err != nil {
				t.Fatalf("valid zero scroll rejected: %v", err)
			}
			if len(bridge.log) != 0 || len(bridge.events) != 0 {
				t.Fatalf("zero scroll injected events: log=%v keys=%v", bridge.log, bridge.events)
			}
		})
	}
}
