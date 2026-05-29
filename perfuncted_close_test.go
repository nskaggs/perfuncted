package perfuncted

import (
	"os"
	"path/filepath"
	"testing"
)

func newImpossibleManagedSession(t *testing.T) *Session {
	t.Helper()
	clearEnv := func(key string) {
		old, ok := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
		t.Cleanup(func() {
			if ok {
				_ = os.Setenv(key, old)
			} else {
				_ = os.Unsetenv(key)
			}
		})
	}
	for _, key := range []string{
		"DISPLAY",
		"WAYLAND_DISPLAY",
		"DBUS_SESSION_BUS_ADDRESS",
		"XDG_CURRENT_DESKTOP",
		"SWAYSOCK",
		"HYPRLAND_INSTANCE_SIGNATURE",
	} {
		clearEnv(key)
	}

	xdgDir := filepath.Join(t.TempDir(), "xdg")
	if err := os.MkdirAll(xdgDir, 0o700); err != nil {
		t.Fatalf("mkdir xdg: %v", err)
	}
	return &Session{
		xdgDir:    xdgDir,
		wlDisplay: "wayland-missing",
		dbusAddr:  "",
	}
}

func TestPerfunctedCloseCleansManagedSession(t *testing.T) {
	xdgDir := filepath.Join(t.TempDir(), "xdg")
	if err := os.MkdirAll(xdgDir, 0o700); err != nil {
		t.Fatalf("mkdir xdg: %v", err)
	}

	sess := &Session{xdgDir: xdgDir}
	pf := &Perfuncted{managed: sess}

	if err := pf.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !sess.IsCleaned() {
		t.Fatal("managed session was not cleaned")
	}
}

func TestNewCleansManagedSessionOnFailure(t *testing.T) {
	sess := newImpossibleManagedSession(t)

	if _, err := New(Options{
		ManagedSession:     sess,
		XDGRuntimeDir:      sess.xdgDir,
		WaylandDisplay:     sess.wlDisplay,
		DBusSessionAddress: sess.dbusAddr,
	}); err == nil {
		t.Fatal("New succeeded unexpectedly")
	}
	if !sess.IsCleaned() {
		t.Fatal("managed session was not cleaned after constructor failure")
	}
}

func TestSessionPerfunctedCleansSessionOnFailure(t *testing.T) {
	sess := newImpossibleManagedSession(t)

	if _, err := sess.Perfuncted(Options{}); err == nil {
		t.Fatal("Session.Perfuncted succeeded unexpectedly")
	}
	if !sess.IsCleaned() {
		t.Fatal("session was not cleaned after Perfuncted failure")
	}
}

func TestPerfunctedCloseNil(t *testing.T) {
	var p *Perfuncted
	if err := p.Close(); err != nil {
		t.Fatalf("nil Close returned error: %v", err)
	}
}
