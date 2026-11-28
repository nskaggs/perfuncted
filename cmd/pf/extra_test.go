package main

import (
	"bytes"
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/v2"
	diagnostic "github.com/nskaggs/perfuncted/v2/internal/diagnostic"
	"github.com/nskaggs/perfuncted/v2/internal/workflow"
	"github.com/nskaggs/perfuncted/v2/output"
	"github.com/nskaggs/perfuncted/v2/pftest"
	"github.com/nskaggs/perfuncted/v2/window"
	"image"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiagnosticEnvironmentFiltersAndRedacts(t *testing.T) {
	got := diagnostic.Environment([]string{
		"DISPLAY=:0",
		"WAYLAND_DISPLAY=wayland-1",
		"XDG_CURRENT_DESKTOP=Sway",
		"XDG_RUNTIME_DIR=/run/user/1000",
		"DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus;nonce=synthetic",
		"PF_AUDIT_SECRET=synthetic-secret",
	})

	if got["DISPLAY"] != ":0" || got["WAYLAND_DISPLAY"] != "wayland-1" {
		t.Fatalf("diagnostic environment lost safe display metadata: %v", got)
	}
	if got["XDG_CURRENT_DESKTOP"] != "Sway" {
		t.Fatalf("diagnostic environment lost desktop metadata: %v", got)
	}
	if got["XDG_RUNTIME_DIR"] != "<set>" || got["DBUS_SESSION_BUS_ADDRESS"] != "<set>" {
		t.Fatalf("diagnostic environment did not redact routing paths: %v", got)
	}
	if _, ok := got["PF_AUDIT_SECRET"]; ok {
		t.Fatalf("diagnostic environment exposed inherited secret: %v", got)
	}
}

func TestBuildInfoReportUsesSessionRuntime(t *testing.T) {
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DISPLAY", "")
	t.Setenv("XDG_CURRENT_DESKTOP", "")
	t.Setenv("SWAYSOCK", "")

	session, err := perfuncted.Open(perfuncted.WithStartupContext(context.Background()), perfuncted.Optional(), perfuncted.WithTarget(perfuncted.EnvironmentTarget([]string{
		"WAYLAND_DISPLAY=/tmp/perfuncted-session-wayland",
		"XDG_CURRENT_DESKTOP=Sway",
	})))
	if err != nil {
		t.Fatalf("perfuncted.Open: %v", err)
	}
	defer session.Close()

	got := buildInfoReport(session)
	if got, want := got.Compositor, "wlroots Wayland"; got != want {
		t.Fatalf("Compositor = %q, want %q", got, want)
	}
}

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
	if err := s.run(strings.NewReader("input type '" + value + "'\n")); err != nil {
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
	cmd.SetIn(strings.NewReader("input sendkeys --stdin\nconfidential payload\nsecond line\n"))
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
		{"input type", "input sendkeys private", func(_ *testing.T, _ *scriptRunner, i *pftest.Inputter, _ *pftest.Manager) { i.Err = failure }},
		{"input sendkeys literal", "input type private", func(_ *testing.T, _ *scriptRunner, i *pftest.Inputter, _ *pftest.Manager) { i.Err = failure }},
		{"pointer location", "input location", func(_ *testing.T, _ *scriptRunner, i *pftest.Inputter, _ *pftest.Manager) { i.Err = failure }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner, inputter, manager, _ := newScriptFixture(t)
			tc.prepare(t, runner, inputter, manager)
			err := runner.run(strings.NewReader(tc.script + "\ninput sendkeys should-not-run\n"))
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
			err := runner.run(strings.NewReader(tc.script + "\ninput sendkeys should-not-run\n"))
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
			cmd.SetIn(strings.NewReader("input sendkeys unauthorized\n"))
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

func newScriptFixture(t *testing.T) (*scriptRunner, *pftest.Inputter, *pftest.Manager, *bytes.Buffer) {
	t.Helper()
	sc := &pftest.Screenshotter{Width: 6, Height: 6}
	inp := &pftest.Inputter{}
	mgr := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "win", Title: "Editor", X: 30, Y: 40, W: 200, H: 100}}}, Titles: []string{"Editor"}}
	out := &outputTestLister{outs: []output.Info{{Name: "eDP-1", Backend: "test", Available: true, Geometry: output.Geometry{X: -200, Y: 0, W: 200, H: 100}, ResolutionW: 400, ResolutionH: 200, ScaleNumerator: 2, ScaleDenominator: 1}}}
	session := pftest.NewWithOutputs(sc, inp, mgr, out, nil)
	t.Cleanup(func() { _ = session.Close() })
	buffer := &bytes.Buffer{}
	return &scriptRunner{ctx: context.Background(), in: strings.NewReader("stdin payload"), out: buffer, pf: session}, inp, mgr, buffer
}

