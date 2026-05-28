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
	oldWlInputMethod := newWlInputMethodBackend
	oldWlVirtual := newWlVirtualBackend
	oldXTest := newXTestBackend
	oldUinput := newUinputBackend
	t.Cleanup(func() {
		newWlInputMethodBackend = oldWlInputMethod
		newWlVirtualBackend = oldWlVirtual
		newXTestBackend = oldXTest
		newUinputBackend = oldUinput
	})

	newWlInputMethodBackend = func(string, int32, int32) (Inputter, error) {
		return nil, os.ErrNotExist
	}
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

func TestOpenRuntimeSkipsHalfFunctionalWlInputMethod(t *testing.T) {
	oldWlInputMethod := newWlInputMethodBackend
	oldWlVirtual := newWlVirtualBackend
	oldXTest := newXTestBackend
	oldUinput := newUinputBackend
	t.Cleanup(func() {
		newWlInputMethodBackend = oldWlInputMethod
		newWlVirtualBackend = oldWlVirtual
		newXTestBackend = oldXTest
		newUinputBackend = oldUinput
	})

	var inputMethodCalls, virtualCalls, xTestCalls int
	newWlInputMethodBackend = func(string, int32, int32) (Inputter, error) {
		inputMethodCalls++
		return &WlInputMethodBackend{}, nil
	}
	newWlVirtualBackend = func(string) (Inputter, error) {
		virtualCalls++
		return nil, os.ErrNotExist
	}
	newXTestBackend = func(string) (Inputter, error) {
		xTestCalls++
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
	if inputMethodCalls != 1 || virtualCalls != 1 || xTestCalls != 1 {
		t.Fatalf("constructor calls = im:%d virtual:%d xtest:%d, want 1/1/1", inputMethodCalls, virtualCalls, xTestCalls)
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
	oldWlInputMethod := newWlInputMethodBackend
	oldWlVirtual := newWlVirtualBackend
	oldXTest := newXTestBackend
	oldUinput := newUinputBackend
	t.Cleanup(func() {
		newWlInputMethodBackend = oldWlInputMethod
		newWlVirtualBackend = oldWlVirtual
		newXTestBackend = oldXTest
		newUinputBackend = oldUinput
	})

	newWlInputMethodBackend = func(string, int32, int32) (Inputter, error) {
		return nil, os.ErrNotExist
	}
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
	if len(results) < 3 {
		t.Fatalf("ProbeRuntime len = %d, want at least 3", len(results))
	}
	if results[2].Name != "xtest" {
		t.Fatalf("ProbeRuntime third result = %q, want xtest", results[2].Name)
	}
	if !results[2].Available || !results[2].Selected {
		t.Fatalf("ProbeRuntime xtest available=%v selected=%v, want true/true", results[2].Available, results[2].Selected)
	}
}

func TestOpenUsesCurrentEnvironment(t *testing.T) {
	oldWlInputMethod := newWlInputMethodBackend
	oldWlVirtual := newWlVirtualBackend
	oldXTest := newXTestBackend
	oldUinput := newUinputBackend
	t.Cleanup(func() {
		newWlInputMethodBackend = oldWlInputMethod
		newWlVirtualBackend = oldWlVirtual
		newXTestBackend = oldXTest
		newUinputBackend = oldUinput
	})

	var inputMethodCalls, virtualCalls, xTestCalls, uinputCalls int
	newWlInputMethodBackend = func(string, int32, int32) (Inputter, error) {
		inputMethodCalls++
		return noopInputter{}, nil
	}
	newWlVirtualBackend = func(string) (Inputter, error) {
		virtualCalls++
		return nil, os.ErrNotExist
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

	inp, err := Open(1024, 768)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, ok := inp.(noopInputter); !ok {
		t.Fatalf("Open type = %T, want noopInputter", inp)
	}
	if inputMethodCalls != 1 || virtualCalls != 0 || xTestCalls != 0 || uinputCalls != 0 {
		t.Fatalf("constructor calls = im:%d virtual:%d xtest:%d uinput:%d", inputMethodCalls, virtualCalls, xTestCalls, uinputCalls)
	}
}

func TestProbeUsesCurrentEnvironment(t *testing.T) {
	oldWlInputMethod := newWlInputMethodBackend
	oldWlVirtual := newWlVirtualBackend
	oldXTest := newXTestBackend
	oldUinput := newUinputBackend
	t.Cleanup(func() {
		newWlInputMethodBackend = oldWlInputMethod
		newWlVirtualBackend = oldWlVirtual
		newXTestBackend = oldXTest
		newUinputBackend = oldUinput
	})

	newWlInputMethodBackend = func(string, int32, int32) (Inputter, error) {
		return nil, os.ErrNotExist
	}
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
	if len(results) < 3 {
		t.Fatalf("Probe len = %d, want at least 3", len(results))
	}
	if results[2].Name != "xtest" {
		t.Fatalf("Probe third result = %q, want xtest", results[2].Name)
	}
	if !results[2].Available || !results[2].Selected {
		t.Fatalf("Probe xtest available=%v selected=%v, want true/true", results[2].Available, results[2].Selected)
	}
}

func TestOpenRuntimePrefersXTestOnX11(t *testing.T) {
	oldWlInputMethod := newWlInputMethodBackend
	oldWlVirtual := newWlVirtualBackend
	oldXTest := newXTestBackend
	oldUinput := newUinputBackend
	t.Cleanup(func() {
		newWlInputMethodBackend = oldWlInputMethod
		newWlVirtualBackend = oldWlVirtual
		newXTestBackend = oldXTest
		newUinputBackend = oldUinput
	})

	var inputMethodCalls, virtualCalls, xTestCalls, uinputCalls int
	newWlInputMethodBackend = func(string, int32, int32) (Inputter, error) {
		inputMethodCalls++
		return nil, os.ErrNotExist
	}
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
	if inputMethodCalls != 0 || virtualCalls != 0 || xTestCalls != 1 || uinputCalls != 0 {
		t.Fatalf("constructor calls = im:%d virtual:%d xtest:%d uinput:%d", inputMethodCalls, virtualCalls, xTestCalls, uinputCalls)
	}
}

func TestCheckWlInputMethodWithGlobs(t *testing.T) {
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
			name:   "missing manager",
			globs:  map[string]bool{"wl_seat": true},
			want:   false,
			wantRe: "zwp_input_method_manager_v2 not advertised",
		},
		{
			name:   "missing seat",
			globs:  map[string]bool{"zwp_input_method_manager_v2": true},
			want:   false,
			wantRe: "wl_seat not advertised",
		},
		{
			name:   "available",
			globs:  map[string]bool{"zwp_input_method_manager_v2": true, "wl_seat": true},
			want:   true,
			wantRe: "zwp_input_method_manager_v2 + wl_seat available",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := checkWlInputMethodWithGlobs("wayland-0", tt.globs)
			if r.Available != tt.want {
				t.Fatalf("Available = %v, want %v", r.Available, tt.want)
			}
			if r.Reason != tt.wantRe {
				t.Fatalf("Reason = %q, want %q", r.Reason, tt.wantRe)
			}
		})
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
