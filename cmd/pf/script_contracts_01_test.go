package main

import (
	"context"
	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
	"os"
	"path/filepath"
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

func TestScriptLiteralInputPreservesQuotedBackslashesEndToEnd(t *testing.T) {
	s, input, _, _ := newScriptFixture(t)
	value := `C:\Users\Public\notes.txt`
	if err := s.run(strings.NewReader("input type-literal '" + value + "'\n")); err != nil {
		t.Fatal(err)
	}
	if len(input.Calls) != 1 || input.Calls[0] != "type-literal:"+value {
		t.Fatalf("script changed literal keyboard content: %#v", input.Calls)
	}
}

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

// A mistyped script option must fail before a window mutation, image capture,
// or hash of an unintended region. Silently ignoring options makes defaults
// unexpectedly authoritative and can publish a screenshot to another path.
func TestScriptCommandsRejectUnknownOptionsBeforeSideEffects(t *testing.T) {
	for _, tc := range []struct{ name, script string }{
		{"window move", "window move --title Editor --x 20 --y 30 --z 90"},
		{"window resize", "window resize --title Editor --w 300 --h 200 --depth 90"},
		{"screen hash", "screen hash --rect 0,0,2,2 --region 1,1,1,1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _, _ := newScriptFixture(t)
			if err := s.run(strings.NewReader(tc.script + "\n")); err == nil || !strings.Contains(err.Error(), "unknown") {
				t.Fatalf("unknown option was ignored: %q err=%v", tc.script, err)
			}
		})
	}
	t.Run("screen grab", func(t *testing.T) {
		s, _, _, _ := newScriptFixture(t)
		dest := filepath.Join(t.TempDir(), "capture.png")
		err := s.run(strings.NewReader("screen grab --rect 0,0,2,2 --out '" + dest + "' --output typo\n"))
		if err == nil || !strings.Contains(err.Error(), "unknown") {
			t.Fatalf("unknown screen capture option accepted: %v", err)
		}
		if _, err := os.Stat(dest); !os.IsNotExist(err) {
			t.Fatalf("unexpected screenshot created: %v", err)
		}
	})
}

func TestScriptWindowTargetSelectionRequiresExplicitOrPriorSelection(t *testing.T) {
	selected := &perfuncted.Window{}
	runner := &scriptRunner{selectedWindow: selected}

	for _, tc := range []struct {
		name    string
		resolve func() (*perfuncted.Window, error)
	}{
		{name: "no arguments use prior selection", resolve: func() (*perfuncted.Window, error) { return runner.windowTarget(nil) }},
		{name: "empty title uses prior selection", resolve: func() (*perfuncted.Window, error) { return runner.targetForTitle("") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.resolve()
			if err != nil || got != selected {
				t.Fatalf("selected target = %p, %v; want %p", got, err, selected)
			}
		})
	}

	runner.selectedWindow = nil
	for _, tc := range []struct {
		name    string
		resolve func() (*perfuncted.Window, error)
	}{
		{name: "no arguments", resolve: func() (*perfuncted.Window, error) { return runner.windowTarget(nil) }},
		{name: "empty title", resolve: func() (*perfuncted.Window, error) { return runner.targetForTitle("") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.resolve()
			if got != nil || err == nil || !strings.Contains(err.Error(), "requires a title or a prior window select") {
				t.Fatalf("missing selection = %p, %v; want explicit error", got, err)
			}
		})
	}
}
