//go:build linux

package input

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/gnomebridge"
)

type selectorTrackedInputter struct {
	noopInputter
	closed int
}

func (b *selectorTrackedInputter) Close() error {
	b.closed++
	return nil
}

func resetRuntimeSelectorConstructors(t *testing.T) {
	t.Helper()
	oldGnome, oldVirtual, oldXTest := newGnomeNativeBackend, newWlVirtualBackend, newXTestBackend
	oldUinput, oldStat := newUinputBackend, statUinput
	t.Cleanup(func() {
		newGnomeNativeBackend, newWlVirtualBackend, newXTestBackend = oldGnome, oldVirtual, oldXTest
		newUinputBackend, statUinput = oldUinput, oldStat
	})
	newGnomeNativeBackend = func(ctx context.Context, rt env.Runtime) (Inputter, error) {
		return NewGnomeNativeBackendForRuntimeContext(ctx, rt)
	}
	newWlVirtualBackend = func(ctx context.Context, sock string) (Inputter, error) {
		return NewWlVirtualBackendContext(ctx, sock)
	}
	newXTestBackend = func(display string) (Inputter, error) { return NewXTestBackend(display) }
	newUinputBackend = func(maxX, maxY int32) (Inputter, error) {
		return NewUinputBackend(maxX, maxY)
	}
	statUinput = func() error { _, err := os.Stat("/dev/uinput"); return err }
	t.Setenv("PF_FORCE_INPUT", "")
}

func syntheticGNOMERuntime(t *testing.T) env.Runtime {
	t.Helper()
	return env.FromEnviron([]string{
		"WAYLAND_DISPLAY=wayland-missing",
		"XDG_RUNTIME_DIR=" + t.TempDir(),
		"XDG_CURRENT_DESKTOP=GNOME",
	})
}

func TestOpenRuntimeGNOMESelectionAndFallbackBoundaries(t *testing.T) {
	t.Run("GNOME backend wins", func(t *testing.T) {
		resetRuntimeSelectorConstructors(t)
		want := &selectorTrackedInputter{}
		gnomeCalls, fallbackCalls := 0, 0
		newGnomeNativeBackend = func(context.Context, env.Runtime) (Inputter, error) {
			gnomeCalls++
			return want, nil
		}
		newWlVirtualBackend = func(context.Context, string) (Inputter, error) {
			fallbackCalls++
			return nil, errors.New("unexpected fallback")
		}
		got, err := OpenRuntimeContext(context.Background(), syntheticGNOMERuntime(t), 1024, 768)
		if err != nil || got != want {
			t.Fatalf("GNOME selection = %T %v", got, err)
		}
		if gnomeCalls != 1 || fallbackCalls != 0 {
			t.Fatalf("constructor calls = GNOME:%d fallback:%d", gnomeCalls, fallbackCalls)
		}
	})

	t.Run("nil GNOME result continues to virtual input", func(t *testing.T) {
		resetRuntimeSelectorConstructors(t)
		gnomeCalls, virtualCalls := 0, 0
		want := &selectorTrackedInputter{}
		newGnomeNativeBackend = func(context.Context, env.Runtime) (Inputter, error) {
			gnomeCalls++
			return nil, nil
		}
		newWlVirtualBackend = func(context.Context, string) (Inputter, error) {
			virtualCalls++
			return want, nil
		}
		got, err := OpenRuntimeContext(context.Background(), syntheticGNOMERuntime(t), 1024, 768)
		if err != nil || got != want {
			t.Fatalf("virtual fallback = %T %v", got, err)
		}
		if gnomeCalls != 1 || virtualCalls != 1 {
			t.Fatalf("constructor calls = GNOME:%d virtual:%d", gnomeCalls, virtualCalls)
		}
	})

	t.Run("restart-required bridge does not fall back", func(t *testing.T) {
		resetRuntimeSelectorConstructors(t)
		fallbackCalls := 0
		newGnomeNativeBackend = func(context.Context, env.Runtime) (Inputter, error) {
			return nil, gnomebridge.ErrSessionRestartRequired
		}
		newWlVirtualBackend = func(context.Context, string) (Inputter, error) {
			fallbackCalls++
			return &selectorTrackedInputter{}, nil
		}
		got, err := OpenRuntimeContext(context.Background(), syntheticGNOMERuntime(t), 1024, 768)
		if got != nil || !errors.Is(err, gnomebridge.ErrSessionRestartRequired) {
			t.Fatalf("restart-required selection = %T %v", got, err)
		}
		if fallbackCalls != 0 {
			t.Fatalf("fallback constructors called %d times", fallbackCalls)
		}
	})

	t.Run("bridge and fallback causes remain inspectable", func(t *testing.T) {
		resetRuntimeSelectorConstructors(t)
		bridgeErr := errors.New("synthetic GNOME bridge unavailable")
		virtualErr := errors.New("synthetic virtual protocol unavailable")
		newGnomeNativeBackend = func(context.Context, env.Runtime) (Inputter, error) { return nil, bridgeErr }
		newWlVirtualBackend = func(context.Context, string) (Inputter, error) { return nil, virtualErr }
		statUinput = func() error { return os.ErrPermission }
		_, err := OpenRuntimeContext(context.Background(), syntheticGNOMERuntime(t), 1024, 768)
		if !errors.Is(err, bridgeErr) || !errors.Is(err, virtualErr) {
			t.Fatalf("fallback error lost causes: %v", err)
		}
		if !strings.Contains(err.Error(), "no backend available for Wayland desktop") {
			t.Fatalf("fallback summary missing: %v", err)
		}
	})

	t.Run("canceled bridge closes a partial backend", func(t *testing.T) {
		resetRuntimeSelectorConstructors(t)
		ctx, cancel := context.WithCancel(context.Background())
		want := &selectorTrackedInputter{}
		newGnomeNativeBackend = func(context.Context, env.Runtime) (Inputter, error) {
			cancel()
			return want, errors.New("late bridge failure")
		}
		fallbackCalls := 0
		newWlVirtualBackend = func(context.Context, string) (Inputter, error) {
			fallbackCalls++
			return nil, errors.New("must not run after cancellation")
		}
		got, err := OpenRuntimeContext(ctx, syntheticGNOMERuntime(t), 1024, 768)
		if got != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled GNOME selection = %T %v", got, err)
		}
		if want.closed != 1 || fallbackCalls != 0 {
			t.Fatalf("partial backend closes=%d fallback calls=%d", want.closed, fallbackCalls)
		}
	})
}

