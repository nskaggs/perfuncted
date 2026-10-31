package perfuncted

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// A managed session may only offer sockets owned directly by its private
// runtime. A regular file or a symlink to an unrelated desktop's IPC socket
// is not an authorization to connect to that desktop.
func TestSwaySocketSkipsUntrustedLookalikesAndSymlinks(t *testing.T) {
	runtimeDir, err := os.MkdirTemp("", "pf-sway-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	foreignDir, err := os.MkdirTemp("", "pf-foreign-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(foreignDir) })
	foreign := filepath.Join(foreignDir, "socket")
	foreignListener, err := net.Listen("unix", foreign)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = foreignListener.Close() })
	if err := os.Symlink(foreign, filepath.Join(runtimeDir, "sway-ipc.0.sock")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "sway-ipc.1.sock"), []byte("not a socket"), 0600); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(runtimeDir, "sway-ipc.2.sock")
	listener, err := net.Listen("unix", local)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	session := &Session{infra: &sessionInfra{xdgDir: runtimeDir}}
	if got := session.SwaySocket(); got != local {
		t.Fatalf("chose non-owned or non-socket Sway IPC path: %q instead of %q", got, local)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if got := session.SwaySocket(); got != "" {
		t.Fatalf("untrusted fake Sway IPC socket was published: %q", got)
	}
}
