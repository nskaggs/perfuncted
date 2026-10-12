package perfuncted

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The live diagnostic used a private TMPDIR that made the D-Bus socket
// pathname longer than Linux's sockaddr_un capacity.
func TestLaunchDBusRejectsTooLongSocketPathBeforeSpawn(t *testing.T) {
	dir := filepath.Join(t.TempDir(), strings.Repeat("d", 96))
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "bus")
	if len([]byte(socket)) <= 107 {
		t.Fatalf("fixture not long enough: %d", len(socket))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	infra := &sessionInfra{
		xdgDir:   dir,
		dbusAddr: "unix:path=" + socket,
		ctx:      context.Background(),
		timeouts: TimeoutPolicy{Poll: 5 * time.Millisecond, Startup: 130 * time.Millisecond},
	}
	err := infra.launchDBus(ctx)
	if err == nil || !strings.Contains(err.Error(), "socket pathname exceeds") {
		t.Fatalf("want immediate socket-path rejection, got %v", err)
	}
	if infra.dbusCmd != nil {
		t.Fatal("launched D-Bus instead of rejecting path")
	}
}

func TestSessionSocketPathByteBoundary(t *testing.T) {
	path107 := strings.Repeat("x", 107)
	if err := validateSessionUnixSocketPath(path107); err != nil {
		t.Fatalf("107-byte pathname must be permitted: %v", err)
	}
	if err := validateSessionUnixSocketPath(path107 + "x"); !errors.Is(err, ErrSessionSocketPathTooLong) {
		t.Fatalf("108-byte pathname wrong error: %v", err)
	}
	if err := validateSessionUnixSocketPath(strings.Repeat("é", 54)); !errors.Is(err, ErrSessionSocketPathTooLong) {
		t.Fatalf("multibyte path must use byte length: %v", err)
	}
}

func TestOpenHeadlessRejectsLongRuntimePathBeforeStartingChildren(t *testing.T) {
	temp := filepath.Join(t.TempDir(), strings.Repeat("x", 90))
	if err := os.MkdirAll(temp, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", temp)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	session, err := Open(ctx, WithHeadless(SessionConfig{}))
	if session != nil {
		_ = session.Close()
		t.Fatal("unexpectedly opened a session")
	}
	if !errors.Is(err, ErrSessionSocketPathTooLong) {
		t.Fatalf("Open must reject too-long runtime path before startup: %v", err)
	}
	children, globErr := filepath.Glob(filepath.Join(temp, "perfuncted-xdg-*"))
	if globErr != nil || len(children) != 0 {
		t.Fatalf("orphan runtime directories: count=%d err=%v", len(children), globErr)
	}
}
