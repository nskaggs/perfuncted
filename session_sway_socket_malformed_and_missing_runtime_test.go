package perfuncted

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// Missing runtime directories must not cause discovery to fall back to the
// caller's desktop, nor should an invalid lookalike be accepted as Sway IPC.
func TestSwaySocketRejectsMissingRuntimeAndMalformedSocketNames(t *testing.T) {
	root := t.TempDir()
	missing := &Session{infra: &sessionInfra{xdgDir: filepath.Join(root, "missing")}}
	if found := missing.SwaySocket(); found != "" {
		t.Fatalf("missing owned runtime resolved unrelated socket: %q", found)
	}
	runtime := filepath.Join(root, "owned")
	if err := os.Mkdir(runtime, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"sway-ipc..sock", "sway-ipc.sock", "sway-ipc.1.socket", "other-ipc.1.sock"} {
		listener, err := net.Listen("unix", filepath.Join(runtime, name))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { listener.Close() })
	}
	owned := &Session{infra: &sessionInfra{xdgDir: runtime}}
	if found := owned.SwaySocket(); found != "" {
		t.Fatalf("malformed socket selected: %q", found)
	}
	valid := filepath.Join(runtime, "sway-ipc.123.sock")
	listener, err := net.Listen("unix", valid)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if found := owned.SwaySocket(); found != valid {
		t.Fatalf("valid owned Sway IPC socket not found: %q want %q", found, valid)
	}
}
