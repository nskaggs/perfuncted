package input

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/output"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestUinputReportsLogicalCoordinatesOnlyAfterSingleOutputDiscovery(t *testing.T) {
	oldOpen, oldCreate := openUinputOutputLister, newUinputBackend
	t.Cleanup(func() { openUinputOutputLister = oldOpen; newUinputBackend = oldCreate })
	cases := []struct {
		name       string
		outputs    []output.Info
		w, h       int32
		advertised bool
	}{
		{name: "authoritative single fractional output", outputs: []output.Info{{Available: true, Geometry: output.Geometry{W: 1600, H: 900}, ResolutionW: 1920, ResolutionH: 1080}}, advertised: true},
		{name: "multiple outputs remain unproven", outputs: []output.Info{{Available: true, Geometry: output.Geometry{W: 1600, H: 900}}, {Available: true, Geometry: output.Geometry{X: 1600, W: 1280, H: 720}}}, advertised: false},
		{name: "explicit dimensions without authoritative output", w: 1600, h: 900, advertised: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			openUinputOutputLister = func(context.Context, env.Runtime) (output.Lister, error) {
				return &fakeUinputOutputLister{outputs: tc.outputs}, nil
			}
			newUinputBackend = func(int32, int32) (Inputter, error) { return &UinputBackend{}, nil }
			backend, err := newResolvedUinputBackend(context.Background(), env.Runtime{}, tc.w, tc.h)
			if err != nil {
				t.Fatal(err)
			}
			b, ok := backend.(*UinputBackend)
			if !ok {
				t.Fatalf("unexpected Uinput backend %T", backend)
			}
			coordinates, err := b.PointerCoordinateSpace(context.Background())
			if tc.advertised {
				if err != nil || coordinates.Kind != CoordinateSpaceLogical || coordinates.ScaleX != 1 || coordinates.ScaleY != 1 {
					t.Fatalf("coordinates=%+v err=%v", coordinates, err)
				}
			} else if !errors.Is(err, ErrNotSupported) {
				t.Fatalf("unproven coordinate mapping was advertised: %+v err=%v", coordinates, err)
			}
			if got := slices.Contains(b.SupportedOperations(), "pointer-coordinate-space"); got != tc.advertised {
				t.Fatalf("operations advertisement=%v want=%v", got, tc.advertised)
			}
		})
	}
}

// A canceled composed key request must stop before consulting the keyboard
// device or attempting to press temporary modifiers.
func TestUinputComposedKeyDispatchRejectsCanceledRequestBeforeDeviceAccess(t *testing.T) {
	var backend UinputBackend
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := backend.typeKeyWithMods(ctx, 1, false, false, modifiers{shift: true, ctrl: true}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled key request reached device dispatch: %v", err)
	}
}

// A writable regular file is not the kernel uinput device. Probe must not
// advertise hardware event authority based merely on successful open(2).
func TestUinputProbeRejectsRegularFileMasqueradingAsDevice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uinput")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	result := checkUinputPath(path)
	if result.Available || !strings.Contains(result.Reason, "device") {
		t.Fatalf("regular file advertised as uinput device: %+v", result)
	}
}

// A symlink to an unrelated character device is not proof that the kernel's
// uinput endpoint exists. Probing must inspect the path itself, not its target.
func TestUinputProbeRejectsSymlinkToUnrelatedCharacterDevice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uinput")
	if err := os.Symlink("/dev/null", path); err != nil {
		t.Fatal(err)
	}
	result := checkUinputPath(path)
	if result.Available || !strings.Contains(result.Reason, "device") {
		t.Fatalf("symlink to unrelated character device advertised as uinput: %+v", result)
	}
}

// Transport-level mouse helpers must honor cancellation before examining an
// unavailable virtual device or emitting any pointer events. This also holds
// for calls made after the public dispatch layer's admission check.
func TestUinputInternalMouseHelpersRefuseCanceledDispatch(t *testing.T) {
	var backend UinputBackend
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"button down", func() error { return backend.mouseDown(ctx, 1) }},
		{"button up", func() error { return backend.mouseUp(ctx, 1) }},
		{"scroll", func() error { return backend.scroll(ctx, false, 1, 1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled helper reached device dispatch: %v", err)
			}
		})
	}
}

