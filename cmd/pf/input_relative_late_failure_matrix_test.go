package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
)

type relativeSyncFailureInput struct {
	*relativePointerInput
	err   error
	calls int
}

func (i *relativeSyncFailureInput) Sync(context.Context) error { i.calls++; return i.err }

// Relative moves first verify an authoritative logical coordinate space and
// read a fresh cursor location. Failure during the actual move or the
// subsequently requested sync must never produce a successful move receipt.
func TestRelativePointerCLIPropagatesLateMovementAndSyncFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		sync bool
	}{
		{"movement refused", false},
		{"post-movement synchronization refused", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failure := errors.New("synthetic pointer backend denied late operation")
			i, w := newRelativePointerBackend(100, 200)
			input := &relativeSyncFailureInput{relativePointerInput: i, err: failure}
			if !tc.sync {
				i.Inputter.Err = failure
			}
			opens := 0
			args := []string{"input", "move-relative", "--dx=7", "--dy=-3", "--verify=false"}
			if tc.sync {
				args = append([]string{"--sync"}, args...)
			}
			stdout, stderr, exit := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opens++
					return perfuncted.NewSessionForTesting(nil, input, w, nil, nil), nil
				}
			})
			expectedSyncs := 0
			if tc.sync {
				expectedSyncs = 1
			}
			if exit == 0 || stdout != "" || !strings.Contains(stderr, failure.Error()) || opens != 1 || input.calls != expectedSyncs {
				t.Fatalf("late failure misreported args=%v exit=%d out=%q err=%q opens=%d syncs=%d", args, exit, stdout, stderr, opens, input.calls)
			}
			if tc.sync && (len(i.Calls) != 1 || i.Calls[0] != "move:107,197") {
				t.Fatalf("synchronization attempted without a successful move: %v", i.Calls)
			}
			if !tc.sync && (len(i.Calls) != 1 || i.Calls[0] != "move:107,197" || i.x != 100 || i.y != 200) {
				t.Fatalf("failed move changed pointer position or attempted unexpected calls: position=%d,%d calls=%v", i.x, i.y, i.Calls)
			}
		})
	}
}

// The command factory may be used without a window-capable session opener.
// It must reject relative motion before attempting input authorization.
func TestRelativePointerRequiresWindowOpenerBeforeAuthorization(t *testing.T) {
	opens := 0
	cmd := inputCmd(func(context.Context) (*perfuncted.Session, error) {
		opens++
		return nil, errors.New("unexpected desktop authorization")
	}, nil)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"move-relative", "--dx=7"})
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	err := cmd.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "requires window capability") || stdout.Len() != 0 || opens != 0 {
		t.Fatalf("unsupported relative motion requested desktop access err=%v opens=%d out=%q", err, opens, stdout.String())
	}
}
