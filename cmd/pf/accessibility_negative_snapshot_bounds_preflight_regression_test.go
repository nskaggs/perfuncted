package main

import (
	"context"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
)

func TestAccessibilityNegativeSnapshotBoundsFailBeforeSession(t *testing.T) {
	for _, tc := range []struct {
		flag    string
		command string
	}{
		{"--max-depth=-1", "tree"},
		{"--max-nodes=-2", "find"},
		{"--max-text-bytes=-3", "action"},
		{"--max-total-bytes=-4", "focus"},
		{"--max-depth=-5", "text"},
	} {
		t.Run(tc.command+" "+tc.flag, func(t *testing.T) {
			args := []string{"a11y", tc.command, "--app", "Example", tc.flag}
			count := 0
			_, stderr, exit := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { count++; return nil, nil }
			})
			if count != 0 {
				t.Fatalf("opened desktop session %d times for invalid limit", count)
			}
			if exit == 0 || !strings.Contains(stderr, "--max-") {
				t.Fatalf("exit=%d stderr=%q", exit, stderr)
			}
		})
	}
}
