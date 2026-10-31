package window

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/compositor"
	"github.com/nskaggs/perfuncted/internal/env"
)

func TestGNOMERuntimeUnavailableBridgeNeverSelectsUnsafeX11Fallback(t *testing.T) {
	for _, desktop := range []string{"GNOME", "ubuntu:GNOME", "GNOME-Classic"} {
		t.Run(desktop, func(t *testing.T) {
			rt := env.FromEnviron([]string{
				"WAYLAND_DISPLAY=/nonexistent/perfuncted-gnome-synthetic",
				"XDG_CURRENT_DESKTOP=" + desktop,
				"DBUS_SESSION_BUS_ADDRESS=unix:path=/nonexistent/perfuncted-gnome-bus",
				"DISPLAY=:450",
			})
			if kind := compositor.DetectRuntime(rt); kind != compositor.GNOME {
				t.Fatalf("unexpected compositor %v", kind)
			}
			manager, err := OpenRuntimeContext(context.Background(), rt)
			if manager != nil {
				defer manager.Close()
				t.Fatalf("unavailable GNOME backend selected %T", manager)
			}
			if err == nil || !strings.Contains(err.Error(), "GNOME Shell Eval unavailable") {
				t.Fatalf("GNOME fallback accepted unavailable bus or X11 desktop: %v", err)
			}
		})
	}
}

func TestGNOMERuntimeSetupAndProbeCancellationAndUnreachableBus(t *testing.T) {
	rt := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=/nonexistent/perfuncted-gnome-synthetic",
		"XDG_CURRENT_DESKTOP=GNOME",
		"DBUS_SESSION_BUS_ADDRESS=unix:path=/nonexistent/perfuncted-gnome-bus",
	})
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if manager, err := openGNOMERuntimeContext(canceled, rt); manager != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled GNOME opener=%T %v", manager, err)
	}
	for _, probe := range []struct {
		name      string
		available bool
		reason    string
	}{
		{"gnome-shell-eval", false, ""},
		{"kwin-scripting", false, "not a KDE Plasma session"},
	} {
		var gotName, gotReason string
		var available bool
		switch probe.name {
		case "gnome-shell-eval":
			got := checkGnomeShellEval(rt, compositor.GNOME)
			gotName, gotReason, available = got.Name, got.Reason, got.Available
		case "kwin-scripting":
			got := checkKWinScript(rt, compositor.GNOME)
			gotName, gotReason, available = got.Name, got.Reason, got.Available
		}
		if gotName != probe.name || available || gotReason == "" {
			t.Fatalf("%s probe result: %q %t %q", probe.name, gotName, available, gotReason)
		}
	}
	// A KDE-labelled but unreachable D-Bus service must not be considered
	// available just because its desktop label matches.
	if got := checkKWinScript(rt, compositor.KDE); got.Available || !strings.Contains(got.Reason, "D-Bus unavailable") {
		t.Fatalf("KWin probe incorrectly authorized: %+v", got)
	}
}

func TestGNOMENativeAndSwayAdvertisedWindowOperationsAreIndependent(t *testing.T) {
	native := (&GnomeNativeManager{}).SupportedOperations()
	if len(native) == 0 || native[len(native)-1] != "events" {
		t.Fatalf("GNOME operations missing event capability: %v", native)
	}
	native[0] = "MUTATED"
	again := (&GnomeNativeManager{}).SupportedOperations()
	if again[0] == "MUTATED" {
		t.Fatal("returned operations shared mutable state")
	}
	if err := (&SwayManager{}).RestoreByID(context.Background(), "not-a-real-ID"); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("unsupported Sway restore result: %v", err)
	}
}
