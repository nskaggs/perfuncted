package clipboard

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"github.com/nskaggs/perfuncted/internal/executil"
)

// An external clipboard tool may finish successfully while the caller
// cancels its request. The caller's cancellation must win over the exit
// status: returning success could publish stale clipboard contents.
func TestClipboardGetRejectsCanceledContextAfterExternalSuccess(t *testing.T) {
	original := executil.CommandContext
	t.Cleanup(func() { executil.CommandContext = original })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executil.CommandContext = func(context.Context, string, ...string) *exec.Cmd {
		cancel()
		return exec.Command("sh", "-c", "printf 'private clipboard bytes'")
	}
	cb := &extCmdClipboard{getCmd: []string{"synthetic-get"}}
	got, err := cb.Get(ctx)
	if !errors.Is(err, context.Canceled) || got != "" {
		t.Fatalf("canceled clipboard read published %q err=%v", got, err)
	}
}

func TestClipboardSetRejectsCanceledContextAfterExternalSuccess(t *testing.T) {
	original := executil.CommandContext
	t.Cleanup(func() { executil.CommandContext = original })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executil.CommandContext = func(context.Context, string, ...string) *exec.Cmd {
		cancel()
		return exec.Command("sh", "-c", "cat >/dev/null")
	}
	cb := &extCmdClipboard{setCmd: []string{"synthetic-set"}}
	if err := cb.Set(ctx, "private clipboard bytes"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled clipboard write reported success: %v", err)
	}
}
