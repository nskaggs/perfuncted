package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted"
)

// A malformed local flag must never trigger desktop authorization, including
// a portal consent prompt or a compositor-backed capture session.
func TestScreenInvalidLocalArgumentsNeverOpenDesktopSession(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"grab rectangle", []string{"screen", "grab", "--rect", "malformed"}},
		{"hash rectangle", []string{"screen", "hash", "--rect", "malformed"}},
		{"grab region rectangle", []string{"screen", "grab-region", "--rect", "malformed"}},
		{"watch rectangle", []string{"screen", "watch", "--rect", "malformed"}},
		{"watch poll", []string{"screen", "watch", "--poll", "not-a-duration"}},
		{"watch duration", []string{"screen", "watch", "--duration", "not-a-duration"}},
		{"wait predicate rectangle", []string{"screen", "wait-for-fn", "--rect", "malformed"}},
		{"wait predicate name", []string{"screen", "wait-for-fn", "--predicate", "unknown-predicate"}},
		{"wait predicate poll", []string{"screen", "wait-for-fn", "--poll", "not-a-duration"}},
		{"wait predicate timeout", []string{"screen", "wait-for-fn", "--timeout", "not-a-duration"}},
		{"wait settle rectangle", []string{"screen", "wait-for-settle", "--rect", "malformed"}},
		{"wait settle samples", []string{"screen", "wait-for-settle", "--stable", "invalid"}},
		{"wait settle poll", []string{"screen", "wait-for-settle", "--poll", "not-a-duration"}},
		{"wait settle timeout", []string{"screen", "wait-for-settle", "--timeout", "not-a-duration"}},
		{"multiple pixels empty", []string{"screen", "get-multiple-pixels", "--points", ""}},
		{"multiple pixels format", []string{"screen", "get-multiple-pixels", "--points", "1,2", "--output", "unknown"}},
		{"multiple pixels coordinates", []string{"screen", "get-multiple-pixels", "--points", "one,two"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opens := 0
			cmd := newRootCmd(func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opens++
					return nil, errors.New("unexpected screen authorization")
				}
			})
			var out, errOut bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			cmd.SetArgs(tc.args)
			err := cmd.ExecuteContext(context.Background())
			if err == nil {
				t.Fatal("invalid input was accepted")
			}
			if opens != 0 {
				t.Fatalf("invalid screen flags opened %d desktop sessions: %v", opens, err)
			}
			if out.Len() != 0 {
				t.Fatalf("invalid invocation published data: %q", out.String())
			}
		})
	}
}
