package clipboard

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/v2/internal/env"
	"github.com/nskaggs/perfuncted/v2/internal/executil"
	"github.com/nskaggs/perfuncted/v2/internal/testutil"
	"github.com/nskaggs/perfuncted/v2/internal/wl"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// Open uses the caller's actual runtime, but a private PATH and fake DISPLAY
// must be enough for a fully synthetic selection; no host desktop is contacted.
func TestClipboardOpenCurrentRuntimeUsesOnlyControlledSyntheticXclip(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "xclip")
	body := "#!/bin/sh\ncase \" $* \" in\n  *' -o '*) printf 'synthetic clipboard\\n' ;;\n  *) /bin/cat >/dev/null ;;\nesac\n"
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("DISPLAY", ":298")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("XDG_SESSION_TYPE", "x11")
	t.Setenv("XDG_CURRENT_DESKTOP", "XFCE")
	t.Setenv("XDG_SESSION_DESKTOP", "XFCE")
	t.Setenv("DESKTOP_SESSION", "XFCE")
	cb, err := Open()
	if err != nil {
		t.Fatalf("Open rejected controlled synthetic X11 tool: %v", err)
	}
	defer cb.Close()
	got, err := cb.Get(context.Background())
	if err != nil || got != "synthetic clipboard\n" {
		t.Fatalf("synthetic clipboard read=%q err=%v", got, err)
	}
	if err := cb.Set(context.Background(), "private test content"); err != nil {
		t.Fatalf("synthetic clipboard write: %v", err)
	}
	selected, ok := cb.(*extCmdClipboard)
	if !ok || len(selected.getCmd) == 0 || selected.getCmd[0] != "xclip" {
		t.Fatalf("wrong clipboard backend selected: %T", cb)
	}
	if !strings.Contains(strings.Join(selected.env, " "), "DISPLAY=:298") {
		t.Fatalf("runtime DISPLAY missing from subprocess environment: %v", selected.env)
	}
}

func TestClipboardOpenCurrentRuntimeWithoutDisplayOrToolsFailsClosed(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("XDG_SESSION_TYPE", "x11")
	t.Setenv("XDG_CURRENT_DESKTOP", "XFCE")
	t.Setenv("XDG_SESSION_DESKTOP", "XFCE")
	t.Setenv("DESKTOP_SESSION", "XFCE")
	got, err := Open()
	if got != nil || !errors.Is(err, ErrNoClipboardTool) {
		t.Fatalf("clipboard selected without display or tools: %T err=%v", got, err)
	}
}

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

func TestExtCmdClipboardSetDoesNotWaitForClipboardDaemon(t *testing.T) {
	oldCmd := executil.CommandContext
	defer func() { executil.CommandContext = oldCmd }()

	// wl-copy and xclip can fork a daemon to retain the selection. If stderr is
	// wired to an os/exec pipe, that daemon inherits the descriptor and Cmd.Run
	// waits for it even after the initial command exits.
	executil.CommandContext = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "sleep 2 >&2 & exit 0")
	}

	// wl-copy uses a foreground owner plus an independently verified paste
	// readiness path, exercised by TestWaylandSetDoesNotWaitForPasteConsumer.
	// The daemonized stderr-handoff path applies to xclip.
	for _, tool := range []string{"xclip"} {
		t.Run(tool, func(t *testing.T) {
			cb := &extCmdClipboard{setCmd: []string{tool}}
			ctx := context.Background()
			started := time.Now()
			if err := cb.Set(ctx, "hello"); err != nil {
				t.Fatalf("Set waited for daemonized %s: %v", tool, err)
			}
			if elapsed := time.Since(started); elapsed >= time.Second {
				t.Fatalf("Set waited %v for daemonized %s", elapsed, tool)
			}
		})
	}
}