func TestScriptRunnerExercisesWindowInputScreenAndOutput(t *testing.T) {
	s, inp, mgr, out := newScriptFixture(t)
	destination := filepath.Join(t.TempDir(), "screenshot.png")
	script := strings.Join([]string{
		"# comments and blank lines must be ignored",
		"",
		"input sendkeys 'hello world'",
		"input type 'literal {enter}'",
		"input location",
		"window list",
		"window active",
		"window find title=Editor",
		"window select title=Editor",
		"window activate",
		"window move --title Editor --x 12 --y 34",
		"window resize --title Editor --w 120 --h 80",
		"window minimize Editor",
		"window maximize Editor",
		"window fullscreen Editor",
		"window unfullscreen Editor",
		"window restore Editor",
		"screen hash --rect 0,0,3,3",
		"screen grab --rect 0,0,3,3 --out '" + destination + "'",
		"output list",
	}, "\n")
	if err := s.run(strings.NewReader(script)); err != nil {
		t.Fatalf("script returned %v; output=%q", err, out.String())
	}
	if len(inp.Calls) != 2 || inp.Calls[0] != "type:hello world" || inp.Calls[1] != "type-literal:literal {enter}" {
		t.Fatalf("typed input not preserved: %v", inp.Calls)
	}
	if len(mgr.Activated) != 1 || mgr.Activated[0] != "win" {
		t.Fatalf("window action did not activate selected ID: %v", mgr.Activated)
	}
	if _, err := os.Stat(destination); err != nil {
		t.Fatalf("capture file absent: %v", err)
	}
	text := out.String()
	for _, want := range []string{"0,0", "Editor", "eDP-1", "2/1"} {
		if !strings.Contains(text, want) {
			t.Fatalf("script output missing %q in %q", want, text)
		}
	}
}

func TestScriptRunnerErrorsStopBeforeSideEffects(t *testing.T) {
	cases := []struct {
		name    string
		script  string
		message string
	}{
		{"unclosed quote", "input sendkeys 'oops", "unterminated quote"},
		{"escape at end", "input sendkeys foo\\", "trailing escape"},
		{"unknown group", "evil command", "unknown command"},
		{"missing input", "input", "missing input"},
		{"unknown input", "input execute", "unsupported input"},
		{"missing window", "window", "missing window"},
		{"unknown window", "window execute", "unsupported window"},
		{"missing target", "window maximize", "requires a title"},
		{"bad move", "window move --title Editor --x nan", "invalid syntax"},
		{"bad resize", "window resize --title Editor --w nan", "invalid syntax"},
		{"bad geometry", "screen hash --rect not-a-rectangle", "must be"},
		{"missing rect", "screen hash --rect", "requires a value"},
		{"missing output", "output hash", "unsupported output"},
		{"invalid selection", "window select 'title=['", "not found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, inp, _, _ := newScriptFixture(t)
			err := s.run(strings.NewReader(tc.script + "\n"))
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.message)) {
				t.Fatalf("script %q returned %v, want %q", tc.script, err, tc.message)
			}
			if len(inp.Calls) > 0 {
				t.Fatalf("invalid script emitted input: %v", inp.Calls)
			}
		})
	}
}

func TestScriptRunnerStoppedOnCanceledContext(t *testing.T) {
	s, inp, _, _ := newScriptFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.ctx = ctx
	if err := s.run(strings.NewReader("input sendkeys forbidden")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled script: %v", err)
	}
	if len(inp.Calls) > 0 {
		t.Fatal("canceled script emitted input")
	}
}

