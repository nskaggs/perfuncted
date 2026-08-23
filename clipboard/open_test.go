package clipboard

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/executil"
)

func TestOpenCompiles(t *testing.T) {
	t.Skip("requires Wayland/X11 display server")
	_, err := Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
}

func TestExtCmdClipboardGetPreservesTrailingNewlines(t *testing.T) {
	oldCmd := executil.CommandContext
	defer func() { executil.CommandContext = oldCmd }()

	var lastCmd *exec.Cmd
	const envValue = "WAYLAND_DISPLAY=wayland-test"
	executil.CommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, "printf", "%s", args[0])
		lastCmd = cmd
		return cmd
	}

	cb := &extCmdClipboard{
		getCmd: []string{"fake-get"},
		env:    []string{envValue},
	}

	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "no trailing newline", text: "hello"},
		{name: "one trailing newline", text: "hello\n"},
		{name: "multiple trailing newlines", text: "hello\n\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cb.getCmd = []string{"fake-get", tc.text}
			got, err := cb.Get(context.Background())
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got != tc.text {
				t.Fatalf("Get() = %q, want %q", got, tc.text)
			}
		})
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
	if _, err := cb.Get(context.TODO()); err != nil {
		t.Fatalf("Get(nil): %v", err)
	}
}

func TestExtCmdClipboardGetPreservesContextError(t *testing.T) {
	oldCmd := executil.CommandContext
	executil.CommandContext = exec.CommandContext
	defer func() { executil.CommandContext = oldCmd }()

	cb := &extCmdClipboard{getCmd: []string{"false"}}
	tests := []struct {
		name       string
		newContext func() (context.Context, context.CancelFunc)
		want       error
	}{
		{
			name: "canceled",
			newContext: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, cancel
			},
			want: context.Canceled,
		},
		{
			name: "deadline exceeded",
			newContext: func() (context.Context, context.CancelFunc) {
				return context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			},
			want: context.DeadlineExceeded,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := tt.newContext()
			defer cancel()
			if _, err := cb.Get(ctx); !errors.Is(err, tt.want) {
				t.Fatalf("Get error = %v, want errors.Is(..., %v)", err, tt.want)
			}
		})
	}
}

func TestExtCmdClipboardSetNilContext(t *testing.T) {
	oldCmd := executil.CommandContext
	executil.CommandContext = exec.CommandContext
	defer func() { executil.CommandContext = oldCmd }()

	cb := &extCmdClipboard{
		setCmd: []string{"sh", "-c", "exit 0"},
	}
	if err := cb.Set(context.TODO(), "hello"); err != nil {
		t.Fatalf("Set(nil): %v", err)
	}
}
