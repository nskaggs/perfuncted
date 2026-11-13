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

// Window reads and mutations may have completed successfully, but a failed
// output sink must never be acknowledged as a delivered result. Test both
// encoding modes and multi-line records, including failure after a first line.
func TestWindowCLIPropagatesOutputDisconnection(t *testing.T) {
	denied := errors.New("synthetic window output disconnected")
	info := window.Info{NativeID: "editor-id", ID: 55, Title: "Editor", X: 10, Y: 20, W: 300, H: 220, PID: 42}
	for _, tc := range []struct {
		name                   string
		args                   []string
		failAt, expectedWrites int
	}{
		{"plain list", []string{"window", "list"}, 1, 1},
		{"JSON list", []string{"window", "list", "--output=json"}, 1, 1},
		{"active title", []string{"window", "active"}, 1, 1},
		{"geometry plain", []string{"window", "get-geometry", "Editor"}, 1, 1},
		{"geometry JSON", []string{"window", "get-geometry", "Editor", "--output=json"}, 1, 1},
		{"find by title first line", []string{"window", "find-by-title", "Editor"}, 1, 1},
		{"find by title second line", []string{"window", "find-by-title", "Editor"}, 2, 2},
		{"find by title third line", []string{"window", "find-by-title", "Editor"}, 3, 3},
		{"activate by title", []string{"window", "activate", "Editor"}, 1, 1},
		{"activate by ID", []string{"window", "activate-id", "editor-id"}, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := &pftest.Manager{Lists: [][]window.Info{{info}}, Titles: []string{"Editor"}}
			opens := 0
			root := newRootCmd(func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opens++
					return pftest.New(nil, nil, manager, nil), nil
				}
			})
			root.SilenceUsage = true
			root.SilenceErrors = true
			w := &failWindowNthWrite{failAt: tc.failAt, failure: denied}
			var stderr bytes.Buffer
			root.SetOut(w)
			root.SetErr(&stderr)
			root.SetArgs(tc.args)
			err := root.ExecuteContext(context.Background())
			if !errors.Is(err, denied) || opens != 1 || w.count != tc.expectedWrites {
				t.Fatalf("failed output accepted args=%v err=%v opens=%d writes=%d expected=%d accepted=%q", tc.args, err, opens, w.count, tc.expectedWrites, w.sent.String())
			}
		})
	}
}
