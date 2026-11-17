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

func TestAccessibilityAtPointRejectsMalformedCoordinatesBeforeOpeningSession(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"one coordinate", []string{"1"}, "expects zero or two coordinates"},
		{"invalid x", []string{"left", "2"}, "at-point x"},
		{"invalid y", []string{"1", "down"}, "at-point y"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opens := 0
			args := append([]string{"a11y", "at-point"}, tc.args...)
			out, stderr, code := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opens++
					return nil, errors.New("unexpected session open")
				}
			})
			if code == 0 || out != "" || !strings.Contains(stderr, tc.want) || opens != 0 {
				t.Fatalf("malformed coordinates reached session: args=%v code=%d stdout=%q stderr=%q opens=%d", args, code, out, stderr, opens)
			}
		})
	}
}

func TestAccessibilityWindowTitleAndIDConflictFailsBeforeATSPICorrelation(t *testing.T) {
	opens := 0
	args := []string{"a11y", "tree", "--window", "Editor", "--window-id", "different-id"}
	out, stderr, code := captureRunIO(t, args, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) {
			opens++
			manager := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "managed-id", Title: "Editor"}}}}
			return perfuncted.NewSessionForTesting(nil, nil, manager, nil, nil, cliAutomationFake{}), nil
		}
	})
	if code == 0 || out != "" || !strings.Contains(stderr, "identify different managed windows") || opens != 1 {
		t.Fatalf("conflicting managed window selectors accepted: code=%d stdout=%q stderr=%q opens=%d", code, out, stderr, opens)
	}
}
