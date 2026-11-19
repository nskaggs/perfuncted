package main

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
)

type watchRejectingWriter struct{ attempts int }

func (w *watchRejectingWriter) Write([]byte) (int, error) {
	w.attempts++
	return 0, io.ErrClosedPipe
}

// A live watch must not silently claim success when either its backend query
// or its consumer stream fails. No actual compositor or display is accessed.
func TestWindowWatchPropagatesBackendAndStreamFailures(t *testing.T) {
	failure := errors.New("synthetic compositor list failure")
	for _, tc := range []struct {
		name          string
		mode          string
		providerError error
		expected      error
		writes        int
	}{
		{name: "backend failure", mode: "json", providerError: failure, expected: failure},
		{name: "json consumer closed", mode: "json", expected: io.ErrClosedPipe, writes: 1},
		{name: "plain consumer closed", mode: "plain", expected: io.ErrClosedPipe, writes: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "synthetic-window", Title: "Editor"}}}, Err: tc.providerError}
			opens := 0
			cmd := windowCmd(func(context.Context) (*perfuncted.Session, error) {
				opens++
				return pftest.New(nil, nil, manager, nil), nil
			}, nil)
			writer := &watchRejectingWriter{}
			cmd.SetOut(writer)
			cmd.SetErr(io.Discard)
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			cmd.SetArgs([]string{"watch", "--output", tc.mode})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			err := cmd.ExecuteContext(ctx)
			if !errors.Is(err, tc.expected) {
				t.Fatalf("lost watch failure: %v (expected %v)", err, tc.expected)
			}
			if opens != 1 || writer.attempts != tc.writes {
				t.Fatalf("watch opened %d sessions and attempted %d writes, expected %d", opens, writer.attempts, tc.writes)
			}
		})
	}
}
