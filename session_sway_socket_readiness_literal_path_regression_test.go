package perfuncted

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManagedSwayIPCReadinessUsesLiteralRuntimeAndRealSocket(t *testing.T) {
	root, err := os.MkdirTemp("/var/tmp", "pfready-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	own := filepath.Join(root, "private[unfinished")
	if err := os.Mkdir(own, 0700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(own, "sway-ipc.1.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := waitForSwayIPCSocket(context.Background(), own, 1, time.Millisecond); err != nil {
		t.Fatalf("real socket under literal runtime not ready: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(socket); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.WriteFile(socket, []byte("fake"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := waitForSwayIPCSocket(context.Background(), own, 1, time.Millisecond); err == nil {
		t.Fatal("non-socket file passed Sway IPC readiness")
	}
}
