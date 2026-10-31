package main

import (
	"context"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
)

// The find/wait CLI commands must report a fresh concrete window, not a
// successful parse or an unverified cached title. This exercises the same
// WindowBundle that resolves native window IDs for interactive operations.
func TestWindowFindAndWaitCLISyntheticStateAndTimeouts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		matches []window.Info
		want    string
		success bool
	}{
		{"find by title", []string{"window", "find", "title=Editor"}, []window.Info{{NativeID: "w-100", ID: 100, Title: "Editor", AppID: "org.editor", Active: true}}, "Editor", true},
		{"find by ID", []string{"window", "find", "id=100"}, []window.Info{{NativeID: "w-100", ID: 100, Title: "Editor", AppID: "org.editor"}}, "Editor", true},
		{"find missing", []string{"window", "find", "title=Missing"}, []window.Info{{NativeID: "w-100", Title: "Editor"}}, "not found", false},
		{"wait existing", []string{"window", "wait-for", "title=Editor", "--poll", "1ms", "--timeout", "150ms"}, []window.Info{{NativeID: "w-100", Title: "Editor"}}, "Editor", true},
		{"wait missing expires", []string{"window", "wait-for", "title=Missing", "--poll", "1ms", "--timeout", "20ms"}, nil, "deadline exceeded", false},
		{"wait close missing is immediate", []string{"window", "wait-close", "title=Missing", "--poll", "1ms", "--timeout", "150ms"}, nil, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mgr := &pftest.Manager{Lists: [][]window.Info{tc.matches}}
			out, errOut, code := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, nil, mgr, nil), nil }
			})
			if tc.success {
				if code != 0 || !strings.Contains(out, tc.want) {
					t.Fatalf("CLI %v did not confirm window state: code=%d output=%q err=%q", tc.args, code, out, errOut)
				}
			} else if code == 0 || !strings.Contains(strings.ToLower(errOut), strings.ToLower(tc.want)) {
				t.Fatalf("CLI %v fabricated successful state: code=%d output=%q err=%q", tc.args, code, out, errOut)
			}
		})
	}
}
