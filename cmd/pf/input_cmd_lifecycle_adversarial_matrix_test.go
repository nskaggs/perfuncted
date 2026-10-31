package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
)

// Exercise actual CLI command resolution, session opening, method dispatch,
// and error publication across operations not covered by the basic matrix.
func TestSyntheticInputExtendedCommandsAndClipboardIndependentCalls(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		want   []string
		output string
	}{
		{"mousedown no coordinates", []string{"input", "mousedown", "--button", "2"}, []string{"mousedown"}, "mousedown button 2"},
		{"mousedown with coordinates", []string{"input", "mousedown", "--x", "9", "--y", "27"}, []string{"move:9,27", "mousedown"}, "mousedown button 1"},
		{"mouseup no coordinates", []string{"input", "mouseup", "--button", "3"}, []string{"mouseup"}, "mouseup button 3"},
		{"mouseup with coordinates", []string{"input", "mouseup", "--x", "11", "--y", "15"}, []string{"move:11,15", "mouseup"}, "mouseup button 1"},
		{"location", []string{"input", "location"}, nil, "0,0"},
		{"coordinate space", []string{"input", "coordinate-space"}, nil, "kind=global-logical"},
		{"sync", []string{"input", "sync"}, nil, ""},
		{"repeat defaults one", []string{"input", "click", "--repeat", "0"}, []string{"click:0,0"}, "clicked button 1"},
		{"repeat three", []string{"input", "click", "--repeat", "3"}, []string{"click:0,0", "click:0,0", "click:0,0"}, "clicked button 1"},
		{"drag", []string{"input", "drag-and-drop", "--x1", "2", "--y1", "4", "--x2", "10", "--y2", "12"}, []string{"move:2,4", "mousedown", "move:10,12", "mouseup"}, "dragged 2,4 to 10,12"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := &pftest.Inputter{}
			out, errOut, exit := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, in, nil, nil), nil }
			})
			if exit != 0 || !strings.Contains(out, tc.output) || errOut != "" {
				t.Fatalf("command=%v code=%d stdout=%q stderr=%q", tc.args, exit, out, errOut)
			}
			if len(in.Calls) != len(tc.want) {
				t.Fatalf("unexpected backend call count %v want %v", in.Calls, tc.want)
			}
			for i := range tc.want {
				if in.Calls[i] != tc.want[i] {
					t.Fatalf("backend calls=%v want %v", in.Calls, tc.want)
				}
			}
		})
	}
}
func TestSyntheticInputCommandsSurfaceBackendFailuresBeforeReportingSuccess(t *testing.T) {
	failure := errors.New("synthetic keyboard/pointer backend disconnected")
	commands := [][]string{
		{"input", "move", "--x", "10", "--y", "20"},
		{"input", "click", "--x", "10", "--y", "20", "--repeat", "3"},
		{"input", "double-click", "--x", "10", "--y", "20"},
		{"input", "drag-and-drop", "--x1", "2", "--y1", "4", "--x2", "10", "--y2", "12"},
		{"input", "click-center", "--rect", "10,20,30,40"},
		{"input", "type", "text"}, {"input", "keydown", "ctrl"}, {"input", "keyup", "ctrl"},
		{"input", "mousedown", "--x", "10", "--y", "20"},
		{"input", "mouseup", "--x", "10", "--y", "20"},
		{"input", "location"}, {"input", "coordinate-space"}, {"input", "sync"},
		{"input", "scroll", "up"}, {"input", "scroll", "down"},
		{"input", "scroll", "left"}, {"input", "scroll", "right"},
	}
	for _, args := range commands {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			in := &pftest.Inputter{Err: failure}
			out, errOut, exit := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, in, nil, nil), nil }
			})
			if exit == 0 || out != "" || !strings.Contains(errOut, failure.Error()) {
				t.Fatalf("backend failure not propagated: command=%v code=%d stdout=%q stderr=%q", args, exit, out, errOut)
			}
		})
	}
}
func TestSyntheticInputInvalidSyntaxCannotDispatchOrOpenAnUnavailableSession(t *testing.T) {
	for _, args := range [][]string{
		{"input", "move", "unexpected"},
		{"input", "keydown"}, {"input", "keyup", "one", "two"},
		{"input", "mousedown", "unexpected"},
		{"input", "click-window"}, {"input", "click-screen", "--x", "-1", "--y", "5"},
		{"input", "select-file", "--title", "Dialog", "--path", "relative-path"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			opened := 0
			_, _, exit := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opened++
					return nil, errors.New("unexpected session open")
				}
			})
			if exit == 0 || opened != 0 {
				t.Fatalf("invalid syntax triggered session open for %v: exit=%d opens=%d", args, exit, opened)
			}
		})
	}
}
