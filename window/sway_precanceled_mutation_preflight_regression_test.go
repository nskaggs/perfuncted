package window

import (
	"context"
	"errors"
	"testing"
)

// A canceled caller no longer has authority to inspect target identifiers or
// validate mutation payloads, much less to dispatch compositor commands.
func TestSwayCanceledMutationsRejectBeforeIdentifierAndGeometryValidation(t *testing.T) {
	m := &SwayManager{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		run  func(context.Context) error
	}{
		{"activate invalid id", func(c context.Context) error { return m.ActivateByID(c, "not-a-window") }},
		{"move invalid coordinate", func(c context.Context) error { return m.MoveByID(c, "1", 1<<40, 0) }},
		{"resize invalid dimensions", func(c context.Context) error { return m.ResizeByID(c, "1", 0, -1) }},
		{"close invalid id", func(c context.Context) error { return m.CloseWindowByID(c, "not-a-window") }},
		{"minimize invalid id", func(c context.Context) error { return m.MinimizeByID(c, "not-a-window") }},
		{"fullscreen invalid id", func(c context.Context) error { return m.FullscreenByID(c, "not-a-window") }},
		{"unfullscreen invalid id", func(c context.Context) error { return m.UnfullscreenByID(c, "not-a-window") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled mutation exposed validation result: %v", err)
			}
		})
	}
}
