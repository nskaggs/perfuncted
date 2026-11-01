package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/accessibility"
)

// The wrapper exposes only the core Backend interface, even when its
// implementation supports optional capabilities. Optional dispatch must
// use the exposed interface, not assume a concrete provider has methods.
type coreOnlyAccessibilityBackend struct{ accessibility.Backend }

func TestAccessibilityOwnerOptionalProviderAbsenceFailsClosed(t *testing.T) {
	underlying := newOwnerLifecycleBackend(9, "applications", "find")
	owner := newAccessibilityBackendOwner(nil)
	owner.install(&coreOnlyAccessibilityBackend{Backend: underlying})
	defer owner.Close()
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"FindApplication", func() error { _, err := owner.FindApplication(ctx, accessibility.ApplicationFilter{}); return err }},
		{"FindApplicationFresh", func() error { _, err := owner.FindApplicationFresh(ctx, accessibility.ApplicationFilter{}); return err }},
		{"ResolveWindow", func() error { _, err := owner.ResolveWindow(ctx, accessibility.WindowTarget{}); return err }},
		{"ResolveWindowFresh", func() error { _, err := owner.ResolveWindowFresh(ctx, accessibility.WindowTarget{}); return err }},
		{"Events", func() error {
			stream, err := owner.Events(ctx, accessibility.EventOptions{})
			if stream != nil {
				t.Errorf("unsupported event source returned live stream")
			}
			return err
		}},
		{"Reopen", func() error { return owner.reopen(ctx) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, accessibility.ErrUnsupported) {
				t.Fatalf("missing optional interface returned %v", err)
			}
		})
	}
	if gen := owner.Generation(); gen != 0 {
		t.Fatalf("unsupported generation source claimed generation %d", gen)
	}
	owner.Invalidate(accessibility.NodeID{BusName: "org.example.NotOwned"})
	if underlying.generation.Load() != 9 {
		t.Fatal("unsupported generation invalidation reached wrapped provider")
	}
	if _, err := owner.Applications(ctx); err != nil {
		t.Fatalf("core Backend operation incorrectly rejected: %v", err)
	}
}

func TestAccessibilityOwnerSingleGenerationEventSourceFailuresAndSuccess(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*ownerLifecycleBackend)
		want      error
	}{
		{"provider error", func(b *ownerLifecycleBackend) { b.eventErr = errors.New("synthetic provider stream failure") }, errors.New("synthetic provider stream failure")},
		{"nil stream", func(b *ownerLifecycleBackend) { b.eventNil = true }, accessibility.ErrDisconnected},
		{"valid stream", func(*ownerLifecycleBackend) {}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newOwnerLifecycleBackend(4, "events")
			tc.configure(b)
			owner := newAccessibilityBackendOwner(nil)
			owner.install(b)
			defer owner.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			stream, err := owner.Events(ctx, accessibility.EventOptions{})
			fixture := awaitOwnerSignal(t, b.eventReady, "owner event source startup")
			if tc.want != nil {
				if err == nil || stream != nil {
					t.Fatalf("failed subscription published stream=%v err=%v", stream, err)
				}
				if tc.name == "nil stream" && !errors.Is(err, accessibility.ErrDisconnected) {
					t.Fatalf("nil stream error=%v", err)
				}
			} else {
				if err != nil || stream == nil || fixture.events == nil {
					t.Fatalf("normal event source stream=%v err=%v fixture=%+v", stream, err, fixture)
				}
				fixture.events <- accessibility.Event{Kind: "synthetic-event"}
				select {
				case event := <-stream:
					if event.Kind != "synthetic-event" {
						t.Fatalf("wrong event %+v", event)
					}
				case <-ctx.Done():
					t.Fatal("event not delivered")
				}
			}
		})
	}
}
