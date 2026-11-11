package main

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/pftest"
)

type relativePointerInput struct {
	*pftest.Inputter
	x, y        int
	locationErr error
	space       input.CoordinateSpaceInfo
}

func (i *relativePointerInput) PointerCoordinateSpace(context.Context) (input.CoordinateSpaceInfo, error) {
	return i.space, nil
}
func (i *relativePointerInput) PointerLocation(context.Context) (int, int, error) {
	return i.x, i.y, i.locationErr
}
func (i *relativePointerInput) MouseMove(ctx context.Context, x, y int) error {
	if err := i.Inputter.MouseMove(ctx, x, y); err != nil {
		return err
	}
	i.x, i.y = x, y
	return nil
}

type relativePointerWindow struct {
	*pftest.Manager
	in               *relativePointerInput
	cursorErr        error
	freeze           bool
	frozenX, frozenY int
}

func (w *relativePointerWindow) SupportedOperations() []string { return []string{"cursor-position"} }
func (w *relativePointerWindow) CursorPosition(context.Context) (int, int, error) {
	if w.freeze {
		return w.frozenX, w.frozenY, w.cursorErr
	}
	return w.in.x, w.in.y, w.cursorErr
}
func runRelativePointerCLI(t *testing.T, i *relativePointerInput, w *relativePointerWindow, args ...string) (string, string, int) {
	t.Helper()
	return captureRunIO(t, args, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) {
			return perfuncted.NewSessionForTesting(nil, i, w, nil, nil), nil
		}
	})
}
func newRelativePointerBackend(x, y int) (*relativePointerInput, *relativePointerWindow) {
	i := &relativePointerInput{Inputter: &pftest.Inputter{}, x: x, y: y, space: input.CoordinateSpaceInfo{Kind: input.CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1}}
	return i, &relativePointerWindow{Manager: &pftest.Manager{}, in: i}
}
func TestRelativePointerCLISuccessAndFallbackToCompositor(t *testing.T) {
	for _, tc := range []struct {
		name     string
		fallback bool
		verify   bool
	}{
		{"input coordinates", false, true},
		{"compositor fallback", true, true},
		{"no verification", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i, w := newRelativePointerBackend(100, 200)
			if tc.fallback {
				i.locationErr = input.ErrNotSupported
			}
			args := []string{"input", "move-relative", "--dx=13", "--dy=-7"}
			if !tc.verify {
				args = append(args, "--verify=false")
			}
			out, stderr, code := runRelativePointerCLI(t, i, w, args...)
			if code != 0 || !strings.Contains(out, "113,193") || len(i.Calls) != 1 || i.Calls[0] != "move:113,193" {
				t.Fatalf("relative movement: code=%d out=%q stderr=%q calls=%v", code, out, stderr, i.Calls)
			}
		})
	}
}
func TestRelativePointerCLIRejectsUnsafeCoordinatesBeforeInput(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		x, y   int
		modify func(*relativePointerInput, *relativePointerWindow)
		want   string
	}{
		{"positive-overflow", []string{"input", "move-relative", "--dx=2", "--verify=false"}, math.MaxInt - 1, 0, nil, "overflows"},
		{"negative-overflow", []string{"input", "move-relative", "--dx=-2", "--verify=false"}, math.MinInt + 1, 0, nil, "overflows"},
		{"y-overflow", []string{"input", "move-relative", "--dy=2", "--verify=false"}, 0, math.MaxInt - 1, nil, "overflows"},
		{"untrusted-scale", []string{"input", "move-relative", "--dx=2"}, 0, 0, func(i *relativePointerInput, _ *relativePointerWindow) { i.space.ScaleX = 1.25 }, "logical pointer coordinate scale"},
		{"location-failure", []string{"input", "move-relative", "--dx=2"}, 0, 0, func(i *relativePointerInput, _ *relativePointerWindow) {
			i.locationErr = errors.New("synthetic cursor query failed")
		}, "synthetic cursor query failed"},
		{"compositor-failure", []string{"input", "move-relative", "--dx=2"}, 0, 0, func(i *relativePointerInput, w *relativePointerWindow) {
			i.locationErr = input.ErrNotSupported
			w.cursorErr = errors.New("synthetic compositor error")
		}, "synthetic compositor error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			i, w := newRelativePointerBackend(tc.x, tc.y)
			if tc.modify != nil {
				tc.modify(i, w)
			}
			out, stderr, code := runRelativePointerCLI(t, i, w, tc.args...)
			if code == 0 || !strings.Contains(stderr, tc.want) || len(i.Calls) != 0 {
				t.Fatalf("unsafe operation: code=%d out=%q stderr=%q calls=%v", code, out, stderr, i.Calls)
			}
		})
	}
}

func TestRelativePointerCLIRequiresFreshCompositorVerification(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*relativePointerWindow)
		want      string
	}{
		{"stale-position", func(w *relativePointerWindow) { w.freeze = true; w.frozenX = 100; w.frozenY = 200 }, "relative pointer move unverified"},
		{"compositor-read-error", func(w *relativePointerWindow) { w.cursorErr = errors.New("synthetic post-move cursor failure") }, "relative pointer position verification"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i, w := newRelativePointerBackend(100, 200)
			tc.configure(w)
			out, stderr, code := runRelativePointerCLI(t, i, w, "input", "move-relative", "--dx=7", "--dy=9")
			if code == 0 || !strings.Contains(stderr, tc.want) || len(i.Calls) != 1 || i.Calls[0] != "move:107,209" {
				t.Fatalf("unverified cursor move: code=%d out=%q stderr=%q calls=%v", code, out, stderr, i.Calls)
			}
		})
	}
}
