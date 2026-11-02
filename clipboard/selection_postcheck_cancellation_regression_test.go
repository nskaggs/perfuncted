package clipboard

import (
	"context"
	"errors"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/executil"
	"github.com/nskaggs/perfuncted/internal/wl"
)

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
	dir := t.TempDir()
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
