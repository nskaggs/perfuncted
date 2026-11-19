//go:build linux

package input

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jezek/xgb"
)

// A reachable X server without the XTEST extension must not grant input
// authority or leave a partially initialized backend with the caller.
func TestXTestBackendRejectsLiveServerWithoutXTestExtension(t *testing.T) {
	if _, err := exec.LookPath("Xvfb"); err != nil {
		t.Skip("Xvfb is unavailable")
	}
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "Xvfb", "-screen", "0", "640x480x24", "-ac", "-extension", "XTEST", "-displayfd", "3")
	cmd.ExtraFiles = []*os.File{write}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		_ = write.Close()
		t.Fatalf("start Xvfb without XTEST: %v", err)
	}
	_ = write.Close()
	t.Cleanup(func() {
		cancel()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	line, err := bufio.NewReader(read).ReadString('\n')
	if err != nil {
		t.Fatalf("Xvfb display identity: %v (%s)", err, stderr.String())
	}
	display := ":" + strings.TrimSpace(line)
	if len(display) < 2 {
		t.Fatalf("invalid Xvfb display %q", display)
	}
	conn, err := xgb.NewConnDisplay(display)
	if err != nil {
		t.Fatalf("X server not reachable at %s: %v (%s)", display, err, stderr.String())
	}
	defer conn.Close()
	backend, err := NewXTestBackend(display)
	if backend != nil {
		_ = backend.Close()
		t.Fatal("backend initialized without the XTEST extension")
	}
	if err == nil || !strings.Contains(err.Error(), "init XTEST") {
		t.Fatalf("missing extension returned error %v, want XTEST initialization rejection", err)
	}
}
