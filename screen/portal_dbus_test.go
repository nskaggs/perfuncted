package screen

import (
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestGnomeShellScreenshotBackendRejectsUninitializedState(t *testing.T) {
	b := &GnomeShellScreenshotBackend{}

	if _, err := b.Grab(context.Background(), image.Rect(0, 0, 1, 1)); err == nil {
		t.Fatal("Grab on an uninitialized backend succeeded")
	}
	if _, _, err := b.Resolution(); err == nil {
		t.Fatal("Resolution on an uninitialized backend succeeded")
	}
}

func TestFileURIPath(t *testing.T) {
	path, err := fileURIPath("file:///tmp/portal%20shot.png")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/tmp/portal shot.png" {
		t.Fatalf("path = %q", path)
	}
}

func TestFileURIPathRejectsUnsupportedHost(t *testing.T) {
	if _, err := fileURIPath("file://remotehost/tmp/portal.png"); err == nil {
		t.Fatal("expected error for unsupported host")
	}
}

func TestPortalUniqueNamePrefersUniqueBusName(t *testing.T) {
	got, err := portalUniqueName([]string{"org.freedesktop.portal.Desktop", ":1.198", "org.example.App"})
	if err != nil {
		t.Fatalf("portalUniqueName: %v", err)
	}
	if got != ":1.198" {
		t.Fatalf("portalUniqueName = %q, want %q", got, ":1.198")
	}
}

func TestPortalRequestPath(t *testing.T) {
	got := portalRequestPath(":1.198", "pf123")
	want := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/1_198/pf123")
	if got != want {
		t.Fatalf("portalRequestPath = %q, want %q", got, want)
	}
}

func TestPortalSignalMatchesReturnedHandle(t *testing.T) {
	expected := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/1_198/pf123")
	returned := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/1_198/pf999")
	sig := &dbus.Signal{Path: returned}
	if !portalSignalMatches(sig, expected, returned) {
		t.Fatal("portalSignalMatches should accept the returned handle path")
	}
	if portalSignalMatches(sig, expected) {
		t.Fatal("portalSignalMatches unexpectedly matched the wrong expected path")
	}
}

func TestRemoveScreenshotIfOwned(t *testing.T) {
	owned := filepath.Join(t.TempDir(), "owned.png")
	if err := os.WriteFile(owned, []byte("owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeScreenshotIfOwned(owned, owned); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(owned); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned screenshot still exists: %v", err)
	}

	foreign := filepath.Join(t.TempDir(), "foreign.png")
	if err := os.WriteFile(foreign, []byte("foreign"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeScreenshotIfOwned(foreign, owned); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("foreign screenshot was removed: %v", err)
	}
}

func TestOpenScreenshotFileRejectsSymlinkOutsideParent(t *testing.T) {
	parent := t.TempDir()
	outside := t.TempDir()
	outsidePath := filepath.Join(outside, "outside.png")
	if err := os.WriteFile(outsidePath, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}

	insidePath := filepath.Join(parent, "inside.png")
	if err := os.WriteFile(insidePath, []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, root, err := openScreenshotFile(insidePath)
	if err != nil {
		t.Fatalf("openScreenshotFile(%q): %v", insidePath, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close screenshot: %v", err)
	}
	if err := root.Close(); err != nil {
		t.Fatalf("close root: %v", err)
	}

	symlinkPath := filepath.Join(parent, "escape.png")
	if err := os.Symlink(outsidePath, symlinkPath); err != nil {
		t.Fatal(err)
	}
	if _, _, err := openScreenshotFile(symlinkPath); err == nil {
		t.Fatalf("openScreenshotFile(%q) unexpectedly followed an external symlink", symlinkPath)
	}
}
