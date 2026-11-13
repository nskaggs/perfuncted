package perfuncted

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A session's runtime can already have been removed by independent stale
// cleanup. Stop must join successfully instead of interpreting the missing
// path as an unsafe replacement or trying to remove neighboring files.
func TestManagedSessionStopToleratesAlreadyRemovedRuntime(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", "/var/tmp/pf26-store")
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	neighbor := filepath.Join(root, "unrelated")
	if err := os.WriteFile(neighbor, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(runtimeDir); err != nil {
		t.Fatal(err)
	}
	infra := &sessionInfra{xdgDir: runtimeDir}
	if err := infra.stop(); err != nil {
		t.Fatalf("already removed runtime stop: %v", err)
	}
	if err := infra.stop(); err != nil {
		t.Fatalf("idempotent stop: %v", err)
	}
	data, err := os.ReadFile(neighbor)
	if err != nil || string(data) != "preserve" {
		t.Fatalf("neighbor modified during absent-runtime teardown: %q err=%v", data, err)
	}
	if _, err := os.Lstat(runtimeDir); !os.IsNotExist(err) {
		t.Fatalf("runtime resurrected by stop: %v", err)
	}
}

// There is no handler to register for an uninitialized infrastructure. The
// unregister callback must still be safe to invoke repeatedly.
func TestNilManagedInfraSignalCleanupReturnsHarmlessUnregister(t *testing.T) {
	var infra *sessionInfra
	unregister := infra.CleanupOnSignal(context.Background())
	if unregister == nil {
		t.Fatal("nil infrastructure returned no unregister callback")
	}
	unregister()
	unregister()
}
