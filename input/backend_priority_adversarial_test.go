package input

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/internal/env"
	"os"
	"strings"
	"testing"
)

type backendName struct {
	noopInputter
	name string
}

func TestWaylandFallbackPrefersNativeUinputOverXTest(t *testing.T) {
	ow, ox, ou, oldStat := newWlVirtualBackend, newXTestBackend, newUinputBackend, statUinput
	t.Cleanup(func() { newWlVirtualBackend = ow; newXTestBackend = ox; newUinputBackend = ou; statUinput = oldStat })
	calls := []string{}
	newWlVirtualBackend = func(context.Context, string) (Inputter, error) {
		calls = append(calls, "wl-virtual")
		return nil, os.ErrNotExist
	}
	newXTestBackend = func(string) (Inputter, error) { calls = append(calls, "xtest"); return backendName{name: "xtest"}, nil }
	newUinputBackend = func(int32, int32) (Inputter, error) {
		calls = append(calls, "uinput")
		return backendName{name: "uinput"}, nil
	}
	statUinput = func() error { return nil }
	t.Setenv("PF_FORCE_INPUT", "")
	rt := env.FromEnviron([]string{"DISPLAY=:99", "WAYLAND_DISPLAY=wayland-0", "XDG_RUNTIME_DIR=" + t.TempDir()})
	b, err := OpenRuntimeContext(context.Background(), rt, 1600, 900)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	named, ok := b.(backendName)
	if !ok {
		t.Fatalf("unexpected input backend %T", b)
	}
	if got := named.name; got != "uinput" {
		t.Fatalf("selected %s, want Wayland-capable uinput before XWayland-only xtest; calls=%v", got, calls)
	}
}

func TestRuntimeBackendSelectionMatrix(t *testing.T) {
	cases := []struct {
		name            string
		wayland         bool
		virtual, ui, xt bool
		expected        string
	}{
		{"wayland virtual wins", true, true, true, true, "virtual"},
		{"wayland uinput wins", true, false, true, true, "uinput"},
		{"wayland XTest last resort", true, false, false, true, "xtest"},
		{"pure X11 XTest wins", false, false, true, true, "xtest"},
		{"pure X11 uinput fallback", false, false, true, false, "uinput"},
		{"no available backend", false, false, false, false, "error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ow, ox, ou, oldStat := newWlVirtualBackend, newXTestBackend, newUinputBackend, statUinput
			t.Cleanup(func() { newWlVirtualBackend = ow; newXTestBackend = ox; newUinputBackend = ou; statUinput = oldStat })
			newWlVirtualBackend = func(context.Context, string) (Inputter, error) {
				if tc.virtual {
					return backendName{name: "virtual"}, nil
				}
				return nil, os.ErrNotExist
			}
			newXTestBackend = func(string) (Inputter, error) {
				if tc.xt {
					return backendName{name: "xtest"}, nil
				}
				return nil, errors.New("unavailable")
			}
			newUinputBackend = func(int32, int32) (Inputter, error) {
				if tc.ui {
					return backendName{name: "uinput"}, nil
				}
				return nil, errors.New("unavailable")
			}
			statUinput = func() error {
				if tc.ui {
					return nil
				}
				return os.ErrPermission
			}
			t.Setenv("PF_FORCE_INPUT", "")
			vars := []string{"DISPLAY=:99"}
			if tc.wayland {
				vars = append(vars, "WAYLAND_DISPLAY=wayland-0", "XDG_RUNTIME_DIR="+t.TempDir())
			}
			b, err := OpenRuntimeContext(context.Background(), env.FromEnviron(vars), 1600, 900)
			if tc.expected == "error" {
				if err == nil {
					t.Fatalf("expected failure, got %T", b)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected %v", err)
			}
			defer b.Close()
			named, ok := b.(backendName)
			if !ok {
				t.Fatalf("unexpected input backend %T", b)
			}
			if got := named.name; got != tc.expected {
				t.Fatalf("got %q want %q", got, tc.expected)
			}
		})
	}
}
func TestForcedUinputNeverFallsBackAndReportsDimensions(t *testing.T) {
	old, oldStat := newUinputBackend, statUinput
	t.Cleanup(func() { newUinputBackend = old; statUinput = oldStat })
	statUinput = func() error { return nil }
	newUinputBackend = func(int32, int32) (Inputter, error) { return nil, errors.New("device failed") }
	t.Setenv("PF_FORCE_INPUT", "uinput")
	_, err := OpenRuntimeContext(context.Background(), env.FromEnviron([]string{"DISPLAY=:99"}), 1600, 900)
	if err == nil || !strings.Contains(err.Error(), "forced uinput") {
		t.Fatalf("forced backend error=%v", err)
	}
}
