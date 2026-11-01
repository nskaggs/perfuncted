package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
)

type inputSyncOnlyFailure struct {
	*pftest.Inputter
	syncFailure error
	calls       int
}

func (f *inputSyncOnlyFailure) Sync(context.Context) error { f.calls++; return f.syncFailure }

// Every --sync command must finish its input action, then refuse to report
// success if the compositor does not acknowledge the synchronization barrier.
// A mock that fails every input call would never reach these branches.
func TestInputCommandsRejectPostMutationSynchronizationFailures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		wantCall string
	}{
		{"move", []string{"move", "--x", "5", "--y", "9"}, "move:5,9"},
		{"click", []string{"click", "--x", "4", "--y", "8"}, "click:4,8"},
		{"double click", []string{"double-click"}, "mousedown"},
		{"drag", []string{"drag-and-drop", "--x1", "1", "--y1", "2", "--x2", "7", "--y2", "8"}, "move:1,2"},
		{"center", []string{"click-center", "--rect", "0,0,10,10"}, "click:5,5"},
		{"type", []string{"type", "hello"}, "type:hello"},
		{"keydown", []string{"keydown", "ctrl"}, "down:ctrl"},
		{"keyup", []string{"keyup", "ctrl"}, "up:ctrl"},
		{"mousedown", []string{"mousedown", "--button", "2"}, "mousedown"},
		{"mouseup", []string{"mouseup", "--button", "2"}, "mouseup"},
		{"scroll up", []string{"scroll", "up", "--clicks", "2"}, "scroll-up:2"},
		{"scroll down", []string{"scroll", "down", "--clicks", "2"}, "scroll-down:2"},
		{"scroll left", []string{"scroll", "left", "--clicks", "2"}, "scroll-left:2"},
		{"scroll right", []string{"scroll", "right", "--clicks", "2"}, "scroll-right:2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errSync := errors.New("synthetic compositor barrier failed")
			input := &inputSyncOnlyFailure{Inputter: &pftest.Inputter{}, syncFailure: errSync}
			args := append([]string{"--sync", "input"}, tc.args...)
			out, stderr, exit := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, input, nil, nil), nil }
			})
			if exit == 0 || out != "" || !strings.Contains(stderr, errSync.Error()) || input.calls != 1 {
				t.Fatalf("sync failure was hidden: args=%v status=%d out=%q stderr=%q syncCalls=%d inputCalls=%v", args, exit, out, stderr, input.calls, input.Inputter.Calls)
			}
			if len(input.Inputter.Calls) == 0 {
				t.Fatal("sync called without performing any input mutation")
			}
			found := false
			for _, call := range input.Inputter.Calls {
				if call == tc.wantCall {
					found = true
				}
			}
			if !found {
				t.Fatalf("mutating action %q was not executed before sync: %v", tc.wantCall, input.Inputter.Calls)
			}
		})
	}
}
