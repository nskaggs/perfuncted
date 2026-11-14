package screen

import (
	"context"
	"errors"
	"image"
	"strings"
	"testing"
)

// A portal connection is required for each hash entrypoint, just as for a
// screenshot. A missing backend cannot return a synthetic hash of zero as
// success, and pre-canceled requests must not attempt portal access.
func TestPortalHashEntryPointsFailClosedWithoutCaptureAuthority(t *testing.T) {
	for _, tc := range []struct {
		name         string
		backend      *PortalDBusBackend
		ctx          context.Context
		want         string
		wantCanceled bool
	}{
		{name: "nil backend", want: "not initialised"},
		{name: "disconnected backend", backend: &PortalDBusBackend{}, want: "not initialised"},
		{name: "pre-canceled backend", backend: &PortalDBusBackend{}, ctx: canceledPortalHashContext(), wantCanceled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := tc.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			for _, operation := range []struct {
				name string
				call func() (uint32, error)
			}{
				{name: "full hash", call: func() (uint32, error) { return tc.backend.GrabFullHash(ctx) }},
				{name: "region hash", call: func() (uint32, error) { return tc.backend.GrabRegionHash(ctx, image.Rect(1, 2, 3, 4)) }},
			} {
				t.Run(operation.name, func(t *testing.T) {
					hash, err := operation.call()
					if hash != 0 || err == nil {
						t.Fatalf("unauthorized hash returned hash=%08x err=%v", hash, err)
					}
					if tc.wantCanceled {
						if !errors.Is(err, context.Canceled) {
							t.Fatalf("cancelled hash returned %v", err)
						}
					} else if !strings.Contains(err.Error(), tc.want) {
						t.Fatalf("unauthorized hash error=%v, want %q", err, tc.want)
					}
				})
			}
		})
	}
}

func canceledPortalHashContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
