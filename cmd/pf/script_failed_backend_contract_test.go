package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/pftest"
)

type failingScriptOutputs struct{ err error }

func (o failingScriptOutputs) List(context.Context) ([]output.Info, error) { return nil, o.err }
func (o failingScriptOutputs) Close() error                                { return nil }

// A script must stop at a failed command instead of dispatching the next
// command. A backend returning an error is not authorization for other work.
func TestScriptRunnerBackendFailureStopsFollowingInput(t *testing.T) {
	failure := errors.New("synthetic backend unavailable")
	for _, tc := range []struct {
		name, script string
		prepare      func(*testing.T, *scriptRunner, *pftest.Inputter, *pftest.Manager)
	}{
		{"window listing", "window list", func(_ *testing.T, _ *scriptRunner, _ *pftest.Inputter, m *pftest.Manager) { m.Err = failure }},
		{"window active title", "window active", func(_ *testing.T, _ *scriptRunner, _ *pftest.Inputter, m *pftest.Manager) { m.Err = failure }},
		{"window find", "window find title=Editor", func(_ *testing.T, _ *scriptRunner, _ *pftest.Inputter, m *pftest.Manager) { m.Err = failure }},
		{"window activate", "window activate Editor", func(_ *testing.T, _ *scriptRunner, _ *pftest.Inputter, m *pftest.Manager) { m.Err = failure }},
		{"window move", "window move --x 10 --y 20", func(t *testing.T, s *scriptRunner, _ *pftest.Inputter, m *pftest.Manager) {
			if err := s.execWindow(1, []string{"select", "title=Editor"}); err != nil {
				t.Fatal(err)
			}
			m.Err = failure
		}},
		{"window resize", "window resize --w 10 --h 20", func(t *testing.T, s *scriptRunner, _ *pftest.Inputter, m *pftest.Manager) {
			if err := s.execWindow(1, []string{"select", "title=Editor"}); err != nil {
				t.Fatal(err)
			}
			m.Err = failure
		}},
		{"input type", "input type private", func(_ *testing.T, _ *scriptRunner, i *pftest.Inputter, _ *pftest.Manager) { i.Err = failure }},
		{"input type literal", "input type-literal private", func(_ *testing.T, _ *scriptRunner, i *pftest.Inputter, _ *pftest.Manager) { i.Err = failure }},
		{"pointer location", "input location", func(_ *testing.T, _ *scriptRunner, i *pftest.Inputter, _ *pftest.Manager) { i.Err = failure }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner, inputter, manager, _ := newScriptFixture(t)
			tc.prepare(t, runner, inputter, manager)
			err := runner.run(strings.NewReader(tc.script + "\ninput type should-not-run\n"))
			if !errors.Is(err, failure) {
				t.Fatalf("script continued after backend failure: %v", err)
			}
			for _, call := range inputter.Calls {
				if strings.Contains(call, "should-not-run") {
					t.Fatalf("script executed command after error: %v", inputter.Calls)
				}
			}
		})
	}
}

func TestScriptRunnerScreenAndOutputFailuresPreventFollowingInput(t *testing.T) {
	failure := errors.New("synthetic capture failed")
	for _, tc := range []struct {
		name, script  string
		screenFailure bool
	}{
		{"screen hash", "screen hash --rect 0,0,3,3", true},
		{"screen grab", "screen grab --rect 0,0,3,3 --out /not-a-real-place/perfuncted.png", true},
		{"output list", "output list", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inp := &pftest.Inputter{}
			sc := &pftest.Screenshotter{Width: 10, Height: 10}
			if tc.screenFailure {
				sc.Err = failure
			}
			out := failingScriptOutputs{err: failure}
			session := pftest.NewWithOutputs(sc, inp, &pftest.Manager{}, out, nil)
			defer session.Close()
			runner := &scriptRunner{ctx: context.Background(), out: &strings.Builder{}, pf: session}
			err := runner.run(strings.NewReader(tc.script + "\ninput type should-not-run\n"))
			if !errors.Is(err, failure) {
				t.Fatalf("failed backend allowed script to continue: %v", err)
			}
			if len(inp.Calls) != 0 {
				t.Fatalf("later input executed after backend failure: %v", inp.Calls)
			}
		})
	}
}

// Script commands must reject inaccessible or nonexistent files before opening
// a desktop session. Once the input is valid, an unavailable session must fail
// rather than interpreting script contents without authorization.
func TestRunCLIRejectsBadScriptFilesBeforeSessionOpening(t *testing.T) {
	failure := errors.New("synthetic session open denied")
	root := t.TempDir()
	blocker := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("outside script"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path string
		open       bool
	}{
		{"file as parent", filepath.Join(blocker, "script.txt"), false},
		{"missing script", filepath.Join(root, "missing.txt"), false},
		{"stdin session unavailable", "-", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opens := 0
			cmd := runCmd(func(context.Context) (*perfuncted.Session, error) { opens++; return nil, failure }, nil)
			cmd.SetArgs([]string{tc.path})
			cmd.SetIn(strings.NewReader("input type unauthorized\n"))
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			err := cmd.Execute()
			if err == nil {
				t.Fatal("unauthorized script accepted")
			}
			if (opens != 0) != tc.open {
				t.Fatalf("session opens=%d, expected open=%v, err=%v", opens, tc.open, err)
			}
			if tc.open && !errors.Is(err, failure) {
				t.Fatalf("session opening failure was lost: %v", err)
			}
		})
	}
}

func TestOutputCLIRefusesBackendAndWriterFailures(t *testing.T) {
	original := outputFormatFlag
	t.Cleanup(func() { outputFormatFlag = original })
	failure := errors.New("synthetic output listing failed")
	for _, tc := range []struct {
		name                              string
		openError, listError, writerError bool
	}{
		{"open", true, false, false},
		{"backend", false, true, false},
		{"json destination", false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opens := 0
			cmd := outputCmd(func(context.Context) (*perfuncted.Session, error) {
				opens++
				if tc.openError {
					return nil, failure
				}
				if tc.listError {
					return pftest.NewWithOutputs(nil, nil, nil, failingScriptOutputs{err: failure}, nil), nil
				}
				return pftest.NewWithOutputs(nil, nil, nil, &outputTestLister{outs: []output.Info{{Name: "private-display"}}}, nil), nil
			})
			args := []string{"list"}
			if tc.writerError {
				args = append(args, "--output", "json")
				cmd.SetOut(scriptBrokenWriter{})
			}
			cmd.SetArgs(args)
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			err := cmd.Execute()
			if err == nil || opens != 1 {
				t.Fatalf("failed output listing accepted: opens=%d err=%v", opens, err)
			}
			if !tc.writerError && !errors.Is(err, failure) {
				t.Fatalf("backend failure missing: %v", err)
			}
		})
	}
}

func TestScriptRunnerRejectsInvalidMatchWithoutDesktopEffect(t *testing.T) {
	runner, inp, _, _ := newScriptFixture(t)
	for _, script := range []string{"window select --no-such-match=value", "window find --no-such-match=value"} {
		if err := runner.run(strings.NewReader(script + "\n")); err == nil {
			t.Fatalf("invalid window selector accepted: %q", script)
		}
	}
	if len(inp.Calls) != 0 {
		t.Fatalf("invalid selector injected input: %v", inp.Calls)
	}
	if err := runner.exec(1, nil); err != nil {
		t.Fatalf("empty script command should be a no-op: %v", err)
	}
	if got := outputScaleString(output.Info{Scale: 2}); got != "2" {
		t.Fatalf("integer output scale=%q", got)
	}
}
