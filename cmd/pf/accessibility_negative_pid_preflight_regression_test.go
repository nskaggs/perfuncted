package main

import (
	"context"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
)

// Invalid process IDs must not trigger AT-SPI authorization or access.
func TestAccessibilityNegativePIDFailsBeforeOpeningSession(t *testing.T) {
	for _, args := range [][]string{
		{"a11y", "tree", "--pid=-1"},
		{"a11y", "find", "--pid=-50"},
		{"a11y", "action", "--pid=-2", "--action", "press"},
		{"a11y", "focus", "--pid=-3"},
		{"a11y", "text", "--pid=-4"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			opened := 0
			_, errText, code := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opened++
					return nil, nil
				}
			})
			if opened != 0 {
				t.Fatalf("opened session %d times for invalid PID", opened)
			}
			if code == 0 || !strings.Contains(strings.ToLower(errText), "pid") {
				t.Fatalf("code=%d stderr=%q", code, errText)
			}
		})
	}
}
