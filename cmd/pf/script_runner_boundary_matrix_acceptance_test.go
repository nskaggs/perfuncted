package main

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

type scriptBrokenReader struct{}

func (scriptBrokenReader) Read([]byte) (int, error) { return 0, errors.New("synthetic stdin failure") }

type scriptBrokenWriter struct{}

func (scriptBrokenWriter) Write([]byte) (int, error) {
	return 0, errors.New("synthetic output failure")
}

func TestScriptRunnerQuotingPreservesEmptyAndEscapedArguments(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  []string
		bad   bool
	}{
		{"input type 'two words'", []string{"input", "type", "two words"}, false},
		{`input type ""`, []string{"input", "type", ""}, false},
		{`input type hello\ world`, []string{"input", "type", "hello world"}, false},
		{`input type "one 'inner' word"`, []string{"input", "type", "one 'inner' word"}, false},
		{`input type 'one "inner" word'`, []string{"input", "type", "one \"inner\" word"}, false},
		{`input type "unterminated`, nil, true},
		{`input type trailing\`, nil, true},
	} {
		got, err := splitShell(tc.input)
		if (err != nil) != tc.bad || (!tc.bad && !reflect.DeepEqual(got, tc.want)) {
			t.Errorf("splitShell(%q)=%#v,%v; want %#v error=%v", tc.input, got, err, tc.want, tc.bad)
		}
	}
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
		{"input type-literal", "requires exactly one"},
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
	if err := s.run(strings.NewReader("input type-literal ''\n")); err != nil {
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
	if err := s.execInput(1, []string{"type", "--stdin"}); err == nil || !strings.Contains(err.Error(), "synthetic stdin failure") {
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
	if len(inp.Calls) != 1 || inp.Calls[0] != "type:from script" {
		t.Fatalf("script recovery failed: %v", inp.Calls)
	}
}
