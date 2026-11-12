//go:build linux

package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/accessibility"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
)

type strictWindowApplicationFixture struct {
	cliAutomationFake
	findCalls      int
	observedFilter accessibility.ApplicationFilter
	scope          accessibility.WindowScope
}

func (f *strictWindowApplicationFixture) FindApplication(_ context.Context, filter accessibility.ApplicationFilter) (accessibility.Application, error) {
	f.findCalls++
	f.observedFilter = filter
	if filter.WindowID != "" {
		return accessibility.Application{}, errors.New("AT-SPI application path is not a compositor window ID")
	}
	return f.scope.Application, nil
}
func (f *strictWindowApplicationFixture) ResolveWindow(_ context.Context, target accessibility.WindowTarget) (accessibility.WindowScope, error) {
	scope := f.scope
	scope.WindowID = target.ID
	return scope, nil
}

func TestAccessibilityWindowAndAppSelectorsUseCorrelatedApplicationIdentity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		extra  []string
		wantOK bool
	}{
		{name: "matching app", extra: []string{"--app", "Test App"}, wantOK: true},
		{name: "matching PID", extra: []string{"--pid", "123"}, wantOK: true},
		{name: "matching app and PID", extra: []string{"--app", "  test  ", "--pid", "123"}, wantOK: true},
		{name: "wrong app", extra: []string{"--app", "Other"}},
		{name: "wrong PID", extra: []string{"--pid", "321"}},
		{name: "matching title and ID", extra: []string{"--window", "Test window", "--app", "Test App"}, wantOK: true},
		{name: "conflicting title and ID", extra: []string{"--window", "Other window", "--app", "Test App"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			appID := accessibility.NodeID{BusName: "org.test.App", ObjectPath: "/app", Generation: 1}
			f := &strictWindowApplicationFixture{scope: accessibility.WindowScope{
				Root:            accessibility.NodeID{BusName: "org.test.App", ObjectPath: "/window", Generation: 1},
				ApplicationRoot: appID,
				Application:     accessibility.Application{Node: accessibility.Node{ID: appID, Name: "Test App", Role: "application"}, PID: 123},
				PID:             123, Generation: 1,
			}}
			args := append([]string{"a11y", "tree", "--window-id", "managed-window"}, tc.extra...)
			out, stderr, exit := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					mgr := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "managed-window", Title: "Test window", PID: 123}}}}
					return perfuncted.NewSessionForTesting(nil, nil, mgr, nil, nil, f), nil
				}
			})
			if tc.wantOK {
				if exit != 0 || stderr != "" || !strings.Contains(out, "/window") {
					t.Fatalf("matching app/PID rejected: code=%d stderr=%q stdout=%q filter=%+v", exit, stderr, out, f.observedFilter)
				}
			} else if exit == 0 || out != "" || (!strings.Contains(stderr, "object not found") && !strings.Contains(stderr, "window: not found")) {
				t.Fatalf("mismatched app/PID incorrectly accepted: code=%d stderr=%q stdout=%q", exit, stderr, out)
			}
			if f.findCalls != 0 {
				t.Fatalf("native window ID incorrectly sent to AT-SPI application resolver: %+v", f.observedFilter)
			}
		})
	}
}
