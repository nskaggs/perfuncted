package input

import (
	"context"
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
