package accessibility

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestChildrenFreshClassifiesProviderDisconnectWithoutPublishingChildren(t *testing.T) {
	backend, root, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(_, frame *syntheticATSPINode) {
		frame.children = []objectRef{{BusName: registryName, ObjectPath: "/org/a11y/atspi/accessible/app"}}
		frame.childrenFailure = "message recipient disconnected"
	})
	t.Cleanup(func() { _ = backend.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	children, err := backend.childrenFresh(ctx, root)
	if len(children) != 0 || !errors.Is(err, ErrDisconnected) || !strings.Contains(err.Error(), "message recipient disconnected") {
		t.Fatalf("disconnected provider returned children=%v err=%v", children, err)
	}
}

func TestChildrenFreshRejectsReplyAfterWindowDestroyed(t *testing.T) {
	var backend *dbusBackend
	var ready atomic.Pointer[dbusBackend]
	var destroyed atomic.Bool
	backend, root, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(_, frame *syntheticATSPINode) {
		frame.children = []objectRef{{BusName: registryName, ObjectPath: "/org/a11y/atspi/accessible/app"}}
		frame.childrenHook = func() {
			if current := ready.Load(); current != nil && destroyed.CompareAndSwap(false, true) {
				sig := &dbus.Signal{
					Name:   "org.a11y.atspi.Event.Window:Destroy",
					Sender: registryName,
					Path:   "/org/a11y/atspi/accessible/editor",
				}
				current.prepareEventTransition(sig, signalEvent(sig))
			}
		}
	})
	t.Cleanup(func() { _ = backend.Close() })
	ready.Store(backend)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	children, err := backend.childrenFresh(ctx, root)
	if len(children) != 0 || !errors.Is(err, ErrStaleNode) {
		t.Fatalf("destroyed window returned children=%v err=%v", children, err)
	}
	if !destroyed.Load() || backend.Generation() != root.Generation {
		t.Fatalf("expected selective window retirement without generation change: destroyed=%v generation=%d", destroyed.Load(), backend.Generation())
	}
	fresh := backend.refID(objectRef{BusName: root.BusName, ObjectPath: dbus.ObjectPath(root.ObjectPath)})
	children, err = backend.childrenFresh(ctx, fresh)
	if err != nil || len(children) != 1 || children[0].ObjectPath != "/org/a11y/atspi/accessible/app" {
		t.Fatalf("replacement window did not recover: children=%v err=%v", children, err)
	}
}
