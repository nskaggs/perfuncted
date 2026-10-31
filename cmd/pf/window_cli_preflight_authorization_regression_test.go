package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
)

// Invalid local window selectors, timeouts and output formats must not open
// a desktop session; session acquisition can prompt for portal authorization.
func TestWindowCLIInvalidLocalArgumentsDoNotOpenDesktopSession(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"unknown list output", []string{"window", "list", "--output", "toml"}},
		{"invalid find match", []string{"window", "find", "unknown-field=value"}},
		{"invalid wait-for match", []string{"window", "wait-for", "unknown-field=value"}},
		{"invalid wait-close match", []string{"window", "wait-close", "unknown-field=value"}},
		{"invalid wait-for poll", []string{"window", "wait-for", "title=Editor", "--poll", "wrong"}},
		{"invalid wait-for timeout", []string{"window", "wait-for", "title=Editor", "--timeout", "wrong"}},
		{"invalid wait-close poll", []string{"window", "wait-close", "title=Editor", "--poll", "wrong"}},
		{"invalid wait-close timeout", []string{"window", "wait-close", "title=Editor", "--timeout", "wrong"}},
		{"invalid move x", []string{"window", "move", "--title", "Editor", "--x", "wrong"}},
		{"invalid move y", []string{"window", "move", "--title", "Editor", "--y", "wrong"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opened := 0
			out, errOut, code := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opened++
					return nil, errors.New("unexpected access to desktop session")
				}
			})
			if code == 0 || opened != 0 || out != "" || strings.Contains(errOut, "unexpected access to desktop session") {
				t.Fatalf("invalid %v accessed %d sessions: code=%d stdout=%q stderr=%q", tc.args, opened, code, out, errOut)
			}
		})
	}
}
