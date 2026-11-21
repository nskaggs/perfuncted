package main

import (
	"context"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
)

// An activation event is not an acknowledgement of focus. Only an observed
// transition of the selected stable window may authorize a verified success.
func TestWindowCLIReportsVerifiedFocusOnlyAfterSelectedWindowBecomesActive(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"title", []string{"window", "activate", "Editor", "--verify", "--verify-timeout=250ms", "--verify-poll=1ms"}},
		{"stable ID", []string{"window", "activate-id", "editor", "--verify", "--verify-timeout=250ms", "--verify-poll=1ms"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inactive := window.Info{NativeID: "editor", Title: "Editor", Active: false}
			active := inactive
			active.Active = true
			mgr := &pftest.Manager{Lists: [][]window.Info{{inactive}, {inactive}, {active}}}
			out, stderr, code := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					return pftest.New(nil, nil, mgr, nil), nil
				}
			})
			if code != 0 || !strings.Contains(out, "activation verified: ") || len(mgr.Activated) != 1 || mgr.Activated[0] != "editor" {
				t.Fatalf("selected window focus not verified: code=%d stdout=%q stderr=%q activated=%q", code, out, stderr, mgr.Activated)
			}
		})
	}
}

// A close request alone is insufficient: observe the stable target disappear
// from an independently refreshed window listing before reporting success.
func TestWindowCLIReportsVerifiedCloseOnlyAfterTargetDisappears(t *testing.T) {
	info := window.Info{NativeID: "dialog", Title: "Save dialog"}
	mgr := &pftest.Manager{Lists: [][]window.Info{{info}, {info}, {}}}
	out, stderr, code := captureRunIO(t, []string{"window", "close-id", "dialog", "--verify", "--verify-timeout=250ms", "--verify-poll=1ms"}, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) {
			return pftest.New(nil, nil, mgr, nil), nil
		}
	})
	if code != 0 || !strings.Contains(out, "close verified: dialog") {
		t.Fatalf("disappearance verification not acknowledged: code=%d stdout=%q stderr=%q", code, out, stderr)
	}
}