func TestScriptRunnerValidatesMoveResizeOptions(t *testing.T) {
	s, _, _, _ := newScriptFixture(t)
	for _, args := range [][]string{{"--title"}, {"--x"}, {"--y"}, {"--x", "x"}, {"--y", "x"}} {
		if _, _, _, err := s.parseWindowMoveArgs(args); err == nil {
			t.Fatalf("accepted invalid move args %v", args)
		}
	}
	for _, args := range [][]string{{"--title"}, {"--w"}, {"--h"}, {"--w", "x"}, {"--h", "x"}} {
		if _, _, _, err := s.parseWindowResizeArgs(args); err == nil {
			t.Fatalf("accepted invalid resize args %v", args)
		}
	}
	title, x, y, err := s.parseWindowMoveArgs([]string{"--title", "Editor", "--x", "-30", "--y", "22"})
	if err != nil || title != "Editor" || x != -30 || y != 22 {
		t.Fatalf("move %q,%d,%d: %v", title, x, y, err)
	}
	title, w, h, err := s.parseWindowResizeArgs([]string{"Editor", "--w", "200", "--h", "100"})
	if err != nil || title != "Editor" || w != 200 || h != 100 {
		t.Fatalf("resize %q,%d,%d: %v", title, w, h, err)
	}
}

