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

type deniedStableWindowManager struct {
	*pftest.Manager
	rejection error
	attempted []string
}

func (m *deniedStableWindowManager) reject(name string) error {
	m.attempted = append(m.attempted, name)
	return m.rejection
}
func (m *deniedStableWindowManager) ActivateByID(context.Context, string) error {
	return m.reject("activate")
}
func (m *deniedStableWindowManager) MoveByID(context.Context, string, int, int) error {
	return m.reject("move")
}
func (m *deniedStableWindowManager) ResizeByID(context.Context, string, int, int) error {
	return m.reject("resize")
}
func (m *deniedStableWindowManager) FullscreenByID(context.Context, string) error {
	return m.reject("fullscreen")
}
func (m *deniedStableWindowManager) UnfullscreenByID(context.Context, string) error {
	return m.reject("unfullscreen")
}
func (m *deniedStableWindowManager) CloseWindowByID(context.Context, string) error {
	return m.reject("close")
}
func (m *deniedStableWindowManager) MinimizeByID(context.Context, string) error {
	return m.reject("minimize")
}
func (m *deniedStableWindowManager) MaximizeByID(context.Context, string) error {
	return m.reject("maximize")
}

// Normal discovery must resolve the exact window before the rejecting backend
// is invoked. An operation rejected by the compositor must never print a
// success message (or attempt an alternate title-based operation).
func TestWindowCommandsReportRealActionRejectionWithoutFalseSuccess(t *testing.T) {
	failure := errors.New("synthetic compositor refused window mutation")
	cases := []struct {
		name   string
		args   []string
		action string
	}{
		{"activate", []string{"window", "activate", "Editor"}, "activate"},
		{"activate-id", []string{"window", "activate-id", "private-id"}, "activate"},
		{"move", []string{"window", "move", "--title", "Editor", "--x", "12", "--y", "23"}, "move"},
		{"resize", []string{"window", "resize", "--title", "Editor", "--w", "101", "--h", "102"}, "resize"},
		{"fullscreen", []string{"window", "fullscreen", "Editor"}, "fullscreen"},
		{"unfullscreen", []string{"window", "unfullscreen", "Editor"}, "unfullscreen"},
		{"close", []string{"window", "close", "Editor"}, "close"},
		{"close-id", []string{"window", "close-id", "private-id"}, "close"},
		{"minimize", []string{"window", "minimize", "Editor"}, "minimize"},
		{"maximize", []string{"window", "maximize", "Editor"}, "maximize"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manager := &deniedStableWindowManager{Manager: &pftest.Manager{Lists: [][]window.Info{{{NativeID: "private-id", Title: "Editor", X: 10, Y: 20, W: 200, H: 100}}}}, rejection: failure}
			opens := 0
			cmd := newRootCmd(func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opens++
					return pftest.New(nil, nil, manager, nil), nil
				}
			})
			var out, stderr bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&stderr)
			cmd.SetArgs(tc.args)
			err := cmd.ExecuteContext(context.Background())
			if !errors.Is(err, failure) {
				t.Fatalf("provider rejection lost for %v: %v", tc.args, err)
			}
			if opens != 1 {
				t.Fatalf("unexpected session count %d", opens)
			}
			if len(manager.attempted) != 1 || manager.attempted[0] != tc.action {
				t.Fatalf("wrong backend operation: %v expected %s", manager.attempted, tc.action)
			}
			if out.Len() != 0 {
				t.Fatalf("rejected operation reported success: %q", out.String())
			}
		})
	}
}
