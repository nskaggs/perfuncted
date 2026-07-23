package clipboard

import (
	"context"
	"os/exec"
	"reflect"
	"testing"

	"github.com/nskaggs/perfuncted/internal/executil"
)

func TestOpenCompiles(t *testing.T) {
	t.Skip("requires Wayland/X11 display server")
	_, err := Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
}

func TestExtCmdClipboardGetTrimsOnlyOneTrailingNewline(t *testing.T) {
	oldCmd := executil.CommandContext
	defer func() { executil.CommandContext = oldCmd }()

	var lastCmd *exec.Cmd
	executil.CommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, "printf", "hello\n\n")
		lastCmd = cmd
		return cmd
	}

	cb := &extCmdClipboard{
		getCmd: []string{"fake-get"},
		env:    []string{"WAYLAND_DISPLAY=wayland-test"},
	}

	got, err := cb.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "hello\n" {
		t.Fatalf("Get() = %q, want %q", got, "hello\n")
	}
	if lastCmd == nil {
		t.Fatal("CommandContext was not called")
	}
	if !reflect.DeepEqual(lastCmd.Env, cb.env) {
		t.Fatalf("command env = %v, want %v", lastCmd.Env, cb.env)
	}
}

func TestExtCmdClipboardGetNilContext(t *testing.T) {
	oldCmd := executil.CommandContext
	executil.CommandContext = exec.CommandContext
	defer func() { executil.CommandContext = oldCmd }()

	cb := &extCmdClipboard{
		getCmd: []string{"sh", "-c", "exit 0"},
	}
	//lint:ignore SA1012 regression test for nil-context handling
	if _, err := cb.Get(nil); err != nil {
		t.Fatalf("Get(nil): %v", err)
	}
}

func TestExtCmdClipboardSetNilContext(t *testing.T) {
	oldCmd := executil.CommandContext
	executil.CommandContext = exec.CommandContext
	defer func() { executil.CommandContext = oldCmd }()

	cb := &extCmdClipboard{
		setCmd: []string{"sh", "-c", "exit 0"},
	}
	//lint:ignore SA1012 regression test for nil-context handling
	if err := cb.Set(nil, "hello"); err != nil {
		t.Fatalf("Set(nil): %v", err)
	}
}
