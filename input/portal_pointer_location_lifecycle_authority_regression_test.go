package input

import (
	"context"
	"errors"
	"testing"
)

// Even unsupported queries must report their operation lifetime truthfully:
// canceled callers and revoked authorization take precedence over capability.
func TestRemoteDesktopPointerLocationHonorsCancellationAndAuthorization(t *testing.T) {
	b, _, _ := newSyntheticRevocableRemoteDesktop(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want error
	}{
		{"canceled", ctx, context.Canceled},
		{"live unsupported", context.Background(), ErrNotSupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x, y, err := b.PointerLocation(tc.ctx)
			if x != 0 || y != 0 || !errors.Is(err, tc.want) {
				t.Fatalf("position (%d,%d) error %v, want %v", x, y, err, tc.want)
			}
		})
	}
	if _, _, err := b.PointerLocation(nil); err == nil {
		t.Fatal("nil context accepted")
	}
	if err := b.revoke(errors.New("synthetic authorization retired")); !errors.Is(err, ErrRemoteDesktopRevoked) {
		t.Fatalf("revocation should report lost authorization, got %v", err)
	}
	if x, y, err := b.PointerLocation(context.Background()); x != 0 || y != 0 || !errors.Is(err, ErrRemoteDesktopRevoked) {
		t.Fatalf("revoked pointer query (%d,%d): %v", x, y, err)
	}
}