func TestOpenRuntimeContextClosesGNOMEBackendReturnedAfterCancellation(t *testing.T) {
	resetRuntimeSelectorConstructors(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend := &selectorTrackedInputter{}
	newGnomeNativeBackend = func(context.Context, env.Runtime) (Inputter, error) {
		cancel()
		return backend, nil
	}
	fallbackCalls := 0
	newWlVirtualBackend = func(context.Context, string) (Inputter, error) {
		fallbackCalls++
		return nil, errors.New("fallback must not run after cancellation")
	}

	got, err := OpenRuntimeContext(ctx, syntheticGNOMERuntime(t), 1024, 768)
	if got != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled GNOME selection = %T %v", got, err)
	}
	if backend.closed != 1 || fallbackCalls != 0 {
		t.Fatalf("late backend closes=%d fallback calls=%d", backend.closed, fallbackCalls)
	}
}

func TestOpenRuntimeForcedUinputBoundariesStayExplicit(t *testing.T) {
	t.Run("inaccessible forced device does not fall back", func(t *testing.T) {
		resetRuntimeSelectorConstructors(t)
		t.Setenv("PF_FORCE_INPUT", "uinput")
		statUinput = func() error { return os.ErrPermission }
		constructorCalls := 0
		newUinputBackend = func(int32, int32) (Inputter, error) {
			constructorCalls++
			return &selectorTrackedInputter{}, nil
		}
		got, err := OpenRuntimeContext(context.Background(), env.FromEnviron([]string{"DISPLAY=:99"}), 1024, 768)
		if got != nil || err == nil || !strings.Contains(err.Error(), "not accessible") {
			t.Fatalf("forced unavailable selection = %T %v", got, err)
		}
		if constructorCalls != 0 {
			t.Fatalf("uinput constructor called %d times", constructorCalls)
		}
	})

	t.Run("nil constructor result is rejected", func(t *testing.T) {
		resetRuntimeSelectorConstructors(t)
		t.Setenv("PF_FORCE_INPUT", "uinput")
		statUinput = func() error { return nil }
		newUinputBackend = func(int32, int32) (Inputter, error) { return nil, nil }
		got, err := OpenRuntimeContext(context.Background(), env.FromEnviron(nil), 1024, 768)
		if got != nil || err == nil || !strings.Contains(err.Error(), "constructor returned nil backend") {
			t.Fatalf("forced nil constructor result = %T %v", got, err)
		}
	})

	t.Run("cancellation closes constructor result", func(t *testing.T) {
		resetRuntimeSelectorConstructors(t)
		t.Setenv("PF_FORCE_INPUT", "uinput")
		statUinput = func() error { return nil }
		ctx, cancel := context.WithCancel(context.Background())
		want := &selectorTrackedInputter{}
		newUinputBackend = func(int32, int32) (Inputter, error) {
			cancel()
			return want, nil
		}
		got, err := OpenRuntimeContext(ctx, env.FromEnviron(nil), 1024, 768)
		if got != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("forced canceled selection = %T %v", got, err)
		}
		if want.closed != 1 {
			t.Fatalf("partial uinput backend closed %d times", want.closed)
		}
	})

	t.Run("late X11 fallback cancellation closes constructor result", func(t *testing.T) {
		resetRuntimeSelectorConstructors(t)
		statUinput = func() error { return nil }
		newXTestBackend = func(string) (Inputter, error) { return nil, errors.New("XTEST unavailable") }
		ctx, cancel := context.WithCancel(context.Background())
		want := &selectorTrackedInputter{}
		newUinputBackend = func(int32, int32) (Inputter, error) {
			cancel()
			return want, nil
		}
		got, err := OpenRuntimeContext(ctx, env.FromEnviron([]string{"DISPLAY=:99"}), 1024, 768)
		if got != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled X11 fallback selection = %T %v", got, err)
		}
		if want.closed != 1 {
			t.Fatalf("partial uinput backend closed %d times", want.closed)
		}
	})
}