func TestScriptRunnerScreenCaptureFallbackAndInputStdin(t *testing.T) {
	s, inp, _, _ := newScriptFixture(t)
	if err := s.execInput(1, []string{"sendkeys", "--stdin"}); err != nil {
		t.Fatal(err)
	}
	if len(inp.Calls) != 1 || inp.Calls[0] != "type:stdin payload" {
		t.Fatalf("stdin mismatch: %v", inp.Calls)
	}
	if err := s.execScreen(2, []string{"grab", "--rect", "0,0,2,2", "--out"}); err == nil {
		t.Fatal("screen grab without out path was accepted")
	}
	if err := s.execInput(3, []string{"type", "a", "b"}); err == nil {
		t.Fatal("literal input accepted multiple arguments")
	}
	if err := s.execInput(4, []string{"type", "z"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.pf.Screen().WithContext(context.Background()).Resolution(); err != nil {
		t.Fatal(err)
	}
	_, err := s.pf.Screen().WithContext(context.Background()).Grab(image.Rect(0, 0, 1, 1))
	if err != nil {
		t.Fatal(err)
	}
}

type scriptBrokenReader struct{}

func (scriptBrokenReader) Read([]byte) (int, error) { return 0, errors.New("synthetic stdin failure") }

type scriptBrokenWriter struct{}

func (scriptBrokenWriter) Write([]byte) (int, error) {
	return 0, errors.New("synthetic output failure")
}

func TestScriptRunnerFailClosedCommandAndOutputBoundaries(t *testing.T) {
	s, inp, _, output := newScriptFixture(t)
	for _, tc := range []struct{ script, want string }{
		{"window select", "requires a match spec"},
		{"window move --title", "requires a value"},
		{"window move --x", "requires a value"},
		{"window move --y", "requires a value"},
		{"window resize --title", "requires a value"},
		{"window resize --w", "requires a value"},
		{"window resize --h", "requires a value"},
		{"screen grab --rect", "requires a value"},
		{"screen grab --out", "requires a value"},
		{"screen hash --rect", "requires a value"},
		{"screen unknown", "unsupported screen"},
		{"input type", "requires exactly one"},
		{"output unknown", "unsupported output"},
		{"window move --title Missing --x 1 --y 2", "not found"},
		{"window resize --title Missing --w 1 --h 2", "not found"},
	} {
		if err := s.run(strings.NewReader(tc.script + "\n")); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("invalid command %q returned %v (want %q)", tc.script, err, tc.want)
		}
	}
	if len(inp.Calls) != 0 {
		t.Fatalf("invalid script produced input events: %v", inp.Calls)
	}
	if err := s.runWindowAction("invalid", nil); err == nil || !strings.Contains(err.Error(), "unknown window action") {
		t.Fatalf("unknown action accepted: %v", err)
	}
	if err := s.execWindow(1, []string{"activate"}); err == nil || !strings.Contains(err.Error(), "requires a title") {
		t.Fatalf("unselected window action accepted: %v", err)
	}
	if err := s.run(strings.NewReader("input type ''\n")); err != nil {
		t.Fatalf("empty literal should be passed through: %v", err)
	}
	if len(inp.Calls) != 1 || inp.Calls[0] != "type-literal:" {
		t.Fatalf("empty literal mutated: %v", inp.Calls)
	}
	output.Reset()
	if err := s.execInfo(2, nil); err != nil || !strings.Contains(output.String(), "capabilities") {
		t.Fatalf("script info report invalid: %v %q", err, output.String())
	}
}

func TestScriptRunnerScannerFailureAndAdversarialStreams(t *testing.T) {
	s, inp, _, _ := newScriptFixture(t)
	if err := s.run(strings.NewReader(strings.Repeat("a", 128*1024))); err == nil || !strings.Contains(err.Error(), "token too long") {
		t.Fatalf("oversized script line accepted: %v", err)
	}
	if len(inp.Calls) != 0 {
		t.Fatal("scanner failure produced input events")
	}
	s.in = scriptBrokenReader{}
	if err := s.execInput(1, []string{"sendkeys", "--stdin"}); err == nil || !strings.Contains(err.Error(), "synthetic stdin failure") {
		t.Fatalf("stdin I/O error lost: %v", err)
	}
	s.out = scriptBrokenWriter{}
	if err := s.execInput(2, []string{"location"}); err == nil || !strings.Contains(err.Error(), "synthetic output failure") {
		t.Fatalf("script location writer failure lost: %v", err)
	}
	if err := s.execOutput(3, []string{"list"}); err == nil || !strings.Contains(err.Error(), "synthetic output failure") {
		t.Fatalf("output-list writer failure lost: %v", err)
	}
	if err := s.execWindow(4, []string{"active"}); err == nil || !strings.Contains(err.Error(), "synthetic output failure") {
		t.Fatalf("active-title writer failure lost: %v", err)
	}
	s.ctx = context.Background()
	s.out = io.Discard
	if err := s.execInput(5, []string{"type", "from script"}); err != nil {
		t.Fatal(err)
	}
	if len(inp.Calls) != 1 || inp.Calls[0] != "type-literal:from script" {
		t.Fatalf("script recovery failed: %v", inp.Calls)
	}
}

type scriptFailingSourceReader struct{ failure error }

func (r scriptFailingSourceReader) Read([]byte) (int, error) { return 0, r.failure }

// The script parser must propagate a real reader failure instead of treating
// it like a clean EOF or silently accepting an incomplete instruction.
func TestScriptRunPropagatesSourceReadFailure(t *testing.T) {
	failure := errors.New("synthetic script transport failed")
	runner := &scriptRunner{ctx: context.Background(), out: io.Discard}
	if err := runner.run(scriptFailingSourceReader{failure: failure}); !errors.Is(err, failure) {
		t.Fatalf("source failure lost: %v", err)
	}
}

// Multiword typing is a single literal input operation, and malformed grab
// coordinates are rejected before any capture destination can be created.
func TestScriptRunnerMultiwordInputAndInvalidGrabPath(t *testing.T) {
	runner, backend, _, _ := newScriptFixture(t)
	if err := runner.run(strings.NewReader("input sendkeys \"alpha beta gamma\"\n")); err != nil {
		t.Fatal(err)
	}
	if len(backend.Calls) != 1 || backend.Calls[0] != "type:alpha beta gamma" {
		t.Fatalf("input tokens were not joined: %v", backend.Calls)
	}
	if err := runner.execScreen(2, []string{"grab", "--rect", "invalid", "--out", t.TempDir() + "/must-not-exist.png"}); err == nil {
		t.Fatal("invalid grab bounds accepted")
	}
}

// Once a window has been selected, a stale target must not report a successful
// close when the compositor refuses the command.
func TestScriptSelectedWindowClosePropagatesRejection(t *testing.T) {
	runner, _, manager, _ := newScriptFixture(t)
	selected, err := findWindowByTitle(context.Background(), runner.pf.Windows(), "Editor")
	if err != nil {
		t.Fatal(err)
	}
	runner.selectedWindow = selected
	failure := errors.New("synthetic compositor rejected close")
	manager.Err = failure
	if err := runner.runWindowAction("close", selected); !errors.Is(err, failure) {
		t.Fatalf("rejected window close returned %v", err)
	}
}

type workflowTestInput struct {
	pftest.Inputter
	entered chan string
	proceed chan struct{}
	cleaned chan struct{}
}

func (i *workflowTestInput) Type(ctx context.Context, text string) error {
	i.entered <- text
	if text != "first" {
		return nil
	}
	defer close(i.cleaned)
	select {
	case <-i.proceed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func workflowReceive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("workflow test timed out")
		var zero T
		return zero
	}
}

func workflowAwaitState(t *testing.T, w *workflow.Lifecycle, want workflow.State) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		_, state, changed := w.Snapshot()
		if state == want {
			return
		}
		select {
		case <-changed:
		case <-deadline:
			t.Fatalf("state %s, want %s", state, want)
		}
	}
}

