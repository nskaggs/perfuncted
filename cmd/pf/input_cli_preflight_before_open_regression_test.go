package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
)

// Argument validation must precede any desktop session acquisition. Opening
// portal-backed input just to reject bad syntax can prompt for permission and
// initialize remote input channels even though no operation is possible.
func TestInputInvalidRuntimeArgumentsDoNotAcquireDesktopSession(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"invalid click delay", []string{"input", "click", "--delay", "nonsense"}},
		{"missing typed text", []string{"input", "type"}},
		{"invalid center rectangle", []string{"input", "click-center", "--rect", "wrong"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opens := 0
			out, errOut, exit := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opens++
					return nil, errors.New("unnecessary desktop session acquisition")
				}
			})
			if exit == 0 || opens != 0 || strings.Contains(out, "clicked") {
				t.Fatalf("invalid input %v opened %d sessions, exit=%d out=%q err=%q", tc.args, opens, exit, out, errOut)
			}
		})
	}
}
