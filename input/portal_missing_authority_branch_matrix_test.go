//go:build linux

package input

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRemoteDesktopPublicOperationAuthorizationFailureMatrix(t *testing.T) {
	for _, tc := range []struct {
		name     string
		disable  func(*eiSender)
		call     func(*RemoteDesktopBackend) error
		want     error
		contains string
	}{
		{"keyup without keyboard object", func(s *eiSender) { s.keyboard = 0 }, func(b *RemoteDesktopBackend) error { return b.KeyUp(context.Background(), "a") }, ErrNotSupported, ""},
		{"keyup without keyboard device", func(s *eiSender) { s.keyboardDev = 0 }, func(b *RemoteDesktopBackend) error { return b.KeyUp(context.Background(), "a") }, ErrNotSupported, ""},
		{"type literal without text device", func(s *eiSender) { s.textDev = 0 }, func(b *RemoteDesktopBackend) error { return b.TypeLiteral(context.Background(), "private") }, ErrNotSupported, ""},
		{"type literal without text object", func(s *eiSender) { s.text = 0 }, func(b *RemoteDesktopBackend) error { return b.TypeLiteral(context.Background(), "private") }, ErrNotSupported, ""},
		{"scroll without device", func(s *eiSender) { s.scrollDev = 0 }, func(b *RemoteDesktopBackend) error { return b.ScrollLeft(context.Background(), 2) }, ErrNotSupported, ""},
		{"scroll without interface", func(s *eiSender) { s.scroll = 0 }, func(b *RemoteDesktopBackend) error { return b.ScrollUp(context.Background(), 2) }, ErrNotSupported, ""},
		{"click without pointer", func(s *eiSender) { s.pointers = nil }, func(b *RemoteDesktopBackend) error { return b.MouseClick(context.Background(), 10, 11, 2) }, ErrNotSupported, ""},
		{"click with empty authorized region", func(s *eiSender) {
			s.pointers = []eiPointerDevice{{device: 501, pointer: 502, regions: []eiRegion{{x: 100, y: 100, width: 3, height: 3, scale: 1}}}}
		}, func(b *RemoteDesktopBackend) error { return b.MouseClick(context.Background(), 10, 11, 2) }, nil, "outside"},
		{"invalid keyup identifier", func(*eiSender) {}, func(b *RemoteDesktopBackend) error { return b.KeyUp(context.Background(), "nonexistent-key") }, nil, "unknown key"},
		{"truncated UTF8 literal", func(*eiSender) {}, func(b *RemoteDesktopBackend) error { return b.TypeLiteral(context.Background(), string([]byte{0xff})) }, nil, "UTF-8"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, peer := newWireObservedRemoteDesktop(t)
			tc.disable(b.sender)
			err := tc.call(b)
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("denied action error = %v; want %v", err, tc.want)
			}
			if tc.contains != "" && (err == nil || !strings.Contains(err.Error(), tc.contains)) {
				t.Fatalf("invalid request accepted or wrong error: %v", err)
			}
			if err == nil {
				t.Fatal("unauthorized action succeeded")
			}
			assertNoEIWireRequest(t, peer)
			if len(b.heldKeys) != 0 || len(b.buttons) != 0 || b.hasPointer {
				t.Fatalf("denied input mutated state: keys=%v buttons=%v pointer=%v", b.heldKeys, b.buttons, b.hasPointer)
			}
			if stateErr := b.stateError(); stateErr != nil {
				t.Fatalf("local validation revoked portal: %v", stateErr)
			}
		})
	}
}

func TestRemoteDesktopNilAndCanceledOperationsHaveNoSideEffects(t *testing.T) {
	var nilBackend *RemoteDesktopBackend
	if err := nilBackend.Close(); err != nil {
		t.Fatalf("nil backend close: %v", err)
	}
	if err := (&RemoteDesktopBackend{}).Close(); err != nil {
		t.Fatalf("partial backend close: %v", err)
	}
	b, peer := newWireObservedRemoteDesktop(t)
	var nilCtx context.Context
	if err := b.ScrollUp(nilCtx, 4); err == nil || !strings.Contains(err.Error(), "nil operation context") {
		t.Fatalf("nil scroll context: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.ScrollRight(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled scroll: %v", err)
	}
	if err := b.Type(ctx, "{enter}"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled type: %v", err)
	}
	assertNoEIWireRequest(t, peer)
}
