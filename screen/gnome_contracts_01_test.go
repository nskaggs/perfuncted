package screen

import (
	"context"
	"errors"
	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/compositor"
	"github.com/nskaggs/perfuncted/internal/env"
	"image"
	"strings"
	"testing"
)

func TestGnomeShellCaptureRejectsUnrepresentableRegionsBeforeDBus(t *testing.T) {
	backend, provider := newSyntheticGnomeScreenshotBackend(t)
	tests := []struct {
		name string
		rect image.Rectangle
		want string
	}{
		{"negative x origin overflow", image.Rect(-1<<40, 0, (-1<<40)+1, 1), "origin"},
		{"positive y origin overflow", image.Rect(0, 1<<40, 1, (1<<40)+1), "origin"},
		{"x span overflow", image.Rect(-100, 0, 1<<32, 1), "size"},
		{"y span overflow", image.Rect(0, -100, 1, 1<<32), "size"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider.mu.Lock()
			before := len(provider.observed)
			provider.mu.Unlock()
			got, err := backend.Grab(context.Background(), tt.rect)
			if got != nil || err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("invalid GNOME capture returned image=%v err=%v, want %q", got, err, tt.want)
			}
			provider.mu.Lock()
			after := len(provider.observed)
			provider.mu.Unlock()
			if after != before {
				t.Fatalf("unrepresentable rectangle reached compositor: calls %d -> %d", before, after)
			}
		})
	}
}

func TestGnomeShellCanceledCapturePreservesErrorAndAvoidsDBus(t *testing.T) {
	backend, provider := newSyntheticGnomeScreenshotBackend(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	provider.mu.Lock()
	before := len(provider.observed)
	provider.mu.Unlock()
	for _, rect := range []image.Rectangle{{}, image.Rect(5, 6, 7, 8)} {
		img, err := backend.Grab(ctx, rect)
		if img != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled capture = %v %v", img, err)
		}
		hash, err := backend.GrabRegionHash(ctx, rect)
		if hash != 0 || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled hash = %d %v", hash, err)
		}
	}
	provider.mu.Lock()
	after := len(provider.observed)
	provider.mu.Unlock()
	if after != before {
		t.Fatalf("canceled capture contacted GNOME service: %d -> %d", before, after)
	}
}

func TestGnomeShellCaptureMethodNamesAreBounded(t *testing.T) {
	for _, tt := range []struct{ input, want string }{
		{"org.gnome.Shell.Screenshot.Screenshot", "Screenshot"},
		{"org.gnome.Shell.Screenshot.ScreenshotArea", "ScreenshotArea"},
		{"Screenshot", "Screenshot"},
		{"", ""},
		{"suffix.", ""},
	} {
		if got := methodName(tt.input); got != tt.want {
			t.Errorf("methodName(%q)=%q want %q", tt.input, got, tt.want)
		}
	}
}

// Probe must distinguish an absent screenshot service from a working, authorized
// GNOME screenshot service on a real isolated session bus.
func TestGnomeShellScreenshotProbePrivateBusAvailability(t *testing.T) {
	_, portal := newSyntheticScreenshotPortal(t)
	rt := env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=" + portal.busAddress})
	missing := checkGnomeShellScreenshot(rt, compositor.GNOME)
	if missing.Available || !strings.Contains(missing.Reason, "not on session bus") {
		t.Fatalf("missing GNOME service advertised: %+v", missing)
	}
	fake := &syntheticGnomeScreenshot{success: true}
	reply, err := portal.conn.RequestName(gnomeShellShotDest, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("register screenshot provider: %v, %v", reply, err)
	}
	if err := portal.conn.Export(fake, gnomeShellShotPath, gnomeShellShotIface); err != nil {
		t.Fatal(err)
	}
	available := checkGnomeShellScreenshot(rt, compositor.GNOME)
	if !available.Available || !strings.Contains(available.Reason, "unsafe mode") {
		t.Fatalf("authorized GNOME service not advertised: %+v", available)
	}
	fake.mu.Lock()
	observed := len(fake.observed)
	fake.deny = true
	fake.mu.Unlock()
	if observed == 0 {
		t.Fatal("probe did not perform an authorization capture")
	}
	denied := checkGnomeShellScreenshot(rt, compositor.GNOME)
	if denied.Available || !strings.Contains(denied.Reason, "permission denied") {
		t.Fatalf("denied GNOME service advertised: %+v", denied)
	}
}
