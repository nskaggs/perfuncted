//go:build linux
// +build linux

package input

import (
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/probe"
)

// waylandRuntime returns an env.Runtime with WAYLAND_DISPLAY set.
func waylandRuntime(sock string) env.Runtime {
	return env.FromEnviron([]string{
		"WAYLAND_DISPLAY=" + sock,
		"XDG_SESSION_TYPE=wayland",
	})
}

// x11Runtime returns an env.Runtime with DISPLAY set.
func x11Runtime(display string) env.Runtime {
	return env.FromEnviron([]string{
		"DISPLAY=" + display,
	})
}

// emptyRuntime returns an env.Runtime with no display variables.
func emptyRuntime() env.Runtime {
	return env.FromEnviron([]string{})
}

func TestOpenRuntime_EmptyEnv(t *testing.T) {
	// No Wayland socket, no DISPLAY → may still succeed via uinput fallback.
	// Just verify it doesn't panic.
	rt := emptyRuntime()
	_, err := OpenRuntime(rt, 1024, 768)
	if err != nil {
		t.Logf("OpenRuntime returned error (expected in envs without /dev/uinput): %v", err)
	} else {
		t.Log("OpenRuntime succeeded via uinput fallback")
	}
}

func TestOpenRuntime_UnreachableWayland(t *testing.T) {
	// Unreachable Wayland socket → falls through to uinput if available.
	rt := waylandRuntime("/nonexistent.sock")
	_, err := OpenRuntime(rt, 1024, 768)
	if err != nil {
		t.Logf("OpenRuntime returned error (expected without /dev/uinput): %v", err)
	} else {
		t.Log("OpenRuntime succeeded via uinput fallback")
	}
}

func TestOpenRuntime_ForceUinput(t *testing.T) {
	t.Setenv("PF_FORCE_INPUT", "uinput")
	defer t.Setenv("PF_FORCE_INPUT", "")

	rt := waylandRuntime("wayland-0")
	_, err := OpenRuntime(rt, 1024, 768)
	// May or may not succeed depending on /dev/uinput access.
	// Just verify no panic.
	if err != nil {
		t.Logf("OpenRuntime returned expected error: %v", err)
	}
}

func TestProbeRuntime_Wayland(t *testing.T) {
	rt := waylandRuntime("/nonexistent.sock")
	results := ProbeRuntime(rt)

	// Should have 3 results: wl-input-method, wl-virtual, uinput
	if len(results) != 3 {
		t.Fatalf("expected 3 probe results, got %d", len(results))
	}
	if results[0].Name != "wl-input-method" {
		t.Errorf("first = %q, want wl-input-method", results[0].Name)
	}
	if results[1].Name != "wl-virtual" {
		t.Errorf("second = %q, want wl-virtual", results[1].Name)
	}
	if results[2].Name != "uinput" {
		t.Errorf("third = %q, want uinput", results[2].Name)
	}
	// All should be unavailable (socket unreachable, /dev/uinput likely absent)
	for _, r := range results {
		if r.Name == "" {
			t.Error("probe result missing name")
		}
		if r.Reason == "" {
			t.Errorf("result %q missing reason", r.Name)
		}
	}
}

func TestProbeRuntime_X11(t *testing.T) {
	rt := x11Runtime(":99")
	results := ProbeRuntime(rt)

	// With no Wayland, should have xtest + uinput results
	if len(results) < 2 {
		t.Fatalf("expected at least 2 probe results, got %d", len(results))
	}
	if results[0].Name != "xtest" {
		t.Errorf("first = %q, want xtest", results[0].Name)
	}
}

func TestProbeRuntime_Empty(t *testing.T) {
	rt := emptyRuntime()
	results := ProbeRuntime(rt)

	// Should still return populated results
	if len(results) < 1 {
		t.Fatal("expected at least 1 probe result")
	}
	// Verify all results have names and reasons
	for _, r := range results {
		if r.Name == "" {
			t.Error("probe result missing name")
		}
		if r.Reason == "" {
			t.Errorf("result %q missing reason", r.Name)
		}
	}
}

func TestProbeSelectBest_FirstWins(t *testing.T) {
	results := probe.SelectBest([]probe.Result{
		{Name: "first", Available: false, Reason: "no"},
		{Name: "second", Available: true, Reason: "yes"},
		{Name: "third", Available: true, Reason: "yes"},
	})
	if results[0].Selected {
		t.Error("first should not be selected")
	}
	if !results[1].Selected {
		t.Error("second should be selected")
	}
	if results[2].Selected {
		t.Error("third should not be selected (second already selected)")
	}
}

func TestProbeSelectBest_NoneAvailable(t *testing.T) {
	results := probe.SelectBest([]probe.Result{
		{Name: "a", Available: false},
		{Name: "b", Available: false},
	})
	for _, r := range results {
		if r.Selected {
			t.Errorf("%s should not be selected", r.Name)
		}
	}
}

func TestCheckWlInputMethod_NoSocket(t *testing.T) {
	rt := emptyRuntime()
	r := checkWlInputMethod(rt)
	if r.Available {
		t.Error("wl-input-method should not be available without WAYLAND_DISPLAY")
	}
	if r.Name != "wl-input-method" {
		t.Errorf("name = %q, want wl-input-method", r.Name)
	}
	if r.Reason != "WAYLAND_DISPLAY not set" {
		t.Errorf("reason = %q, want %q", r.Reason, "WAYLAND_DISPLAY not set")
	}
}

func TestCheckXTest_NoDisplay(t *testing.T) {
	rt := emptyRuntime()
	r := checkXTest(rt)
	if r.Available {
		t.Error("xtest should not be available without DISPLAY")
	}
	if r.Name != "xtest" {
		t.Errorf("name = %q, want xtest", r.Name)
	}
	if r.Reason != "DISPLAY not set" {
		t.Errorf("reason = %q, want %q", r.Reason, "DISPLAY not set")
	}
}

func TestCheckWlVirtual_NoSocket(t *testing.T) {
	rt := emptyRuntime()
	r := checkWlVirtual(rt)
	if r.Available {
		t.Error("wl-virtual should not be available without WAYLAND_DISPLAY")
	}
	if r.Name != "wl-virtual" {
		t.Errorf("name = %q, want wl-virtual", r.Name)
	}
}
