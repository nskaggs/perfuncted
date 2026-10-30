package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
)

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
		"input type 'hello world'",
		"input type-literal 'literal {enter}'",
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
		{"unclosed quote", "input type 'oops", "unterminated quote"},
		{"escape at end", "input type foo\\", "trailing escape"},
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
	if err := s.run(strings.NewReader("input type forbidden")); !errors.Is(err, context.Canceled) {
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
	if err := s.execInput(1, []string{"type", "--stdin"}); err != nil {
		t.Fatal(err)
	}
	if len(inp.Calls) != 1 || inp.Calls[0] != "type:stdin payload" {
		t.Fatalf("stdin mismatch: %v", inp.Calls)
	}
	if err := s.execScreen(2, []string{"grab", "--rect", "0,0,2,2", "--out"}); err == nil {
		t.Fatal("screen grab without out path was accepted")
	}
	if err := s.execInput(3, []string{"type-literal", "a", "b"}); err == nil {
		t.Fatal("literal input accepted multiple arguments")
	}
	if err := s.execInput(4, []string{"type-literal", "z"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.pf.Screen.Resolution(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err := s.pf.Screen.Grab(context.Background(), image.Rect(0, 0, 1, 1))
	if err != nil {
		t.Fatal(err)
	}
}
