package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
)

// A discovered window may be destroyed before the CLI refreshes its details.
// Discovery is not permission to report stale geometry or claim success.
type disappearingInfoWindowManager struct {
	*pftest.Manager
	failure   error
	infoCalls int
}

//nolint:unparam // This backend deliberately always fails refresh after a successful discovery.
func (m *disappearingInfoWindowManager) InfoByID(context.Context, string) (window.Info, error) {
	m.infoCalls++
	return window.Info{}, m.failure
}

func TestWindowCommandsRejectStaleWindowInfoBeforeOutputOrMutation(t *testing.T) {
	failure := errors.New("synthetic window disappeared before info refresh")
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"list matches", []string{"window", "find", "title:Editor"}},
		{"move using existing position", []string{"window", "move", "--title", "Editor", "--x", "-", "--y", "13"}},
		{"get geometry", []string{"window", "get-geometry", "Editor"}},
		{"find title", []string{"window", "find-by-title", "Editor"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := &disappearingInfoWindowManager{
				Manager: &pftest.Manager{Lists: [][]window.Info{{{NativeID: "private-window", Title: "Editor", X: 10, Y: 20, W: 640, H: 480}}}}, failure: failure,
			}
			cmd := newRootCmd(func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, nil, manager, nil), nil }
			})
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			cmd.SetArgs(tc.args)
			err := cmd.ExecuteContext(context.Background())
			if !errors.Is(err, failure) {
				t.Fatalf("stale info error missing: %v", err)
			}
			if manager.infoCalls == 0 {
				t.Fatalf("command did not refresh stale window before returning")
			}
			if stdout.Len() != 0 {
				t.Fatalf("command published stale window info: %q", stdout.String())
			}
		})
	}
}