// The socket is a private Unix listener, never a desktop session. The tools
// are inert stubs: backend selection must not execute either clipboard tool.
func TestClipboardOpenRuntimeSelectsWaylandOnlyWhenSocketAndBothToolsExist(t *testing.T) {
	dir := testutil.ShortSocketDir(t)
	sock := filepath.Join(dir, "wayland-test")
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	// A real private Wayland wire handshake lets compositor detection finish;
	// the test intentionally never contacts the host desktop.
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
				for {
					var header [8]byte
					if _, err := io.ReadFull(conn, header[:]); err != nil {
						return
					}
					encoded := wl.Uint32(header[4:])
					size := int(encoded >> 16)
					if size < 8 || size > 4096 {
						return
					}
					payload := make([]byte, size-8)
					if _, err := io.ReadFull(conn, payload); err != nil {
						return
					}
					if wl.Uint32(header[0:4]) != 1 {
						return
					}
					switch encoded & 0xffff {
					case 1: // get_registry, no globals advertised
					case 0: // sync callback
						if len(payload) < 4 {
							return
						}
						callback := wl.Uint32(payload)
						var reply [12]byte
						wl.PutUint32(reply[:4], callback)
						wl.PutUint32(reply[4:8], 12<<16)
						if _, err := conn.Write(reply[:]); err != nil {
							return
						}
					default:
						return
					}
				}
			}()
		}
	}()
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	for _, tool := range []string{"wl-copy", "wl-paste", "xclip"} {
		if err := os.WriteFile(filepath.Join(bin, tool), []byte("#!/bin/sh\nexit 77\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	rt := env.FromEnviron([]string{"WAYLAND_DISPLAY=wayland-test", "XDG_RUNTIME_DIR=" + dir, "XDG_SESSION_TYPE=wayland", "XDG_CURRENT_DESKTOP=sway", "DISPLAY=:unused-test", "CUSTOM_SELECTION_MARKER=selection-fixture"})
	clip, err := OpenRuntimeContext(context.Background(), rt)
	if err != nil {
		t.Fatal(err)
	}
	selected, ok := clip.(*extCmdClipboard)
	if !ok {
		t.Fatalf("unexpected Wayland backend %T", clip)
	}
	if !slices.Equal(selected.getCmd, []string{"wl-paste", "--no-newline"}) || !slices.Equal(selected.setCmd, []string{"wl-copy", "--foreground"}) {
		t.Fatalf("wrong Wayland subprocess contract: get=%v set=%v", selected.getCmd, selected.setCmd)
	}
	if !slices.Contains(selected.env, "CUSTOM_SELECTION_MARKER=selection-fixture") {
		t.Fatalf("lost runtime-scoped environment: %v", selected.env)
	}
	if err := clip.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(bin, "wl-paste")); err != nil {
		t.Fatal(err)
	}
	fallback, err := OpenRuntimeContext(context.Background(), rt)
	if err != nil {
		t.Fatal(err)
	}
	selected, ok = fallback.(*extCmdClipboard)
	if !ok || len(selected.getCmd) == 0 || selected.getCmd[0] != "xclip" {
		t.Fatalf("missing Wayland paste tool selected unsafe backend: %T %+v", fallback, selected)
	}
	if err := fallback.Close(); err != nil {
		t.Fatal(err)
	}
	// The other half of the Wayland tool pair is equally mandatory. An
	// available wl-paste without wl-copy must not produce a read-only backend
	// that appears capable of setting clipboard content.
	if err := os.WriteFile(filepath.Join(bin, "wl-paste"), []byte("#!/bin/sh\nexit 77\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(bin, "wl-copy")); err != nil {
		t.Fatal(err)
	}
	fallback, err = OpenRuntimeContext(context.Background(), rt)
	if err != nil {
		t.Fatal(err)
	}
	selected, ok = fallback.(*extCmdClipboard)
	if !ok || len(selected.setCmd) == 0 || selected.setCmd[0] != "xclip" {
		t.Fatalf("missing Wayland copy tool selected unsafe backend: %T %+v", fallback, selected)
	}
	if err := fallback.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(bin, "xclip")); err != nil {
		t.Fatal(err)
	}
	if candidate, err := OpenRuntimeContext(context.Background(), rt); candidate != nil || !errors.Is(err, ErrNoClipboardTool) {
		t.Fatalf("incomplete tools accepted: %T %v", candidate, err)
	}
}

// An active Unix listener is not evidence that a compositor will ever
// answer wl_display.get_registry. A caller's deadline must bound detection
// before the clipboard implementation is chosen or any owner is launched.
func TestClipboardOpenAbortsBlockedWaylandProbeAtCallerDeadline(t *testing.T) {
	dir := testutil.ShortSocketDir(t)
	listener, err := net.Listen("unix", filepath.Join(dir, "wayland-hung"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	rt := env.FromEnviron([]string{"WAYLAND_DISPLAY=wayland-hung", "XDG_RUNTIME_DIR=" + dir, "XDG_SESSION_TYPE=wayland", "XDG_CURRENT_DESKTOP=sway"})
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	clipboard, err := OpenRuntimeContext(ctx, rt)
	if clipboard != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wedged Wayland detection returned backend=%T err=%v, want caller timeout", clipboard, err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("deadline ignored during compositor detection: %v", elapsed)
	}
}

// When the X11 clipboard executable becomes discoverable at the exact moment
// authorization is canceled, backend publication must not beat cancellation.
func TestClipboardOpenRefusesBackendAfterToolLookupCancellation(t *testing.T) {
	original := executil.LookPath
	t.Cleanup(func() { executil.LookPath = original })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lookedUp := 0
	executil.LookPath = func(name string) (string, error) {
		if name != "xclip" {
			t.Fatalf("unexpected tool lookup: %q", name)
		}
		lookedUp++
		cancel()
		return "/synthetic/not-executed/xclip", nil
	}
	rt := env.FromEnviron([]string{"DISPLAY=:synthetic-canceled", "XDG_SESSION_TYPE=x11"})
	backend, err := OpenRuntimeContext(ctx, rt)
	if !errors.Is(err, context.Canceled) || backend != nil {
		t.Fatalf("canceled xclip discovery returned backend=%T, err=%v", backend, err)
	}
	if lookedUp != 1 {
		t.Fatalf("expected one tool lookup, got %d", lookedUp)
	}
}

func TestClipboardOpenReturnsCancellationAfterFailedToolLookup(t *testing.T) {
	original := executil.LookPath
	t.Cleanup(func() { executil.LookPath = original })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executil.LookPath = func(name string) (string, error) {
		if name != "xclip" {
			t.Fatalf("unexpected tool lookup: %q", name)
		}
		cancel()
		return "", errors.New("synthetic clipboard tool missing")
	}
	rt := env.FromEnviron([]string{"DISPLAY=:synthetic-canceled", "XDG_SESSION_TYPE=x11"})
	backend, err := OpenRuntimeContext(ctx, rt)
	if backend != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("failed tool lookup returned backend=%T, err=%v; want cancellation", backend, err)
	}
}

type cancelAfterFirstErrContext struct {
	context.Context //nolint:containedctx // Synthetic context intentionally controls one cancellation boundary.
	cancel          context.CancelFunc
	once            sync.Once
}

func (c *cancelAfterFirstErrContext) Err() error {
	err := c.Context.Err()
	c.once.Do(c.cancel)
	return err
}

// Cancellation after the first preflight check, but before foreground owner
// spawning, must prevent all executable invocation, even when subprocess
// lifetime would otherwise be detached from the caller context.
func TestWaylandSetNeverSpawnsForegroundOwnerAfterEarlyCancellation(t *testing.T) {
	original := executil.CommandContext
	t.Cleanup(func() { executil.CommandContext = original })
	created := 0
	executil.CommandContext = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		created++
		return exec.CommandContext(ctx, "true")
	}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelAfterFirstErrContext{Context: base, cancel: cancel}
	backend := &extCmdClipboard{setCmd: []string{"synthetic-wl-copy"}, getCmd: nil, env: []string{}}
	err := backend.setWayland(ctx, "sensitive clipboard text")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("late cancellation not honored: %v", err)
	}
	if created != 0 {
		t.Fatalf("canceled operation started %d new clipboard owners", created)
	}
}

