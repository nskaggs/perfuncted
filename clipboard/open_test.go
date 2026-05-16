package clipboard

import (
	"context"
	"os"
	"os/exec"
	"reflect"
	"testing"

	"github.com/nskaggs/perfuncted/internal/executil"
)

func TestOpen_NoToolReturnsErr(t *testing.T) {
	t.Setenv("WAYLAND_DISPLAY", "wayland-1")
	os.Unsetenv("DISPLAY")
	// Override LookPath to always fail.
	oldLP := executil.LookPath
	executil.LookPath = func(name string) (string, error) { return "", os.ErrNotExist }
	defer func() { executil.LookPath = oldLP }()

	if _, err := Open(); err == nil {
		t.Fatalf("Open succeeded unexpectedly when no tools present")
	}
}

func TestOpen_PrefersWaylandWhenAvailable(t *testing.T) {
	// Override LookPath to indicate wl-copy/wl-paste are present.
	oldLP := executil.LookPath
	executil.LookPath = func(name string) (string, error) {
		if name == "wl-copy" || name == "wl-paste" {
			return "/nonexistent/" + name, nil
		}
		return "", os.ErrNotExist
	}
	defer func() { executil.LookPath = oldLP }()

	// Override CommandContext so executed commands don't rely on real binaries.
	oldCmd := executil.CommandContext
	executil.CommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		// return a harmless command that exits successfully
		return exec.CommandContext(ctx, "sh", "-c", "exit 0")
	}
	defer func() { executil.CommandContext = oldCmd }()

	// Simulate Wayland session.
	t.Setenv("WAYLAND_DISPLAY", "wayland-2")
	os.Unsetenv("DISPLAY")

	cb, err := Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, ok := cb.(*extCmdClipboard); !ok {
		t.Fatalf("clipboard type = %T, want *extCmdClipboard", cb)
	}
}

func TestOpen_PrefersX11WhenXclipAvailable(t *testing.T) {
	oldLP := executil.LookPath
	executil.LookPath = func(name string) (string, error) {
		if name == "xclip" {
			return "/nonexistent/xclip", nil
		}
		return "", os.ErrNotExist
	}
	defer func() { executil.LookPath = oldLP }()

	oldCmd := executil.CommandContext
	executil.CommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "exit 0")
	}
	defer func() { executil.CommandContext = oldCmd }()

	// Simulate X11 session.
	t.Setenv("DISPLAY", ":0")
	os.Unsetenv("WAYLAND_DISPLAY")
	t.Setenv("XDG_SESSION_TYPE", "x11")

	cb, err := Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, ok := cb.(*extCmdClipboard); !ok {
		t.Fatalf("clipboard type = %T, want *extCmdClipboard", cb)
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