func TestWorkflowRunnerLifecycle(t *testing.T) {
	for _, mode := range []string{"resume", "stop-paused", "stop-in-flight", "parent-cancel", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			w := workflow.New(parent, "")
			defer w.Stop()
			inp := &workflowTestInput{entered: make(chan string, 3), proceed: make(chan struct{}), cleaned: make(chan struct{})}
			session := pftest.New(&pftest.Screenshotter{}, inp, &pftest.Manager{}, nil)
			defer session.Close()
			runner := &scriptRunner{ctx: parent, lifecycle: w, out: io.Discard, pf: session}
			done := make(chan error, 1)
			go func() { done <- runner.run(strings.NewReader("input sendkeys first\ninput sendkeys second\n")) }()
			if got := workflowReceive(t, inp.entered); got != "first" {
				t.Fatal(got)
			}
			id, state, _ := w.Snapshot()
			if id == "" || state != workflow.Running {
				t.Fatalf("initial identity/state: %q %s", id, state)
			}
			switch mode {
			case "resume", "stop-paused":
				if !w.Pause() {
					t.Fatal("pause refused")
				}
				workflowAwaitState(t, w, workflow.Pausing)
				close(inp.proceed)
				workflowAwaitState(t, w, workflow.Paused)
				select {
				case got := <-inp.entered:
					t.Fatalf("dispatch while paused: %s", got)
				default:
				}
				if mode == "resume" {
					if !w.Resume() {
						t.Fatal("resume refused")
					}
				} else if !w.Stop() {
					t.Fatal("stop refused")
				}
			case "stop-in-flight":
				if !w.Stop() {
					t.Fatal("stop refused")
				}
			case "parent-cancel":
				cancel()
			case "legacy":
				close(inp.proceed)
			}
			err := workflowReceive(t, done)
			workflowReceive(t, inp.cleaned)
			want := workflow.Stopped
			if mode == "legacy" || mode == "resume" {
				want = workflow.Completed
				if err != nil {
					t.Fatal(err)
				}
				if got := workflowReceive(t, inp.entered); got != "second" {
					t.Fatal(got)
				}
			} else if !errors.Is(err, context.Canceled) {
				t.Fatalf("want cancellation, got %v", err)
			}
			gotID, gotState, _ := w.Snapshot()
			if gotID != id || gotState != want {
				t.Fatalf("final identity/state: %q %s", gotID, gotState)
			}
			if w.Resume() || w.Pause() || w.Stop() {
				t.Fatal("terminal state accepted control")
			}
			select {
			case got := <-inp.entered:
				t.Fatalf("unexpected dispatch: %s", got)
			default:
			}
		})
	}
}

func TestWorkflowFailureAndIdentity(t *testing.T) {
	w := workflow.New(context.Background(), "")
	other := workflow.New(context.Background(), "")
	defer other.Stop()
	if w.ID() == other.ID() {
		t.Fatal("duplicate run identity")
	}
	runner := &scriptRunner{lifecycle: w}
	if err := runner.run(strings.NewReader("unknown\n")); err == nil {
		t.Fatal("expected script error")
	}
	workflowAwaitState(t, w, workflow.Failed)
}

func TestWorkflowCommandLocalCancellationFails(t *testing.T) {
	for _, err := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(err.Error(), func(t *testing.T) {
			w := workflow.New(context.Background(), "")
			if got := w.Finish(err); !errors.Is(got, err) {
				t.Fatalf("finish returned %v, want %v", got, err)
			}
			workflowAwaitState(t, w, workflow.Failed)
		})
	}
}