// A synthetic Wayland registry supports only the get_registry and sync calls
// used during compositor detection. No real desktop or clipboard owner runs.
func TestClipboardWaylandDiscoveryRefusesCancellationAfterFinalToolLookup(t *testing.T) { //nolint:gocyclo // Synthetic Wayland transport exercises two actual capability boundaries.
	dir := testutil.ShortSocketDir(t)
	listener, err := net.Listen("unix", filepath.Join(dir, "wayland-private"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(time.Second))
				for {
					var header [8]byte
					if _, readErr := io.ReadFull(conn, header[:]); readErr != nil {
						return
					}
					encoded := wl.Uint32(header[4:])
					size := int(encoded >> 16)
					if size < 8 || size > 1024 {
						return
					}
					payload := make([]byte, size-8)
					if _, readErr := io.ReadFull(conn, payload); readErr != nil {
						return
					}
					if wl.Uint32(header[:4]) != 1 {
						return
					}
					if encoded&0xffff == 0 {
						if len(payload) < 4 {
							return
						}
						var reply [12]byte
						wl.PutUint32(reply[:4], wl.Uint32(payload[:4]))
						wl.PutUint32(reply[4:8], 12<<16)
						if _, writeErr := conn.Write(reply[:]); writeErr != nil {
							return
						}
					}
				}
			}()
		}
	}()
	original := executil.LookPath
	t.Cleanup(func() { executil.LookPath = original })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	checked := []string{}
	executil.LookPath = func(name string) (string, error) {
		checked = append(checked, name)
		if name == "wl-paste" {
			cancel()
		}
		return "/inert/" + name, nil
	}
	rt := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=wayland-private", "XDG_RUNTIME_DIR=" + dir,
		"XDG_SESSION_TYPE=wayland", "XDG_CURRENT_DESKTOP=sway",
	})
	selected, err := OpenRuntimeContext(ctx, rt)
	if selected != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Wayland discovery exposed backend=%T err=%v", selected, err)
	}
	if !slices.Equal(checked, []string{"wl-copy", "wl-paste"}) {
		t.Fatalf("unexpected Wayland capability checks: %v", checked)
	}
}

