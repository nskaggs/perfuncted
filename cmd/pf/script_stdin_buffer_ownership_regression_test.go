package main

import (
	"context"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
)

// When the script itself is read from stdin, type --stdin must receive the
// remaining bytes rather than losing data prefetched by the script parser.
func TestRunScriptStdinPassesUnconsumedBytesToInputType(t *testing.T) {
	inputter := &pftest.Inputter{}
	cmd := runCmd(func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, inputter, nil, nil), nil }, nil)
	cmd.SetIn(strings.NewReader("input type --stdin\nconfidential payload\nsecond line\n"))
	cmd.SetArgs([]string{"-"})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	if err := cmd.Execute(); err != nil {
		t.Fatalf("stdin script failed: %v", err)
	}
	if got, want := inputter.Calls, []string{"type:confidential payload\nsecond line\n"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("scanner lost stdin payload: got=%q want=%q", got, want)
	}
}
