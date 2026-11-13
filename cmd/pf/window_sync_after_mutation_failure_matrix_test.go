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

type deniedWindowSync struct {
	*pftest.Manager
	syncErr   error
	syncCalls int
}

func (m *deniedWindowSync) Sync(context.Context) error { m.syncCalls++; return m.syncErr }

// A successful compositor mutation is not a verified completed operation if
// the explicitly requested synchronization fails afterward. In particular,
// callers must not publish a success receipt on a failed sync barrier.
func TestWindowMutationsDoNotAcknowledgeFailedSynchronization(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"activate by title", []string{"window", "activate", "Editor"}},
		{"activate by id", []string{"window", "activate-id", "editor-id"}},
		{"move", []string{"window", "move", "--title", "Editor", "--x", "23", "--y", "24"}},
		{"resize", []string{"window", "resize", "--title", "Editor", "--w", "640", "--h", "480"}},
		{"fullscreen", []string{"window", "fullscreen", "Editor"}},
		{"unfullscreen", []string{"window", "unfullscreen", "Editor"}},
		{"close by id", []string{"window", "close-id", "editor-id"}},
		{"close by title", []string{"window", "close", "Editor"}},
		{"minimize", []string{"window", "minimize", "Editor"}},
		{"maximize", []string{"window", "maximize", "Editor"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failure := errors.New("synthetic compositor sync barrier failed")
			m := &deniedWindowSync{Manager: &pftest.Manager{Lists: [][]window.Info{{{NativeID: "editor-id", Title: "Editor", X: 10, Y: 20, W: 300, H: 220}}}}, syncErr: failure}
			opens := 0
			args := append([]string{"--sync"}, tc.args...)
			stdout, stderr, exit := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { opens++; return pftest.New(nil, nil, m, nil), nil }
			})
			if opens != 1 || m.syncCalls != 1 || exit == 0 || stdout != "" || !strings.Contains(stderr, failure.Error()) {
				t.Fatalf("failed sync misreported as success args=%v opens=%d syncs=%d exit=%d out=%q err=%q", args, opens, m.syncCalls, exit, stdout, stderr)
			}
		})
	}
}
