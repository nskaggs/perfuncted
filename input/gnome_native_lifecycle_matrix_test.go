//go:build linux

package input

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type lifecycleGnomeBridge struct {
	recordingGnomeBridge
	log    []string
	failed error
	closed int
	x, y   int
}

func (b *lifecycleGnomeBridge) Text(_ context.Context, s string) error {
	b.log = append(b.log, "text:"+s)
	return b.failed
}
func (b *lifecycleGnomeBridge) Paste(_ context.Context, s string) error {
	b.log = append(b.log, "paste:"+s)
	return b.failed
}
func (b *lifecycleGnomeBridge) PointerMove(_ context.Context, x, y int32) error {
	b.log = append(b.log, fmt.Sprintf("move:%d,%d", x, y))
	return b.failed
}
func (b *lifecycleGnomeBridge) PointerButton(_ context.Context, button uint32, down bool) error {
	b.log = append(b.log, fmt.Sprintf("button:%d:%t", button, down))
	return b.failed
}
func (b *lifecycleGnomeBridge) PointerLocation(context.Context) (int, int, error) {
	return b.x, b.y, b.failed
}
func (b *lifecycleGnomeBridge) Close() error { b.closed++; return b.failed }

func TestGnomeNativeInputNormalLiteralPointerAndLifecyclePaths(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(*GnomeNativeBackend) error
		want string
	}{
		{"ASCII text", func(b *GnomeNativeBackend) error { return b.TypeLiteral(context.Background(), "ASCII 123") }, "text:ASCII 123"},
		{"Unicode clipboard fallback", func(b *GnomeNativeBackend) error { return b.TypeLiteral(context.Background(), "café") }, "paste:café"},
		{"multiline control-safe text", func(b *GnomeNativeBackend) error { return b.TypeLiteral(context.Background(), "a\nb") }, "text:a\nb"},
		{"key syntax literal braces", func(b *GnomeNativeBackend) error { return b.TypeLiteral(context.Background(), "{ctrl+a}") }, "text:{ctrl+a}"},
		{"move", func(b *GnomeNativeBackend) error { return b.MouseMove(context.Background(), 12, 24) }, "move:12,24"},
		{"mouse down", func(b *GnomeNativeBackend) error { return b.MouseDown(context.Background(), 2) }, "button:2:true"},
		{"mouse up", func(b *GnomeNativeBackend) error { return b.MouseUp(context.Background(), 3) }, "button:3:false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bridge := &lifecycleGnomeBridge{}
			b := &GnomeNativeBackend{bridge: bridge}
			if err := tc.call(b); err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(bridge.log, "|"); got != tc.want {
				t.Fatalf("incorrect GNOME bridge command %q want %q", got, tc.want)
			}
		})
	}
	bridge := &lifecycleGnomeBridge{x: 45, y: 89}
	b := &GnomeNativeBackend{bridge: bridge}
	x, y, err := b.PointerLocation(context.Background())
	if err != nil || x != 45 || y != 89 {
		t.Fatalf("pointer=(%d,%d) %v", x, y, err)
	}
	space, err := b.PointerCoordinateSpace(context.Background())
	if err != nil || space.Kind != CoordinateSpaceLogical || space.ScaleX != 1 || space.ScaleY != 1 {
		t.Fatalf("GNOME coordinates %+v %v", space, err)
	}
	if err := b.Close(); err != nil || bridge.closed != 1 {
		t.Fatalf("bridge close count=%d err=%v", bridge.closed, err)
	}
}
func TestGnomeNativeInputUnavailableAndProviderFailuresFailClosed(t *testing.T) {
	for _, b := range []*GnomeNativeBackend{nil, {}} {
		if err := b.TypeLiteral(context.Background(), "text"); err == nil {
			t.Fatal("uninitialized GNOME accepted input")
		}
		if err := b.MouseUp(context.Background(), 1); err == nil {
			t.Fatal("uninitialized GNOME accepted button")
		}
		if _, _, err := b.PointerLocation(context.Background()); err == nil {
			t.Fatal("uninitialized GNOME disclosed coordinates")
		}
		if _, err := b.PointerCoordinateSpace(context.Background()); err == nil {
			t.Fatal("uninitialized GNOME asserted pointer coordinate trust")
		}
		if err := b.Close(); err != nil {
			t.Fatal(err)
		}
	}
	failure := errors.New("synthetic GNOME bridge disconnected")
	for _, tc := range []struct {
		name string
		call func(*GnomeNativeBackend) error
	}{
		{"text", func(b *GnomeNativeBackend) error { return b.TypeLiteral(context.Background(), "abc") }},
		{"paste", func(b *GnomeNativeBackend) error { return b.TypeLiteral(context.Background(), "🎯") }},
		{"move", func(b *GnomeNativeBackend) error { return b.MouseMove(context.Background(), 4, 5) }},
		{"button down", func(b *GnomeNativeBackend) error { return b.MouseDown(context.Background(), 1) }},
		{"button up", func(b *GnomeNativeBackend) error { return b.MouseUp(context.Background(), 2) }},
		{"pointer", func(b *GnomeNativeBackend) error { _, _, err := b.PointerLocation(context.Background()); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &GnomeNativeBackend{bridge: &lifecycleGnomeBridge{failed: failure}}
			if err := tc.call(b); !errors.Is(err, failure) {
				t.Fatalf("backend error lost: %v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b := &GnomeNativeBackend{bridge: &lifecycleGnomeBridge{}}
	if _, err := b.PointerCoordinateSpace(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled coordinate query: %v", err)
	}
	if err := b.TypeLiteral(ctx, "abc"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled typing: %v", err)
	}
}
