package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
)

// A native KWin tile request is not success until it can be confirmed by a
// fresh backend read. Missing targets and permission failures are equally
// terminal: never report a tile or float operation that did not complete.
func TestKDETileAndFloatCommandsPreserveLateFailures(t *testing.T) {
	denied := errors.New("synthetic authorization rejected")
	stale := errors.New("synthetic window info read failed after mutation")
	for _, tc := range []struct {
		name          string
		args          []string
		openerFailure error
		missing       bool
		stale         bool
		want          string
		action        string
	}{
		{name: "tile authorization", args: []string{"window", "tile", "Editor", "--x=13", "--y=17"}, openerFailure: denied, want: denied.Error()},
		{name: "float authorization", args: []string{"window", "float", "Editor"}, openerFailure: denied, want: denied.Error()},
		{name: "tile missing target", args: []string{"window", "tile", "Missing", "--x=13", "--y=17"}, missing: true, want: "not found"},
		{name: "tile verification read fails", args: []string{"window", "tile", "Editor", "--x=13", "--y=17"}, stale: true, want: "native window tiling not confirmed", action: "tile:uuid-a"},
		{name: "float verification read fails", args: []string{"window", "float", "Editor"}, stale: true, want: "native window floating not confirmed", action: "float:uuid-a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newKDEWindowState()
			if tc.stale {
				m.readErr = stale
			}
			opens := 0
			stdout, stderr, code := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opens++
					if tc.openerFailure != nil {
						return nil, tc.openerFailure
					}
					return perfuncted.NewSessionForTesting(nil, nil, m, nil, nil), nil
				}
			})
			if code == 0 || stdout != "" || !strings.Contains(stderr, tc.want) || opens != 1 || m.action != tc.action {
				t.Fatalf("rejected tile/float code=%d stdout=%q stderr=%q opens=%d action=%q (want %q, action %q)", code, stdout, stderr, opens, m.action, tc.want, tc.action)
			}
			if tc.stale && !strings.Contains(stderr, stale.Error()) {
				t.Fatalf("verification error lost: %q", stderr)
			}
		})
	}
}
