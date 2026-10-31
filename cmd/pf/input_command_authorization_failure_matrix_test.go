package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
)

// Refusing authorization must stop every input command before it can queue
// keystrokes, pointer events, or a success report. Each case is executed by
// Cobra, rather than calling a command callback in isolation.
func TestInputCommandsPropagateSessionAuthorizationFailure(t *testing.T) {
	denied := errors.New("synthetic private input authorization denied")
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"move", []string{"input", "move", "--x", "12", "--y", "8"}},
		{"click", []string{"input", "click", "--x", "12", "--y", "8", "--repeat", "3"}},
		{"double click", []string{"input", "double-click", "--x", "12", "--y", "8"}},
		{"drag", []string{"input", "drag-and-drop", "--x1", "1", "--y1", "2", "--x2", "30", "--y2", "40"}},
		{"center", []string{"input", "click-center", "--rect", "0,0,10,10"}},
		{"type", []string{"input", "type", "private payload"}},
		{"keydown", []string{"input", "keydown", "shift"}},
		{"keyup", []string{"input", "keyup", "shift"}},
		{"mousedown", []string{"input", "mousedown", "--x", "12", "--y", "8"}},
		{"mouseup", []string{"input", "mouseup", "--x", "12", "--y", "8"}},
		{"location", []string{"input", "location"}},
		{"coordinate space", []string{"input", "coordinate-space"}},
		{"scroll up", []string{"input", "scroll", "up", "--clicks", "2"}},
		{"scroll down", []string{"input", "scroll", "down", "--clicks", "2"}},
		{"scroll left", []string{"input", "scroll", "left", "--clicks", "2"}},
		{"scroll right", []string{"input", "scroll", "right", "--clicks", "2"}},
		{"sync", []string{"input", "sync"}},
		{"window click", []string{"input", "click-window", "--id", "synthetic-window", "--x", "1", "--y", "2"}},
		{"screen click", []string{"input", "click-screen", "--x", "1", "--y", "2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opens := 0
			root := newRootCmd(func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { opens++; return nil, denied }
			})
			var out, stderr bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&stderr)
			root.SetArgs(tc.args)
			err := root.ExecuteContext(context.Background())
			if !errors.Is(err, denied) {
				t.Fatalf("authorization failure lost: command=%v err=%v", tc.args, err)
			}
			if opens != 1 {
				t.Fatalf("expected exactly one authorization attempt, got %d", opens)
			}
			if strings.Contains(out.String(), "sent") || strings.Contains(out.String(), "clicked") || strings.Contains(out.String(), "moved") {
				t.Fatalf("unauthorized action falsely reported as successful: %q", out.String())
			}
		})
	}
}
