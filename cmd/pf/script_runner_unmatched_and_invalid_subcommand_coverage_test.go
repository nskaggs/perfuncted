package main

import (
	"strings"
	"testing"
)

// Missing window matches and malformed script arguments must fail locally
// rather than acting on a previously selected or similarly named window.
func TestScriptRunnerUnmatchedAndMalformedCommandBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*scriptRunner) error
		want string
	}{
		{"find unmatched", func(s *scriptRunner) error { return s.execWindow(4, []string{"find", "title=DoesNotExist"}) }, "not found"},
		{"duplicate move title", func(s *scriptRunner) error {
			_, _, _, err := s.parseWindowMoveArgs([]string{"Editor", "Another"})
			return err
		}, "unknown window option"},
		{"missing screen subcommand", func(s *scriptRunner) error { return s.execScreen(9, nil) }, "missing screen subcommand"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner, input, manager, output := newScriptFixture(t)
			err := tc.run(runner)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("boundary returned %v, want %q", err, tc.want)
			}
			if len(input.Calls) > 0 || len(manager.Activated) > 0 || output.Len() > 0 {
				t.Fatalf("rejected script caused side effects: input=%v activation=%v output=%q", input.Calls, manager.Activated, output.String())
			}
		})
	}
}