func TestSyntheticX11ClipboardTransport(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "xclip")
	selection := filepath.Join(dir, "selection")
	script := "#!/bin/sh\ncase \"$*\" in *-o*) cat \"$PF_SYNTHETIC_CLIP_FILE\";; *) cat > \"$PF_SYNTHETIC_CLIP_FILE\";; esac\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	rt := env.FromEnviron([]string{"DISPLAY=:234", "PF_SYNTHETIC_CLIP_FILE=" + selection, "PATH=/bin:/usr/bin"})
	cb, err := OpenRuntimeContext(context.Background(), rt)
	if err != nil {
		t.Fatalf("open x11 fixture: %v", err)
	}
	defer cb.Close()
	if err := cb.Set(context.Background(), "new\nclipboard"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got, err := cb.Get(context.Background()); err != nil || got != "new\nclipboard" {
		t.Fatalf("get=%q err=%v", got, err)
	}
	if file, err := os.ReadFile(selection); err != nil || string(file) != "new\nclipboard" {
		t.Fatalf("written fixture %q err %v", file, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cb.Get(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled get %v", err)
	}
	if err := cb.Set(ctx, "must not write"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled set %v", err)
	}
	if file, _ := os.ReadFile(selection); string(file) != "new\nclipboard" {
		t.Fatalf("canceled set mutated selection: %q", file)
	}
}
func TestClipboardOpenRuntimeUnreachableAndNoTools(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, vars := range [][]string{{}, {"DISPLAY=:234"}, {"WAYLAND_DISPLAY=missing-wayland", "XDG_RUNTIME_DIR=" + t.TempDir()}} {
		rt := env.FromEnviron(vars)
		if cb, err := OpenRuntime(rt); cb != nil || !errors.Is(err, ErrNoClipboardTool) {
			t.Fatalf("unexpected clipboard for %v: %T %v", vars, cb, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if cb, err := OpenRuntimeContext(ctx, env.FromEnviron(nil)); cb != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled open %T %v", cb, err)
	}
	if !strings.Contains(ErrNoClipboardTool.Error(), "clipboard") {
		t.Fatal("error lacks capability guidance")
	}
}
