package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
)

// Incomplete semantic requests must not initialize the AT-SPI desktop,
// especially when opening the session may trigger user authorization.
func TestAccessibilitySemanticCommandsRequireScopeBeforeOpeningSession(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"tree", []string{"a11y", "tree"}},
		{"find", []string{"a11y", "find", "--name", "Save"}},
		{"action", []string{"a11y", "action", "--role", "button", "--action", "press"}},
		{"focus", []string{"a11y", "focus", "--role", "textbox"}},
		{"text", []string{"a11y", "text", "--role", "entry", "--value", "confidential"}},
		{"empty app scope", []string{"a11y", "tree", "--app", "   "}},
		{"empty window scope", []string{"a11y", "focus", "--window", " "}},
		{"explicit zero PID", []string{"a11y", "find", "--pid", "0"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			attempts := 0
			root := newRootCmd(func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					attempts++
					return nil, errors.New("unexpected accessibility session authorization")
				}
			})
			var out, errOut bytes.Buffer
			root.SetArgs(tc.args)
			root.SetOut(&out)
			root.SetErr(&errOut)
			err := root.ExecuteContext(context.Background())
			if err == nil || !strings.Contains(err.Error(), "explicit --app") {
				t.Fatalf("missing scope returned %v", err)
			}
			if attempts != 0 {
				t.Fatalf("scope-free request accessed desktop %d times", attempts)
			}
			if out.Len() != 0 {
				t.Fatalf("unscoped request produced output: %q", out.String())
			}
		})
	}
}
