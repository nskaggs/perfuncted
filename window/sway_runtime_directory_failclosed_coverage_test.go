package window

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

// An explicitly configured runtime path is not silently replaced with the
// process runtime directory, and missing folders must be diagnostic errors.
func TestSwayRuntimeDiscoveryRejectsMissingRuntimeDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-sway-runtime")
	manager, err := NewSwayManagerRuntimeContext(context.Background(), env.FromEnviron([]string{"SWAYSOCK=", "XDG_RUNTIME_DIR=" + missing}))
	if manager != nil || err == nil || !strings.Contains(err.Error(), "inspect runtime sockets") || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing explicit runtime directory unexpectedly accepted: manager=%+v error=%v", manager, err)
	}
}

// A fake Sway socket can be a normal file, directory or a socket-looking
// symlink. None is a valid direct Unix socket entry for compositor probing.
func TestSwayRuntimeDiscoverySkipsNonSocketCandidateEntries(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"other.sock", "sway-ipc..sock", "sway-ipc.12.txt", "sway-ipc.42.sock"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("fake"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "sway-ipc.43.sock"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "sway-ipc.42.sock"), filepath.Join(dir, "sway-ipc.44.sock")); err != nil {
		t.Fatal(err)
	}
	manager, err := NewSwayManagerRuntimeContext(context.Background(), env.FromEnviron([]string{"SWAYSOCK=", "XDG_RUNTIME_DIR=" + dir}))
	if manager != nil || err == nil || !strings.Contains(err.Error(), "no reachable sway IPC socket") {
		t.Fatalf("non-socket entries falsely authorized compositor: manager=%+v err=%v", manager, err)
	}
}
