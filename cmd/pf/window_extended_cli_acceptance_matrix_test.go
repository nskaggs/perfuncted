package main

import (
	"context"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
)

// Exercise public command dispatch through a deterministic native-ID manager.
// Output must describe the actual selected window, and invalid requests must
// not pass through as successful control operations.
func TestWindowExtendedCLIBehaviorsAndInvalidInputFailClosed(t *testing.T) {
	info := window.Info{NativeID: "id-editor", ID: 27, Title: "Editor - file.txt", AppID: "org.editor", PID: 445, X: 12, Y: 21, W: 640, H: 480, Active: true}
	cases := []struct {
		name     string
		args     []string
		windows  []window.Info
		titles   []string
		expected string
		ok       bool
	}{
		{"plain list", []string{"window", "list"}, []window.Info{info}, nil, "Editor - file.txt", true},
		{"JSON list", []string{"window", "list", "--output=json"}, []window.Info{info}, nil, "\"NativeID\":\"id-editor\"", true},
		{"empty list", []string{"window", "list"}, nil, nil, "", true},
		{"invalid output format", []string{"window", "list", "--output=csv"}, []window.Info{info}, nil, "unknown output format", false},
		{"active title", []string{"window", "active"}, []window.Info{info}, []string{"Editor - file.txt"}, "Editor - file.txt", true},
		{"geometry plain", []string{"window", "get-geometry", "Editor"}, []window.Info{info}, nil, "12,21,652,501", true},
		{"geometry JSON", []string{"window", "get-geometry", "Editor", "--output=json"}, []window.Info{info}, nil, "\"w\":640", true},
		{"geometry wrong format", []string{"window", "get-geometry", "Editor", "--output=xml"}, []window.Info{info}, nil, "invalid --output", false},
		{"find by title", []string{"window", "find-by-title", "Editor"}, []window.Info{info}, nil, "pid=445", true},
		{"visible", []string{"window", "is-visible", "Editor"}, []window.Info{info}, nil, "true", true},
		{"not visible", []string{"window", "is-visible", "Absent"}, []window.Info{info}, nil, "false", true},
		{"unknown stable ID", []string{"window", "activate-id", "unknown"}, []window.Info{info}, nil, "not found", false},
		{"activate by stable ID", []string{"window", "activate-id", "id-editor"}, []window.Info{info}, nil, "activation requested", true},
		{"close by stable ID", []string{"window", "close-id", "id-editor"}, []window.Info{info}, nil, "close requested", true},
		{"close by title", []string{"window", "close", "Editor"}, []window.Info{info}, nil, "closed: Editor", true},
		{"minimize by title", []string{"window", "minimize", "Editor"}, []window.Info{info}, nil, "minimized: Editor", true},
		{"maximize by title", []string{"window", "maximize", "Editor"}, []window.Info{info}, nil, "maximized: Editor", true},
		{"fullscreen by title", []string{"window", "fullscreen", "Editor"}, []window.Info{info}, nil, "fullscreen: Editor", true},
		{"unfullscreen by title", []string{"window", "unfullscreen", "Editor"}, []window.Info{info}, nil, "unfullscreen: Editor", true},
		{"resize invalid dimension", []string{"window", "resize", "--title=Editor", "--w=0", "--h=300"}, []window.Info{info}, nil, "invalid", false},
		{"resize normal", []string{"window", "resize", "--title=Editor", "--w=320", "--h=240"}, []window.Info{info}, nil, "resized", true},
		{"move preserve x", []string{"window", "move", "--title=Editor", "--x=keep", "--y=41"}, []window.Info{info}, nil, "12,41", true},
		{"move preserve y", []string{"window", "move", "--title=Editor", "--x=-11", "--y=keep"}, []window.Info{info}, nil, "-11,21", true},
		{"move bad x", []string{"window", "move", "--title=Editor", "--x=NaN", "--y=2"}, []window.Info{info}, nil, "invalid", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &pftest.Manager{Lists: [][]window.Info{tc.windows}, Titles: tc.titles}
			out, errOut, code := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, nil, m, nil), nil }
			})
			if tc.ok && (code != 0 || !strings.Contains(out, tc.expected)) {
				t.Fatalf("%v code=%d output=%q err=%q expected=%q", tc.args, code, out, errOut, tc.expected)
			}
			if !tc.ok && (code == 0 || !strings.Contains(strings.ToLower(errOut), strings.ToLower(tc.expected))) {
				t.Fatalf("invalid request %v accepted or wrong error: code=%d output=%q err=%q expected=%q", tc.args, code, out, errOut, tc.expected)
			}
		})
	}
}
