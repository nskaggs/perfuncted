package main

import (
	"bytes"
	"context"
	"errors"
	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/pftest"
	"math"
	"strings"
	"testing"
)

func TestRemoteDesktopCLIRequiresExplicitOptIn(t *testing.T) {
	root := newRootCmd(func(cfg *cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) { return nil, nil }
	})
	if root.PersistentFlags().Lookup("input-backend") == nil {
		t.Fatal("missing input-backend option to select compositor-authorized portal")
	}
	if root.PersistentFlags().Lookup("remember-input-permission") == nil {
		t.Fatal("missing explicit persistence setting")
	}
}

// Argument validation must precede any desktop session acquisition. Opening
// portal-backed input just to reject bad syntax can prompt for permission and
// initialize remote input channels even though no operation is possible.
func TestInputInvalidRuntimeArgumentsDoNotAcquireDesktopSession(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"invalid click delay", []string{"input", "click", "--delay", "nonsense"}},
		{"missing typed text", []string{"input", "type"}},
		{"invalid center rectangle", []string{"input", "click-center", "--rect", "wrong"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opens := 0
			out, errOut, exit := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opens++
					return nil, errors.New("unnecessary desktop session acquisition")
				}
			})
			if exit == 0 || opens != 0 || strings.Contains(out, "clicked") {
				t.Fatalf("invalid input %v opened %d sessions, exit=%d out=%q err=%q", tc.args, opens, exit, out, errOut)
			}
		})
	}
}

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

// Refusing authorization must stop every input command before it can queue
// keystrokes, pointer events, or a success report. Each case is executed by
// Cobra, rather than calling a command callback in isolation.
func TestInputCommandsPropagateSessionAuthorizationFailure(t *testing.T) {
	denied := errors.New("synthetic private input authorization denied")
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"move", []string{"input", "move", "--x", "12", "--y", "8"}},
		{"click", []string{"input", "click", "--x", "12", "--y", "8", "--repeat", "3"}},
		{"double click", []string{"input", "double-click", "--x", "12", "--y", "8"}},
		{"drag", []string{"input", "drag-and-drop", "--x1", "1", "--y1", "2", "--x2", "30", "--y2", "40"}},
		{"center", []string{"input", "click-center", "--rect", "0,0,10,10"}},
		{"type", []string{"input", "type", "private payload"}},
		{"keydown", []string{"input", "keydown", "shift"}},
		{"keyup", []string{"input", "keyup", "shift"}},
		{"mousedown", []string{"input", "mousedown", "--x", "12", "--y", "8"}},
		{"mouseup", []string{"input", "mouseup", "--x", "12", "--y", "8"}},
		{"location", []string{"input", "location"}},
		{"coordinate space", []string{"input", "coordinate-space"}},
		{"scroll up", []string{"input", "scroll", "up", "--clicks", "2"}},
		{"scroll down", []string{"input", "scroll", "down", "--clicks", "2"}},
		{"scroll left", []string{"input", "scroll", "left", "--clicks", "2"}},
		{"scroll right", []string{"input", "scroll", "right", "--clicks", "2"}},
		{"sync", []string{"input", "sync"}},
		{"window click", []string{"input", "click-window", "--id", "synthetic-window", "--x", "1", "--y", "2"}},
		{"screen click", []string{"input", "click-screen", "--x", "1", "--y", "2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opens := 0
			root := newRootCmd(func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { opens++; return nil, denied }
			})
			var out, stderr bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&stderr)
			root.SetArgs(tc.args)
			err := root.ExecuteContext(context.Background())
			if !errors.Is(err, denied) {
				t.Fatalf("authorization failure lost: command=%v err=%v", tc.args, err)
			}
			if opens != 1 {
				t.Fatalf("expected exactly one authorization attempt, got %d", opens)
			}
			if strings.Contains(out.String(), "sent") || strings.Contains(out.String(), "clicked") || strings.Contains(out.String(), "moved") {
				t.Fatalf("unauthorized action falsely reported as successful: %q", out.String())
			}
		})
	}
}

// A half-specified pointer location must not silently send an input action
// at the previous cursor position: the caller believes a different target
// was selected. Reject these commands before a consent-prompting session opens.
func TestInputCLIRejectsPartialMouseCoordinatesAndNegativeClickRepeatsBeforeAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"mouse down only x", []string{"input", "mousedown", "--x", "15"}},
		{"mouse down only y", []string{"input", "mousedown", "--y", "25"}},
		{"mouse up only x", []string{"input", "mouseup", "--x", "15"}},
		{"mouse up only y", []string{"input", "mouseup", "--y", "25"}},
		{"negative click repeat", []string{"input", "click", "--repeat", "-2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opened := 0
			stdout, stderr, exit := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opened++
					return nil, errors.New("must not open desktop session")
				}
			})
			if exit == 0 || opened != 0 || stdout != "" || stderr == "" || strings.Contains(stderr, "must not open desktop session") {
				t.Fatalf("ambiguous input accepted: args=%v opened=%d exit=%d stdout=%q stderr=%q", tc.args, opened, exit, stdout, stderr)
			}
		})
	}
}

// math.MaxInt is rounded upward when converted to float64 on 64-bit Go.
// A coordinate equal to that rounded limit must not pass the comparison
// and then wrap into a negative pointer coordinate during int conversion.
func TestClickScreenRefusesCoordinatesAtRoundedIntMaximumBeforeInjection(t *testing.T) {
	for _, tc := range []struct {
		name           string
		scaleX, scaleY float64
	}{
		{"horizontal", math.Exp2(63), 1},
		{"vertical", 1, math.Exp2(63)},
		{"both", math.Exp2(63), math.Exp2(63)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := &coordinateTestInput{Inputter: &pftest.Inputter{}, space: input.CoordinateSpaceInfo{Kind: input.CoordinateSpacePhysical, ScaleX: tc.scaleX, ScaleY: tc.scaleY}}
			outs := &outputTestLister{outs: []output.Info{{Available: true, Geometry: output.Geometry{W: 100, H: 100}, ResolutionW: 100, ResolutionH: 100}}}
			out, stderr, exit := captureRunIO(t, []string{"input", "click-screen", "--x=1", "--y=1"}, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					return pftest.NewWithOutputs(nil, in, nil, outs, nil), nil
				}
			})
			if exit == 0 || len(in.Calls) != 0 || !strings.Contains(stderr, "outside pointer range") {
				t.Fatalf("overflow was dispatched: exit=%d stdout=%q stderr=%q calls=%v", exit, out, stderr, in.Calls)
			}
		})
	}
}
