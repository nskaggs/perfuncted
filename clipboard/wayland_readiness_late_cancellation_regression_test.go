package clipboard

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/nskaggs/perfuncted/internal/executil"
)

// A compositor observation can race caller cancellation. A successful paste
// result from a subprocess that disregards its context must not authorize
// Set's success after its caller has already canceled.
func TestWaylandReadinessRejectsCanceledContextAfterMatchingPaste(t *testing.T) {
	original := executil.CommandContext
	t.Cleanup(func() { executil.CommandContext = original })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executil.CommandContext = func(context.Context, string, ...string) *exec.Cmd {
		cancel()
		return exec.Command("sh", "-c", "printf 'synthetic-selection'")
	}
	cb := &extCmdClipboard{getCmd: []string{"wl-paste"}}
	if err := cb.waitWaylandContent(ctx, "synthetic-selection"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled compositor observation reported selection ready: %v", err)
	}
}

// The private foreground owner must be reaped if the read that proved
// clipboard readiness was concurrently canceled. Otherwise a canceled
// clipboard request can retain the selection for the full owner timeout.
func TestWaylandSetLateCancellationReapsForegroundOwner(t *testing.T) {
	original := executil.CommandContext
	t.Cleanup(func() { executil.CommandContext = original })
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var owner *exec.Cmd
	executil.CommandContext = func(_ context.Context, name string, _ ...string) *exec.Cmd {
		if name == "wl-copy" {
			owner = exec.Command("sleep", "30")
			return owner
		}
		cancel()
		return exec.Command("sh", "-c", "printf 'synthetic-selection'")
	}
	cb := &extCmdClipboard{setCmd: []string{"wl-copy", "--foreground"}, getCmd: []string{"wl-paste", "--no-newline"}}
	if err := cb.Set(ctx, "synthetic-selection"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Set reported success: %v", err)
	}
	if owner == nil || owner.ProcessState == nil {
		// A reaped process killed by a signal reports Exited() == false;
		// ProcessState publication by Wait is the relevant lifecycle proof.
		t.Fatalf("canceled Set did not reap foreground owner: %+v", owner)
	}
	for _, pattern := range []string{"perfuncted-clipboard-input-*", "perfuncted-clipboard-stderr-*"} {
		found, err := filepath.Glob(filepath.Join(dir, pattern))
		if err != nil || len(found) != 0 {
			t.Fatalf("clipboard tmpfiles leaked (%s): %v %v", pattern, found, err)
		}
	}
}
