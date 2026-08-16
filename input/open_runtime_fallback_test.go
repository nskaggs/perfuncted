package input

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

type noopInputter struct{}

func (noopInputter) KeyDown(context.Context, string) error             { return nil }
func (noopInputter) KeyUp(context.Context, string) error               { return nil }
func (noopInputter) Type(context.Context, string) error                { return nil }
func (noopInputter) TypeLiteral(context.Context, string) error         { return nil }
func (noopInputter) MouseMove(context.Context, int, int) error         { return nil }
func (noopInputter) MouseClick(context.Context, int, int, int) error   { return nil }
func (noopInputter) MouseDown(context.Context, int) error              { return nil }
func (noopInputter) MouseUp(context.Context, int) error                { return nil }
func (noopInputter) ScrollUp(context.Context, int) error               { return nil }
func (noopInputter) ScrollDown(context.Context, int) error             { return nil }
func (noopInputter) ScrollLeft(context.Context, int) error             { return nil }
func (noopInputter) ScrollRight(context.Context, int) error            { return nil }
func (noopInputter) PointerLocation(context.Context) (int, int, error) { return 0, 0, nil }
func (noopInputter) Sync(context.Context) error                        { return nil }
func (noopInputter) Close() error                                      { return nil }

func TestOpenRuntimeFallsBackToXTestWhenWaylandSocketUnresolvable(t *testing.T) {
	oldWlVirtual := newWlVirtualBackend
	oldXTest := newXTestBackend
	oldUinput := newUinputBackend
	t.Cleanup(func() {
		newWlVirtualBackend = oldWlVirtual
		newXTestBackend = oldXTest
		newUinputBackend = oldUinput
	})

	newWlVirtualBackend = func(string) (Inputter, error) {
		return nil, os.ErrNotExist
	}
	newXTestBackend = func(string) (Inputter, error) {
		return noopInputter{}, nil
	}
	newUinputBackend = func(int32, int32) (Inputter, error) {
		return nil, os.ErrNotExist
	}

	t.Setenv("PF_FORCE_INPUT", "")
	rt := env.FromEnviron([]string{
		"DISPLAY=:99",
		"WAYLAND_DISPLAY=wayland-0",
		"XDG_RUNTIME_DIR=" + t.TempDir(),
	})

	inp, err := OpenRuntime(rt, 1024, 768)
	if err != nil {
		t.Fatalf("OpenRuntime: %v", err)
	}
	if _, ok := inp.(noopInputter); !ok {
		t.Fatalf("OpenRuntime type = %T, want noopInputter", inp)
	}
}

func TestOpenRuntimeForcedUinputWrapsInitError(t *testing.T) {
	oldUinput := newUinputBackend
	oldStatUinput := statUinput
	t.Cleanup(func() {
		newUinputBackend = oldUinput
		statUinput = oldStatUinput
	})

	want := errors.New("uinput init failed")
	newUinputBackend = func(int32, int32) (Inputter, error) {
		return nil, want
	}
	statUinput = func() error { return nil }

	t.Setenv("PF_FORCE_INPUT", "uinput")
	err := func() error {
		_, err := OpenRuntime(env.FromEnviron(nil), 1024, 768)
		return err
	}()
	if !errors.Is(err, want) {
		t.Fatalf("OpenRuntime error = %v, want wrapped %v", err, want)
	}
}

func TestProbeRuntimeFallsBackToXTestWhenWaylandSocketUnresolvable(t *testing.T) {
	oldWlVirtual := newWlVirtualBackend
	oldXTest := newXTestBackend
	oldUinput := newUinputBackend
	t.Cleanup(func() {
		newWlVirtualBackend = oldWlVirtual
		newXTestBackend = oldXTest
		newUinputBackend = oldUinput
	})

	newWlVirtualBackend = func(string) (Inputter, error) {
		return nil, os.ErrNotExist
	}
	newXTestBackend = func(string) (Inputter, error) {
		return noopInputter{}, nil
	}
	newUinputBackend = func(int32, int32) (Inputter, error) {
		return nil, os.ErrNotExist
	}

	t.Setenv("PF_FORCE_INPUT", "")
	rt := env.FromEnviron([]string{
		"DISPLAY=:99",
		"WAYLAND_DISPLAY=wayland-0",
		"XDG_RUNTIME_DIR=" + t.TempDir(),
	})

	results := ProbeRuntime(rt)
	if len(results) < 2 {
		t.Fatalf("ProbeRuntime len = %d, want at least 2", len(results))
	}
	if results[1].Name != "xtest" {
		t.Fatalf("ProbeRuntime second result = %q, want xtest", results[1].Name)
	}
	if !results[1].Available || !results[1].Selected {
		t.Fatalf("ProbeRuntime xtest available=%v selected=%v, want true/true", results[1].Available, results[1].Selected)
	}
}

