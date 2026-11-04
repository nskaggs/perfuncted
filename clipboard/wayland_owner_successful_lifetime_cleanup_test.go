//go:build linux

package clipboard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/executil"
)

// Successful Wayland readiness detaches the selection owner for a bounded
// lifetime. It must be reaped after the caller's deadline, even if no later
// clipboard request arrives to perform cleanup.
func TestWaylandSuccessfulSelectionOwnerIsReapedAfterBoundedLifetime(t *testing.T) {
	original := executil.CommandContext
	t.Cleanup(func() { executil.CommandContext = original })
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	var owner *exec.Cmd
	executil.CommandContext = func(_ context.Context, name string, _ ...string) *exec.Cmd {
		if name == "wl-copy" {
			owner = exec.Command("sleep", "30")
			return owner
		}
		return exec.Command("sh", "-c", "printf requested-selection")
	}
	cb := &extCmdClipboard{setCmd: []string{"wl-copy", "--foreground"}, getCmd: []string{"wl-paste", "--no-newline"}}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if err := cb.Set(ctx, "requested-selection"); err != nil {
		t.Fatalf("selection readiness: %v", err)
	}
	if owner == nil || owner.Process == nil {
		t.Fatal("foreground selection owner never started")
	}
	// The process is reaped by cmd.Wait rather than merely signaled or left
	// as a zombie; a removed /proc entry proves the child was waited for.
	pid := owner.Process.Pid
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid)))
		if os.IsNotExist(err) {
			break
		}
		if err != nil {
			t.Fatalf("inspect foreground child %d: %v", pid, err)
		}
		if time.Now().After(deadline) {
			_ = owner.Process.Kill()
			t.Fatalf("foreground owner %d survived caller's bounded ownership duration", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
	assertClipboardOwnerTempFilesRemoved(t, dir)
}

// A foreground owner may exit naturally before the ownership timer fires.
// Its Wait goroutine must still reap and clean up temporary resources.
func TestWaylandSuccessfulSelectionOwnerNaturalExitReapsResources(t *testing.T) {
	original := executil.CommandContext
	t.Cleanup(func() { executil.CommandContext = original })
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	var owner *exec.Cmd
	executil.CommandContext = func(_ context.Context, name string, _ ...string) *exec.Cmd {
		if name == "wl-copy" {
			owner = exec.Command("sleep", "0.3")
			return owner
		}
		return exec.Command("sh", "-c", "printf requested-selection")
	}
	cb := &extCmdClipboard{setCmd: []string{"wl-copy", "--foreground"}, getCmd: []string{"wl-paste", "--no-newline"}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := cb.Set(ctx, "requested-selection"); err != nil {
		t.Fatalf("selection readiness: %v", err)
	}
	if owner == nil || owner.Process == nil {
		t.Fatal("foreground selection owner never started")
	}
	pid := owner.Process.Pid
	deadline := time.Now().Add(time.Second)
	for {
		_, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid)))
		if os.IsNotExist(err) {
			break
		}
		if err != nil {
			t.Fatalf("inspect foreground child %d: %v", pid, err)
		}
		if time.Now().After(deadline) {
			_ = owner.Process.Kill()
			t.Fatalf("naturally terminated selection owner %d was not reaped", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
	assertClipboardOwnerTempFilesRemoved(t, dir)
}

// Reaping and temporary-file removal occur sequentially in a goroutine;
// /proc disappearance may precede the final unlink by a few instructions.
func assertClipboardOwnerTempFilesRemoved(t *testing.T, dir string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		var remaining []string
		for _, pattern := range []string{"perfuncted-clipboard-input-*", "perfuncted-clipboard-stderr-*"} {
			files, err := filepath.Glob(filepath.Join(dir, pattern))
			if err != nil {
				t.Fatal(err)
			}
			remaining = append(remaining, files...)
		}
		if len(remaining) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("foreground owner leaked temporary files: %v", remaining)
		}
		time.Sleep(time.Millisecond)
	}
}
