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

type syntheticKDEWindowState struct {
	pftest.Manager
	info    window.Info
	action  string
	fail    error
	readErr error
}

func (m *syntheticKDEWindowState) SupportedOperations() []string {
	return []string{"discover", "info", "active-title", "set-window-desktop", "keep-above", "keep-below", "shade-when-supported", "tile-window", "float-window"}
}
func (m *syntheticKDEWindowState) InfoByID(_ context.Context, id string) (window.Info, error) {
	if m.readErr != nil {
		return window.Info{}, m.readErr
	}
	if id != m.info.NativeID {
		return window.Info{}, window.ErrWindowNotFound
	}
	return m.info, nil
}
func (m *syntheticKDEWindowState) SetWindowDesktopByID(_ context.Context, id, desktop string) error {
	m.action = "desktop:" + id + ":" + desktop
	if m.fail != nil {
		return m.fail
	}
	m.info.DesktopIDs = desktop
	return nil
}
func (m *syntheticKDEWindowState) SetKeepAboveByID(_ context.Context, id string, on bool) error {
	m.action = "above:" + id
	if m.fail != nil {
		return m.fail
	}
	m.info.KeepAbove = on
	return nil
}
func (m *syntheticKDEWindowState) SetKeepBelowByID(_ context.Context, id string, on bool) error {
	m.action = "below:" + id
	if m.fail != nil {
		return m.fail
	}
	m.info.KeepBelow = on
	return nil
}
func (m *syntheticKDEWindowState) SetShadeByID(_ context.Context, id string, on bool) error {
	m.action = "shade:" + id
	if m.fail != nil {
		return m.fail
	}
	m.info.Shaded = on
	return nil
}
func (m *syntheticKDEWindowState) TileWindowByID(_ context.Context, id string, x, y int) error {
	m.action = "tile:" + id
	if x != 13 || y != 17 {
		return errors.New("wrong tile coordinates")
	}
	if m.fail != nil {
		return m.fail
	}
	m.info.Tiled = true
	return nil
}
func (m *syntheticKDEWindowState) FloatWindowByID(_ context.Context, id string) error {
	m.action = "float:" + id
	if m.fail != nil {
		return m.fail
	}
	m.info.Tiled = false
	return nil
}

func runKDEWindowStateCLI(t *testing.T, m *syntheticKDEWindowState, args ...string) (string, string, int) {
	t.Helper()
	return captureRunIO(t, args, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) {
			return perfuncted.NewSessionForTesting(nil, nil, m, nil, nil), nil
		}
	})
}
func newKDEWindowState() *syntheticKDEWindowState {
	m := &syntheticKDEWindowState{info: window.Info{NativeID: "uuid-a", Title: "Editor", Active: true, Tiled: true, DesktopIDs: "desk-one"}}
	m.Lists = [][]window.Info{{m.info}}
	return m
}
func TestKDEWindowStateCommandsDispatchStableIDAndVerify(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		want   string
		action string
	}{
		{"active-info", []string{"window", "active-info"}, `"NativeID":"uuid-a"`, ""},
		{"set-desktop", []string{"window", "set-desktop", "Editor", "desk-two"}, "moved window uuid-a to desktop desk-two", "desktop:uuid-a:desk-two"},
		{"above", []string{"window", "keep-above", "Editor", "on"}, "keep-above true: uuid-a", "above:uuid-a"},
		{"below", []string{"window", "keep-below", "Editor", "on"}, "keep-below true: uuid-a", "below:uuid-a"},
		{"shade", []string{"window", "shade", "Editor", "on"}, "shade true: uuid-a", "shade:uuid-a"},
		{"tile", []string{"window", "tile", "Editor", "--x=13", "--y=17"}, "tiled window uuid-a at 13,17", "tile:uuid-a"},
		{"float", []string{"window", "float", "Editor"}, "floating window uuid-a", "float:uuid-a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newKDEWindowState()
			out, stderr, code := runKDEWindowStateCLI(t, m, tc.args...)
			if code != 0 || !strings.Contains(out, tc.want) || m.action != tc.action {
				t.Fatalf("code=%d output=%q stderr=%q action=%q want=%q", code, out, stderr, m.action, tc.action)
			}
		})
	}
}
func TestKDEWindowStateCommandFailuresDoNotClaimSuccess(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
		fail bool
	}{
		{"blank-tile-title", []string{"window", "tile", " "}, "window title cannot be blank", false},
		{"blank-float-title", []string{"window", "float", " "}, "window title cannot be blank", false},
		{"bad-above", []string{"window", "keep-above", "Editor", "maybe"}, "state must be on or off", false},
		{"bad-below", []string{"window", "keep-below", "Editor", "maybe"}, "state must be on or off", false},
		{"bad-shade", []string{"window", "shade", "Editor", "maybe"}, "shade state must be on or off", false},
		{"blank-desktop", []string{"window", "set-desktop", "Editor", " "}, "title and desktop ID must not be empty", false},
		{"missing-window", []string{"window", "float", "Missing"}, "window", false},
		{"backend-tile", []string{"window", "tile", "Editor", "--x=13", "--y=17"}, "synthetic mutation refused", true},
		{"backend-float", []string{"window", "float", "Editor"}, "synthetic mutation refused", true},
		{"backend-shade", []string{"window", "shade", "Editor", "on"}, "synthetic mutation refused", true},
		{"backend-desktop", []string{"window", "set-desktop", "Editor", "desk-two"}, "synthetic mutation refused", true},
		{"backend-above", []string{"window", "keep-above", "Editor", "on"}, "synthetic mutation refused", true},
		{"backend-below", []string{"window", "keep-below", "Editor", "on"}, "synthetic mutation refused", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newKDEWindowState()
			if tc.fail {
				m.fail = errors.New("synthetic mutation refused")
			}
			out, stderr, code := runKDEWindowStateCLI(t, m, tc.args...)
			if code == 0 || !strings.Contains(stderr, tc.want) {
				t.Fatalf("code=%d out=%q stderr=%q want=%q", code, out, stderr, tc.want)
			}
		})
	}
}
