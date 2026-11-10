package window

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
)

// The runtime directory may contain characters meaningful to glob syntax.
// Runtime fallback must discover the actual socket instead of skipping it.
func TestSwayRuntimeDiscoveryTreatsRuntimeDirectoryAsLiteral(t *testing.T) {
	root, err := os.MkdirTemp("/var/tmp", "pfsway-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	dir := filepath.Join(root, "private[unfinished")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "sway-ipc.1.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		_, _, err = readSwayMessage(conn)
		if err == nil {
			err = writeSwayMessage(conn, swayMsgGetTree, `{"id":1,"type":"root"}`)
		}
		done <- err
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	manager, err := NewSwayManagerRuntimeContext(ctx, env.FromEnviron([]string{"XDG_RUNTIME_DIR=" + dir}))
	if err != nil {
		t.Fatalf("valid socket inside literal runtime %q not discovered: %v", dir, err)
	}
	defer manager.Close()
	if manager.sock != socket {
		t.Fatalf("discovered socket %q instead of %q", manager.sock, socket)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
