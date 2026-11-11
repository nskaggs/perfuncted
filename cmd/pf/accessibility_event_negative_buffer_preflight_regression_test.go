package main

import (
	"context"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
)

// Explicitly negative event buffering is invalid user input, not a request
// for a default. Refuse before acquiring accessibility authorization.
func TestAccessibilityEventsRejectNegativeBufferBeforeSession(t *testing.T) {
	for _, v := range []string{"-1", "-4096"} {
		t.Run(v, func(t *testing.T) {
			opened := 0
			_, stderr, exit := captureRunIO(t, []string{"a11y", "events", "--buffer=" + v}, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { opened++; return nil, nil }
			})
			if opened != 0 {
				t.Fatalf("negative buffer opened accessibility session %d times", opened)
			}
			if exit == 0 || !strings.Contains(stderr, "buffer") {
				t.Fatalf("exit=%d stderr=%q", exit, stderr)
			}
		})
	}
}