func TestOpenRuntimeContextCancellationStopsBackendFallback(t *testing.T) {
	for _, tc := range []struct {
		name         string
		rt           func(*testing.T) env.Runtime
		cancelBefore bool
	}{
		{
			name:         "already canceled",
			rt:           func(*testing.T) env.Runtime { return env.FromEnviron(nil) },
			cancelBefore: true,
		},
		{
			name: "virtual input",
			rt: func(t *testing.T) env.Runtime {
				return env.FromEnviron([]string{"WAYLAND_DISPLAY=wayland-missing", "XDG_RUNTIME_DIR=" + t.TempDir(), "XDG_CURRENT_DESKTOP=SWAY"})
			},
		},
		{
			name: "Wayland uinput",
			rt: func(t *testing.T) env.Runtime {
				return env.FromEnviron([]string{"WAYLAND_DISPLAY=wayland-missing", "XDG_RUNTIME_DIR=" + t.TempDir(), "XDG_CURRENT_DESKTOP=SWAY"})
			},
		},
		{
			name: "Wayland XTEST",
			rt: func(t *testing.T) env.Runtime {
				return env.FromEnviron([]string{"DISPLAY=:99", "WAYLAND_DISPLAY=wayland-missing", "XDG_RUNTIME_DIR=" + t.TempDir(), "XDG_CURRENT_DESKTOP=SWAY"})
			},
		},
		{
			name: "X11 XTEST",
			rt:   func(*testing.T) env.Runtime { return env.FromEnviron([]string{"DISPLAY=:99"}) },
		},
		{
			name: "missing-device probe",
			rt:   func(*testing.T) env.Runtime { return env.FromEnviron(nil) },
		},
		{
			name: "Wayland final cancellation check",
			rt: func(t *testing.T) env.Runtime {
				return env.FromEnviron([]string{"WAYLAND_DISPLAY=wayland-missing", "XDG_RUNTIME_DIR=" + t.TempDir(), "XDG_CURRENT_DESKTOP=SWAY"})
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetRuntimeSelectorConstructors(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancelBefore {
				cancel()
			}
			newWlVirtualBackend = func(context.Context, string) (Inputter, error) {
				if tc.name != "virtual input" {
					return nil, errors.New("synthetic virtual input unavailable")
				}
				cancel()
				return nil, errors.New("virtual input canceled")
			}
			newXTestBackend = func(string) (Inputter, error) {
				if tc.name == "Wayland XTEST" || tc.name == "X11 XTEST" {
					cancel()
				}
				return nil, errors.New("synthetic XTEST unavailable")
			}
			newUinputBackend = func(int32, int32) (Inputter, error) {
				if tc.name == "Wayland uinput" {
					cancel()
					return nil, errors.New("uinput canceled")
				}
				return nil, errors.New("synthetic uinput unavailable")
			}
			statUinput = func() error {
				if tc.name == "Wayland uinput" {
					return nil
				}
				if tc.name == "missing-device probe" || tc.name == "Wayland final cancellation check" {
					cancel()
				}
				return os.ErrPermission
			}
			_, err := OpenRuntimeContext(ctx, tc.rt(t), 1024, 768)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("OpenRuntimeContext error = %v", err)
			}
		})
	}
}

func TestOpenRuntimeRejectsNilFinalUinputBackendAndReportsNoBackend(t *testing.T) {
	t.Run("nil uinput constructor", func(t *testing.T) {
		resetRuntimeSelectorConstructors(t)
		statUinput = func() error { return nil }
		newUinputBackend = func(int32, int32) (Inputter, error) { return nil, nil }
		_, err := OpenRuntimeContext(context.Background(), env.FromEnviron(nil), 1024, 768)
		if err == nil || !strings.Contains(err.Error(), "constructor returned nil backend") {
			t.Fatalf("nil final uinput backend error = %v", err)
		}
	})

	t.Run("no display and no uinput", func(t *testing.T) {
		resetRuntimeSelectorConstructors(t)
		statUinput = func() error { return os.ErrPermission }
		_, err := OpenRuntimeContext(context.Background(), env.FromEnviron(nil), 1024, 768)
		if err == nil || !strings.Contains(err.Error(), "no backend available") {
			t.Fatalf("empty-runtime selection error = %v", err)
		}
	})
}