func TestOpenUsesCurrentEnvironment(t *testing.T) {
	oldWlVirtual := newWlVirtualBackend
	oldXTest := newXTestBackend
	oldUinput := newUinputBackend
	t.Cleanup(func() {
		newWlVirtualBackend = oldWlVirtual
		newXTestBackend = oldXTest
		newUinputBackend = oldUinput
	})

	var virtualCalls, xTestCalls, uinputCalls int
	newWlVirtualBackend = func(string) (Inputter, error) {
		virtualCalls++
		return noopInputter{}, nil
	}
	newXTestBackend = func(string) (Inputter, error) {
		xTestCalls++
		return nil, os.ErrNotExist
	}
	newUinputBackend = func(int32, int32) (Inputter, error) {
		uinputCalls++
		return nil, os.ErrNotExist
	}

	t.Setenv("PF_FORCE_INPUT", "")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("DISPLAY", ":99")

	inp, err := OpenRuntime(env.FromEnviron(os.Environ()), 1024, 768)
	if err != nil {
		t.Fatalf("OpenRuntime: %v", err)
	}
	if _, ok := inp.(noopInputter); !ok {
		t.Fatalf("Open type = %T, want noopInputter", inp)
	}
	if virtualCalls != 1 || xTestCalls != 0 || uinputCalls != 0 {
		t.Fatalf("constructor calls = virtual:%d xtest:%d uinput:%d", virtualCalls, xTestCalls, uinputCalls)
	}
}

func TestProbeUsesCurrentEnvironment(t *testing.T) {
	oldWlVirtual := newWlVirtualBackend
	oldXTest := newXTestBackend
	oldUinput := newUinputBackend
	t.Cleanup(func() {
		newWlVirtualBackend = oldWlVirtual
		newXTestBackend = oldXTest
		newUinputBackend = oldUinput
	})

	newWlVirtualBackend = func(string) (Inputter, error) {
		return nil, os.ErrNotExist
	}
	newXTestBackend = func(string) (Inputter, error) {
		return noopInputter{}, nil
	}
	newUinputBackend = func(int32, int32) (Inputter, error) {
		return nil, os.ErrNotExist
	}

	t.Setenv("PF_FORCE_INPUT", "")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("DISPLAY", ":99")

	results := Probe()
	if len(results) < 2 {
		t.Fatalf("Probe len = %d, want at least 2", len(results))
	}
	if results[1].Name != "xtest" {
		t.Fatalf("Probe second result = %q, want xtest", results[1].Name)
	}
	if !results[1].Available || !results[1].Selected {
		t.Fatalf("Probe xtest available=%v selected=%v, want true/true", results[1].Available, results[1].Selected)
	}
}

func TestOpenRuntimePrefersXTestOnX11(t *testing.T) {
	oldWlVirtual := newWlVirtualBackend
	oldXTest := newXTestBackend
	oldUinput := newUinputBackend
	t.Cleanup(func() {
		newWlVirtualBackend = oldWlVirtual
		newXTestBackend = oldXTest
		newUinputBackend = oldUinput
	})

	var virtualCalls, xTestCalls, uinputCalls int
	newWlVirtualBackend = func(string) (Inputter, error) {
		virtualCalls++
		return nil, os.ErrNotExist
	}
	newXTestBackend = func(string) (Inputter, error) {
		xTestCalls++
		return noopInputter{}, nil
	}
	newUinputBackend = func(int32, int32) (Inputter, error) {
		uinputCalls++
		return nil, os.ErrNotExist
	}

	t.Setenv("PF_FORCE_INPUT", "")
	rt := env.FromEnviron([]string{"DISPLAY=:99"})
	inp, err := OpenRuntime(rt, 1024, 768)
	if err != nil {
		t.Fatalf("OpenRuntime: %v", err)
	}
	if _, ok := inp.(noopInputter); !ok {
		t.Fatalf("OpenRuntime type = %T, want noopInputter", inp)
	}
	if virtualCalls != 0 || xTestCalls != 1 || uinputCalls != 0 {
		t.Fatalf("constructor calls = virtual:%d xtest:%d uinput:%d", virtualCalls, xTestCalls, uinputCalls)
	}
}

func TestCheckWlVirtualWithGlobs(t *testing.T) {
	tests := []struct {
		name   string
		globs  map[string]bool
		want   bool
		wantRe string
	}{
		{
			name:   "nil",
			globs:  nil,
			want:   false,
			wantRe: "connect wayland-0: failed",
		},
		{
			name:   "missing pointer manager",
			globs:  map[string]bool{"zwp_virtual_keyboard_manager_v1": true, "wl_seat": true},
			want:   false,
			wantRe: "zwlr_virtual_pointer_manager_v1 not advertised",
		},
		{
			name:   "missing keyboard manager",
			globs:  map[string]bool{"zwlr_virtual_pointer_manager_v1": true, "wl_seat": true},
			want:   false,
			wantRe: "zwp_virtual_keyboard_manager_v1 not advertised",
		},
		{
			name:   "available",
			globs:  map[string]bool{"zwlr_virtual_pointer_manager_v1": true, "zwp_virtual_keyboard_manager_v1": true},
			want:   true,
			wantRe: "zwlr_virtual_pointer_manager_v1 + zwp_virtual_keyboard_manager_v1 available",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := checkWlVirtualWithGlobs("wayland-0", tt.globs)
			if r.Available != tt.want {
				t.Fatalf("Available = %v, want %v", r.Available, tt.want)
			}
			if r.Reason != tt.wantRe {
				t.Fatalf("Reason = %q, want %q", r.Reason, tt.wantRe)
			}
		})
	}
}
