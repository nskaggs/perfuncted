package input

import (
	"context"
	"errors"
	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
	"testing"
)

func TestX11ObservePointer(t *testing.T) {
	for _, same := range []bool{true, false} {
		mock := &x11.MockConnection{}
		mock.QueryPointerFunc = func(xproto.Window) x11.QueryPointerCookie {
			return syntheticPointerReply{reply: &xproto.QueryPointerReply{SameScreen: same, RootX: -52, RootY: 17}}
		}
		b := &XTestBackend{conn: mock, root: 1}
		got, err := b.ObservePointer(context.Background())
		if !same {
			if !errors.Is(err, ErrNotSupported) || got != (PointerObservation{}) {
				t.Fatalf("different root: %+v %v", got, err)
			}
		} else if err != nil || got.X != -52 || got.Y != 17 || got.Provider != "x11-root" || got.ObservedAt.IsZero() || got.CoordinateSpace.Kind != PointerCoordinateSpaceRootPixels {
			t.Fatalf("got %+v %v", got, err)
		}
	}
}
func TestUnsupportedBackendsHaveNoPointerObserver(t *testing.T) {
	for _, b := range []any{&RemoteDesktopBackend{}, &WlVirtualBackend{}, &UinputBackend{}} {
		if _, ok := b.(PointerObserver); ok {
			t.Fatalf("%T unexpectedly observes pointer", b)
		}
	}
}

type observationGnomeBridge struct{ lifecycleGnomeBridge }

func (*observationGnomeBridge) PointerLocation(context.Context) (int, int, error) { return -14, 0, nil }
func TestGnomeObservePointer(t *testing.T) {
	b := &GnomeNativeBackend{bridge: &observationGnomeBridge{}}
	got, err := b.ObservePointer(context.Background())
	if err != nil || got.X != -14 || got.Y != 0 || got.Provider != "gnome-native" || got.ObservedAt.IsZero() || got.CoordinateSpace != (CoordinateSpaceInfo{Kind: CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1}) {
		t.Fatalf("got %+v %v", got, err)
	}
}
