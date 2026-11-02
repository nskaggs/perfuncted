package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted"
)

// Every valid CLI operation must stop at an unavailable accessibility session
// rather than emitting fabricated success or reaching a privileged primitive.
func TestAccessibilityCLIValidCommandsPropagateAuthorizationDenial(t *testing.T) {
	denied := errors.New("synthetic AT-SPI authorization denied")
	for _, tc := range []struct {
		name   string
		args   []string
		window bool
	}{
		{"apps", []string{"apps"}, false},
		{"tree scoped", []string{"tree", "--app", "Editor"}, false},
		{"find scoped", []string{"find", "--app", "Editor", "--name", "Save"}, false},
		{"focused", []string{"focused"}, false},
		{"at point default", []string{"at-point"}, false},
		{"at point coordinates", []string{"at-point", "14", "18"}, false},
		{"events", []string{"events", "--buffer", "2"}, false},
		{"semantic action", []string{"action", "--app", "Editor", "--role", "button"}, false},
		{"semantic focus", []string{"focus", "--app", "Editor", "--role", "textbox"}, false},
		{"semantic text", []string{"text", "--app", "Editor", "--role", "entry", "--value", "new"}, false},
		{"raw focus", []string{"raw", "--op", "focus", "--bus", "org.test.App", "--path", "/node", "--generation", "1"}, false},
		{"raw reopen", []string{"raw", "--op", "reopen"}, false},
		{"window scoped tree", []string{"tree", "--window-id", "123"}, true},
		{"window scoped action", []string{"action", "--window", "Editor", "--role", "button"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			direct, windowOpens := 0, 0
			mainOpen := func(context.Context) (*perfuncted.Session, error) { direct++; return nil, denied }
			windowOpen := func(context.Context) (*perfuncted.Session, error) { windowOpens++; return nil, denied }
			cmd := accessibilityCmd(mainOpen, windowOpen)
			// Production's root command suppresses Cobra usage text on
			// operational failures. Preserve that contract in this direct harness.
			cmd.SilenceUsage = true
			var out, errOut bytes.Buffer
			cmd.SetArgs(tc.args)
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			err := cmd.ExecuteContext(context.Background())
			if !errors.Is(err, denied) {
				t.Fatalf("authorization failure lost: %v", err)
			}
			if out.Len() != 0 {
				t.Fatalf("denied action published result: %q", out.String())
			}
			if tc.window {
				if direct != 0 || windowOpens != 1 {
					t.Fatalf("wrong session opened: main=%d window=%d", direct, windowOpens)
				}
			} else if direct != 1 || windowOpens != 0 {
				t.Fatalf("wrong session opened: main=%d window=%d", direct, windowOpens)
			}
		})
	}
}
