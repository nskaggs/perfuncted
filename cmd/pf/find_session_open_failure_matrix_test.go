package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
)

// Every find command needs a real session before it can observe screen data.
// A failed open must never be converted to a successful match, pixel, or hash;
// retrying a failed open within the command would also duplicate authorization.
func TestFindCommandsRejectFailedSessionOpenWithoutPublishingResults(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"wait for", []string{"find", "wait-for", "--hash", "0x12"}},
		{"wait for change", []string{"find", "wait-for-change", "--initial", "0x12"}},
		{"wait for change capture", []string{"find", "wait-for-change", "--capture-initial"}},
		{"wait for no change", []string{"find", "wait-for-no-change"}},
		{"scan for", []string{"find", "scan-for", "--rects", "0,0,8,8", "--wants", "0x12"}},
		{"wait for visible change", []string{"find", "wait-for-visible-change"}},
		{"color", []string{"find", "color", "--color", "0c2238"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attempts := 0
			failure := errors.New("synthetic session authorization refused")
			stdout, stderr, exit := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { attempts++; return nil, failure }
			})
			if attempts != 1 || exit == 0 || !strings.Contains(stderr, failure.Error()) || stdout != "" {
				t.Fatalf("failed find session args=%v attempts=%d exit=%d stdout=%q stderr=%q", tc.args, attempts, exit, stdout, stderr)
			}
		})
	}
}
