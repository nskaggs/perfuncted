package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted"
)

// Run actual Cobra leaf commands against a deliberately denied synthetic
// desktop. No operation is allowed to report success or mask an authorization
// failure with a secondary backend error.
func TestWindowAndScreenCommandsPropagateDesktopAuthorizationDenial(t *testing.T) {
	denied := errors.New("synthetic desktop authorization denied")
	cases := []struct {
		name string
		args []string
	}{
		{"window list", []string{"window", "list"}},
		{"window list JSON", []string{"window", "list", "--output", "json"}},
		{"window activate", []string{"window", "activate", "Editor"}},
		{"window activate by id", []string{"window", "activate-id", "window-id"}},
		{"window active title", []string{"window", "active"}},
		{"window move", []string{"window", "move", "--title", "Editor", "--x", "11", "--y", "20"}},
		{"window resize", []string{"window", "resize", "--title", "Editor", "--w", "300", "--h", "200"}},
		{"window fullscreen", []string{"window", "fullscreen", "Editor"}},
		{"window unfullscreen", []string{"window", "unfullscreen", "Editor"}},
		{"window find", []string{"window", "find", "title=Editor"}},
		{"window wait", []string{"window", "wait-for", "title=Editor"}},
		{"window wait close", []string{"window", "wait-close", "title=Editor"}},
		{"window geometry", []string{"window", "get-geometry", "Editor"}},
		{"window by title", []string{"window", "find-by-title", "Editor"}},
		{"window visible", []string{"window", "is-visible", "Editor"}},
		{"window watch", []string{"window", "watch"}},
		{"window close", []string{"window", "close", "Editor"}},
		{"window close id", []string{"window", "close-id", "window-id"}},
		{"window minimize", []string{"window", "minimize", "Editor"}},
		{"screen grab", []string{"screen", "grab", "--rect", "0,0,2,2"}},
		{"screen hash", []string{"screen", "hash", "--rect", "0,0,2,2"}},
		{"screen pixel", []string{"screen", "pixel", "--x", "1", "--y", "1"}},
		{"screen all pixels", []string{"screen", "get-all-pixels"}},
		{"screen grab region", []string{"screen", "grab-region", "--rect", "0,0,2,2"}},
		{"screen watch", []string{"screen", "watch", "--duration", "1ms"}},
		{"screen resolution", []string{"screen", "resolution"}},
		{"screen wait predicate", []string{"screen", "wait-for-fn", "--timeout", "1ms"}},
		{"screen wait settle", []string{"screen", "wait-for-settle", "--timeout", "1ms"}},
		{"screen pixel batch", []string{"screen", "get-multiple-pixels", "--points", "1,2;3,4"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opened := 0
			cmd := newRootCmd(func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { opened++; return nil, denied }
			})
			var stdout, stderr bytes.Buffer
			cmd.SetArgs(tc.args)
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			err := cmd.ExecuteContext(context.Background())
			if !errors.Is(err, denied) {
				t.Fatalf("denial not propagated for %v: err=%v stdout=%q stderr=%q", tc.args, err, stdout.String(), stderr.String())
			}
			if opened != 1 {
				t.Fatalf("expected exactly one session authorization for %v, got %d", tc.args, opened)
			}
			if stdout.Len() != 0 {
				t.Fatalf("denied command published partial success: %q", stdout.String())
			}
		})
	}
}
