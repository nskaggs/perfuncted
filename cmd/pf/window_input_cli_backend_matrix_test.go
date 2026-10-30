package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
)

// Exercise actual Cobra command paths against a session with a deterministic,
// concrete window backend. Verify the resulting request/output, not only that
// command argument parsing succeeds.
func TestSyntheticWindowCommandsAcrossLifecycle(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		output    string
		activated string
	}{
		{"list plain", []string{"window", "list"}, "Editor", ""},
		{"list json", []string{"window", "list", "--output", "json"}, `"Title":"Editor"`, ""},
		{"active title", []string{"window", "active"}, "Editor", ""},
		{"activate", []string{"window", "activate", "Editor"}, "activation requested", "desktop-window"},
		{"move keep x", []string{"window", "move", "--title", "Editor", "--y", "350"}, "to 100,350", ""},
		{"move keep y", []string{"window", "move", "--title", "Editor", "--x", "205"}, "to 205,200", ""},
		{"resize", []string{"window", "resize", "--title", "Editor", "--w", "320", "--h", "180"}, "320x180", ""},
		{"fullscreen", []string{"window", "fullscreen", "Editor"}, "fullscreen: Editor", ""},
		{"unfullscreen", []string{"window", "unfullscreen", "Editor"}, "unfullscreen: Editor", ""},
		{"get geometry", []string{"window", "get-geometry", "Editor"}, "100,200,740,680", ""},
		{"get geometry json", []string{"window", "get-geometry", "Editor", "--output", "json"}, `"x":100`, ""},
		{"find by title", []string{"window", "find-by-title", "Editor"}, "x=100 y=200", ""},
		{"visible", []string{"window", "is-visible", "Editor"}, "true", ""},
		{"not visible", []string{"window", "is-visible", "Absent"}, "false", ""},
		{"close", []string{"window", "close", "Editor"}, "closed: Editor", ""},
		{"minimize", []string{"window", "minimize", "Editor"}, "minimized: Editor", ""},
		{"maximize", []string{"window", "maximize", "Editor"}, "maximized: Editor", ""},
		{"activate stable ID", []string{"window", "activate-id", "desktop-window"}, "activation requested", "desktop-window"},
		{"close stable ID", []string{"window", "close-id", "desktop-window"}, "close requested", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr := &pftest.Manager{Titles: []string{"Editor"}, Lists: [][]window.Info{{{NativeID: "desktop-window", ID: 77, Title: "Editor", AppID: "editor", X: 100, Y: 200, W: 640, H: 480, Active: true}}}}
			out, errOut, code := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, nil, mgr, nil), nil }
			})
			if code != 0 || !strings.Contains(out, tc.output) {
				t.Fatalf("CLI %v returned %d stdout=%q stderr=%q, want %q", tc.args, code, out, errOut, tc.output)
			}
			if tc.activated != "" && (len(mgr.Activated) != 1 || mgr.Activated[0] != tc.activated) {
				t.Fatalf("wrong activated ID: %v", mgr.Activated)
			}
		})
	}
}

func TestSyntheticWindowCommandsFailClosedOnBackendErrors(t *testing.T) {
	for _, cmd := range [][]string{
		{"window", "list"}, {"window", "active"}, {"window", "activate", "Editor"},
		{"window", "move", "--title", "Editor", "--x", "10", "--y", "20"},
		{"window", "resize", "--title", "Editor"}, {"window", "fullscreen", "Editor"},
		{"window", "unfullscreen", "Editor"}, {"window", "get-geometry", "Editor"},
		{"window", "is-visible", "Editor"}, {"window", "close", "Editor"},
		{"window", "minimize", "Editor"}, {"window", "maximize", "Editor"},
	} {
		t.Run(strings.Join(cmd, "_"), func(t *testing.T) {
			mgr := &pftest.Manager{Err: errors.New("synthetic compositor disconnected")}
			out, errOut, code := captureRunIO(t, cmd, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, nil, mgr, nil), nil }
			})
			if code == 0 || strings.Contains(out, "requested") || strings.Contains(out, "maximized") || !strings.Contains(errOut, "synthetic compositor disconnected") {
				t.Fatalf("unavailable backend was not rejected: code=%d out=%q err=%q", code, out, errOut)
			}
		})
	}
}

func TestSyntheticInputCommandsHaveRealSideEffects(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"move", []string{"input", "move", "--x", "17", "--y", "24"}, "move:17,24"},
		{"click", []string{"input", "click", "--x", "17", "--y", "24", "--repeat", "3"}, "click:17,24"},
		{"double click", []string{"input", "double-click", "--x", "17", "--y", "24"}, "mousedown"},
		{"drag", []string{"input", "drag-and-drop", "--x1", "1", "--y1", "2", "--x2", "50", "--y2", "60"}, "move:50,60"},
		{"center", []string{"input", "click-center", "--rect", "10,20,30,40"}, "click:20,30"},
		{"type", []string{"input", "type", "literal text"}, "type:literal text"},
		{"keydown", []string{"input", "keydown", "a"}, "down:a"},
		{"keyup", []string{"input", "keyup", "a"}, "up:a"},
		{"scroll up", []string{"input", "scroll", "up", "--clicks", "2"}, "scroll-up:2"},
		{"scroll down", []string{"input", "scroll", "down", "--clicks", "2"}, "scroll-down:2"},
		{"scroll left", []string{"input", "scroll", "left", "--clicks", "2"}, "scroll-left:2"},
		{"scroll right", []string{"input", "scroll", "right", "--clicks", "2"}, "scroll-right:2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := &pftest.Inputter{}
			out, errOut, code := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, in, nil, nil), nil }
			})
			if tc.name == "double click" && strings.Join(in.Calls, ",") != "move:17,24,mousedown,mouseup,mousedown,mouseup" {
				t.Fatalf("double click should press and release twice: %v", in.Calls)
			}
			if code != 0 || !strings.Contains(strings.Join(in.Calls, ","), tc.want) {
				t.Fatalf("command %v code=%d out=%q err=%q calls=%v expected %q", tc.args, code, out, errOut, in.Calls, tc.want)
			}
		})
	}
}
