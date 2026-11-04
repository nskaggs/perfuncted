package input

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type cancelDuringPortalPreflight struct {
	context.Context //nolint:containedctx // deterministic canceled-provider boundary, not a stored operation.
	cancel          context.CancelFunc
	once            sync.Once
}

func (c *cancelDuringPortalPreflight) Err() error {
	err := c.Context.Err()
	c.once.Do(c.cancel)
	return err
}

// A zero-distance scroll is a no-op, but its return is still an acknowledgement
// of the caller operation. Cancellation at admission must win over success.
func TestPortalCanceledZeroScrollDoesNotAcknowledgeSuccess(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*RemoteDesktopBackend, context.Context) error
	}{
		{"up", func(b *RemoteDesktopBackend, ctx context.Context) error { return b.ScrollUp(ctx, 0) }},
		{"down", func(b *RemoteDesktopBackend, ctx context.Context) error { return b.ScrollDown(ctx, 0) }},
		{"left", func(b *RemoteDesktopBackend, ctx context.Context) error { return b.ScrollLeft(ctx, 0) }},
		{"right", func(b *RemoteDesktopBackend, ctx context.Context) error { return b.ScrollRight(ctx, 0) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := syntheticQueuedPortal()
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &cancelDuringPortalPreflight{Context: base, cancel: cancel}
			if err := tc.run(b, ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("scroll acknowledged a canceled operation: %v", err)
			}
		})
	}
}

// The EIS authorized coordinate system is session-private; a canceled query
// may not disclose its successful metadata even if authority was valid at entry.
func TestPortalCanceledCoordinateMetadataDoesNotPublish(t *testing.T) {
	b := syntheticQueuedPortal()
	b.sender.pointers = []eiPointerDevice{{device: 11, pointer: 22, regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}}}}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelDuringPortalPreflight{Context: base, cancel: cancel}
	space, err := b.PointerCoordinateSpace(ctx)
	if !errors.Is(err, context.Canceled) || space.Kind != "" {
		t.Fatalf("canceled metadata query returned %+v, %v", space, err)
	}
}