// Malformed wheel requests must be rejected before the backend tries to
// allocate its lazy mouse device, even without an attached uinput transport.
func TestUinputPublicScrollDirectionsRejectInvalidCountsBeforeDeviceAccess(t *testing.T) {
	var backend UinputBackend
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		call func(int) error
	}{
		{"up", func(n int) error { return backend.ScrollUp(ctx, n) }},
		{"down", func(n int) error { return backend.ScrollDown(ctx, n) }},
		{"left", func(n int) error { return backend.ScrollLeft(ctx, n) }},
		{"right", func(n int) error { return backend.ScrollRight(ctx, n) }},
	} {
		for _, n := range []int{-1, maxScrollClicks + 1} {
			t.Run(tc.name+"/invalid", func(t *testing.T) {
				err := tc.call(n)
				if err == nil || !strings.Contains(err.Error(), "scroll clicks") {
					t.Fatalf("invalid count %d was dispatched: %v", n, err)
				}
			})
		}
	}
}

// Internal helpers are also required to validate button IDs independently
// of public API preflight. An unavailable touchpad must not mask the error.
func TestUinputInternalMouseButtonsRejectInvalidIDsBeforeDeviceAccess(t *testing.T) {
	var backend UinputBackend
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		call func(int) error
	}{
		{"down", func(n int) error { return backend.mouseDown(ctx, n) }},
		{"up", func(n int) error { return backend.mouseUp(ctx, n) }},
	} {
		for _, id := range []int{-1, 4} {
			t.Run(tc.name+"/invalid", func(t *testing.T) {
				err := tc.call(id)
				if err == nil || !strings.Contains(err.Error(), "button") {
					t.Fatalf("invalid mouse button %d was dispatched: %v", id, err)
				}
			})
		}
	}
}

// A writable character device is not necessarily the uinput protocol.
// Reporting an unrelated device as input-capable breaks backend selection.
func TestUinputProbeRejectsWritableNonUinputCharacterDevice(t *testing.T) {
	if _, err := os.Stat("/dev/null"); err != nil {
		t.Skipf("no synthetic character device: %v", err)
	}
	got := checkUinputPath("/dev/null")
	if got.Available {
		t.Fatalf("unrelated writable character device advertised as uinput: %+v", got)
	}
	if got.Name != "uinput" || !strings.Contains(got.Reason, "uinput") {
		t.Fatalf("missing diagnostic for unrelated device: %+v", got)
	}
}

// Probing an unavailable /dev/uinput candidate must distinguish an absent
// device from a malformed path, and must not advertise permission or protocol
// support when no device has been successfully opened and identified.
func TestUinputProbeMissingDeviceAndInvalidParentFailClosed(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "missing-uinput")
	parent := filepath.Join(root, "ordinary-file")
	if err := os.WriteFile(parent, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, path, reason string }{
		{"missing device", missing, "not found"},
		{"parent is regular file", filepath.Join(parent, "uinput"), "not a directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := checkUinputPath(tc.path)
			if got.Name != "uinput" || got.Available || !strings.Contains(strings.ToLower(got.Reason), tc.reason) {
				t.Fatalf("inaccessible path accepted or misdiagnosed: %+v", got)
			}
		})
	}
	data, err := os.ReadFile(parent)
	if err != nil || string(data) != "preserve" {
		t.Fatalf("probe altered an unrelated parent: %q err=%v", data, err)
	}
}

// The internal wheel dispatch is also a security boundary: callers may
// reach it only with a live operation context and validated click count.
// Neither revoked work nor an invalid wheel request can cause a device write,
// including on a lazily allocated pointer device.
func TestUinputScrollInternalAuthorizationRejectsRevokedOrInvalidRequests(t *testing.T) {
	for _, tc := range []struct {
		name       string
		horizontal bool
		delta      int
		clicks     int
		canceled   bool
	}{
		{name: "revoked vertical", delta: 1, clicks: 1, canceled: true},
		{name: "revoked horizontal", horizontal: true, delta: -1, clicks: -7, canceled: true},
		{name: "invalid negative vertical", delta: 1, clicks: -1},
		{name: "invalid negative horizontal", horizontal: true, delta: -1, clicks: -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mouse := &recordingMouse{}
			b := &UinputBackend{mouse: mouse}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.canceled {
				cancel()
			}
			err := b.scroll(ctx, tc.horizontal, tc.delta, tc.clicks)
			if tc.canceled {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("revoked operation returned %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "scroll") {
				t.Fatalf("invalid scroll was admitted: %v", err)
			}
			if events := mouse.eventsSnapshot(); len(events) != 0 {
				t.Fatalf("unauthorized scroll wrote %v", events)
			}
		})
	}
}
