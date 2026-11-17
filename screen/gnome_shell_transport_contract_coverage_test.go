package screen

import (
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

type malformedGnomeScreenshotReply struct{}

func (malformedGnomeScreenshotReply) Screenshot(bool, bool, string) (string, *dbus.Error) {
	return "unexpected reply shape", nil
}

func (malformedGnomeScreenshotReply) ScreenshotArea(int32, int32, int32, int32, bool, string) (string, *dbus.Error) {
	return "unexpected reply shape", nil
}

func TestGnomeShellRejectsMissingBusAndMalformedReply(t *testing.T) {
	if backend, err := NewGnomeShellScreenshotBackendForBus(""); backend != nil || err == nil || !strings.Contains(err.Error(), "D-Bus session unset") {
		t.Fatalf("empty session bus address returned backend=%v err=%v", backend, err)
	}

	_, portal := newSyntheticScreenshotPortal(t)
	reply, err := portal.conn.RequestName(gnomeShellShotDest, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claim GNOME screenshot service: reply=%v err=%v", reply, err)
	}
	if err := portal.conn.Export(malformedGnomeScreenshotReply{}, gnomeShellShotPath, gnomeShellShotIface); err != nil {
		t.Fatal(err)
	}
	backend, err := NewGnomeShellScreenshotBackendForBusContext(context.Background(), portal.busAddress)
	if backend != nil || err == nil || !strings.Contains(err.Error(), "reply") {
		t.Fatalf("malformed screenshot reply returned backend=%v err=%v", backend, err)
	}
}

func TestGnomeShellUsesOwnedPathWhenServiceReturnsNoPath(t *testing.T) {
	backend, provider := newSyntheticGnomeScreenshotBackend(t)
	provider.mu.Lock()
	provider.returnEmptyPath = true
	provider.mu.Unlock()

	img, err := backend.Grab(context.Background(), image.Rectangle{})
	if err != nil || img == nil || img.Bounds() != image.Rect(0, 0, 4, 4) {
		t.Fatalf("empty service path did not use the owned screenshot transport: image=%v err=%v", img, err)
	}
}

func TestGnomeShellReportsCleanupFailureWithoutDeletingReplacementContents(t *testing.T) {
	backend, provider := newSyntheticGnomeScreenshotBackend(t)
	t.Cleanup(func() {
		provider.mu.Lock()
		path := provider.transportPath
		provider.mu.Unlock()
		if path != "" {
			_ = os.RemoveAll(path)
		}
	})
	provider.mu.Lock()
	provider.replaceWithNonemptyDir = true
	provider.mu.Unlock()

	img, err := backend.Grab(context.Background(), image.Rectangle{})
	if img != nil || err == nil || !strings.Contains(err.Error(), "remove transport") {
		t.Fatalf("replacement transport returned image=%v err=%v; want reported cleanup failure", img, err)
	}

	provider.mu.Lock()
	path := provider.transportPath
	provider.mu.Unlock()
	data, readErr := os.ReadFile(filepath.Join(path, "preserve"))
	if readErr != nil || string(data) != "untrusted replacement" {
		t.Fatalf("cleanup removed or altered replacement contents: %q %v", data, readErr)
	}
}

func TestGnomeScreenshotDecodeRejectsMissingParent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-parent", "capture.png")
	if img, err := decodeScreenshot(path); img != nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("decode with missing parent returned image=%v err=%v", img, err)
	}
}
