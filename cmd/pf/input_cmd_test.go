package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/v2"
	"github.com/nskaggs/perfuncted/v2/input"
	"github.com/nskaggs/perfuncted/v2/internal/third_party/xgb"
	"github.com/nskaggs/perfuncted/v2/internal/third_party/xgb/xproto"
	"github.com/nskaggs/perfuncted/v2/internal/third_party/xgb/xtest"
	"github.com/nskaggs/perfuncted/v2/internal/x11test"
	"github.com/nskaggs/perfuncted/v2/output"
	"github.com/nskaggs/perfuncted/v2/pftest"
	"github.com/nskaggs/perfuncted/v2/window"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A failed button transition must not print success, or try the opposite
// transition to compensate for an action the compositor refused.
func TestInputButtonCommandsPropagateBackendRejection(t *testing.T) {
	denied := errors.New("synthetic compositor refused button transition")
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"press", []string{"input", "mousedown", "--button", "1"}, "mousedown"},
		{"release", []string{"input", "mouseup", "--button", "1"}, "mouseup"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &pftest.Inputter{Err: denied}
			cmd := newRootCmd(func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, backend, nil, nil), nil }
			})
			var out, stderr bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&stderr)
			cmd.SetArgs(tc.args)
			if err := cmd.ExecuteContext(context.Background()); !errors.Is(err, denied) {
				t.Fatalf("provider rejection lost: %v", err)
			}
			if len(backend.Calls) != 1 || backend.Calls[0] != tc.want {
				t.Fatalf("unexpected input dispatched: %v", backend.Calls)
			}
			if out.Len() != 0 {
				t.Fatalf("rejected button command reported success: %q", out.String())
			}
		})
	}
}

type untrustedRelativeCoordinateInput struct{ *pftest.Inputter }

//nolint:unparam // The synthetic coordinate-space provider intentionally returns a valid contract with no error.
func (untrustedRelativeCoordinateInput) PointerCoordinateSpace(context.Context) (input.CoordinateSpaceInfo, error) {
	return input.CoordinateSpaceInfo{Kind: input.CoordinateSpacePhysical, ScaleX: 2, ScaleY: 2}, nil
}

func TestRelativePointerMoveRejectsUnverifiedCoordinateSpaceBeforeInput(t *testing.T) {
	backend := untrustedRelativeCoordinateInput{Inputter: &pftest.Inputter{}}
	session := pftest.New(nil, backend, nil, nil)
	cmd := inputCmd(nil, nil, func(context.Context) (*perfuncted.Session, error) { return session, nil })
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	var out, stderr bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"move-relative", "--dx", "3", "--dy", "4"})
	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Fatal("unverified coordinate-space move accepted")
	}
	if len(backend.Calls) != 0 || out.Len() != 0 {
		t.Fatalf("rejected coordinate-space move dispatched %v or printed %q", backend.Calls, out.String())
	}
}

type screenClickFailingOutput struct {
	*outputTestLister
	failure error
}

func (o *screenClickFailingOutput) List(context.Context) ([]output.Info, error) {
	return nil, o.failure
}

type screenClickSyncFailureInput struct {
	*coordinateTestInput
	syncErr error
}

func (i *screenClickSyncFailureInput) Sync(context.Context) error { return i.syncErr }

// Screen-coordinate clicking is never complete until discovery, delivery and
// requested synchronization have succeeded. A rejected operation cannot emit
// a success line even if an earlier stage made progress.
func TestClickScreenPropagatesDiscoveryInjectionAndSyncFailures(t *testing.T) {
	failure := errors.New("synthetic screen click stage failed")
	for _, tc := range []struct {
		name                               string
		outputError, clickError, syncError error
		wantCalls                          int
	}{
		{name: "output discovery", outputError: failure},
		{name: "pointer injection", clickError: failure, wantCalls: 1},
		{name: "input synchronization", syncError: failure, wantCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			injected := &pftest.Inputter{Err: tc.clickError}
			in := &screenClickSyncFailureInput{coordinateTestInput: &coordinateTestInput{Inputter: injected, space: input.CoordinateSpaceInfo{Kind: input.CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1}}, syncErr: tc.syncError}
			outs := &screenClickFailingOutput{outputTestLister: &outputTestLister{outs: []output.Info{{Available: true, Geometry: output.Geometry{W: 100, H: 100}, ResolutionW: 100, ResolutionH: 100}}}, failure: tc.outputError}
			var provider output.Lister = outs
			if tc.outputError == nil {
				provider = &outputTestLister{outs: []output.Info{{Available: true, Geometry: output.Geometry{W: 100, H: 100}, ResolutionW: 100, ResolutionH: 100}}}
			}
			sessionOpens := 0
			opener := func(context.Context) (*perfuncted.Session, error) {
				sessionOpens++
				return pftest.NewWithOutputs(nil, in, nil, provider, nil), nil
			}
			cmd := inputCmd(opener, &cliConfig{sync: true}, nil, opener)
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			cmd.SetArgs([]string{"click-screen", "--x", "12", "--y", "34"})
			var stdout bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(io.Discard)
			err := cmd.ExecuteContext(context.Background())
			if !errors.Is(err, failure) {
				t.Fatalf("stage failure not propagated: %v", err)
			}
			if stdout.Len() != 0 || sessionOpens != 1 || len(injected.Calls) != tc.wantCalls {
				t.Fatalf("failed screen click published %q, opened %d sessions, calls=%v", stdout.String(), sessionOpens, injected.Calls)
			}
		})
	}
}

type coverageTitleClickInput struct {
	*pftest.Inputter
	space    input.CoordinateSpaceInfo
	spaceErr error
}

func (i *coverageTitleClickInput) PointerCoordinateSpace(context.Context) (input.CoordinateSpaceInfo, error) {
	return i.space, i.spaceErr
}

func TestInputClickWindowTitleRequiresOneCurrentTargetAndSafeDispatch(t *testing.T) {
	coordinateError := errors.New("synthetic coordinate metadata unavailable")
	clickError := errors.New("synthetic click transport failure")
	target := window.Info{NativeID: "editor-window", Title: "Editor", X: 100, Y: 200, W: 400, H: 300}
	space := input.CoordinateSpaceInfo{Kind: input.CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1}
	tests := []struct {
		name        string
		lists       [][]window.Info
		spaceErr    error
		inputErr    error
		wantSuccess bool
		wantErr     string
		wantClick   string
	}{
		{
			name:        "unique title maps into current window",
			lists:       [][]window.Info{{target}, {target}},
			wantSuccess: true,
			wantClick:   "click:120,230",
		},
		{
			name:      "ambiguous title refuses to choose a window",
			lists:     [][]window.Info{{target, {NativeID: "another-window", Title: "Editor settings"}}},
			wantErr:   "ambiguous",
			wantClick: "",
		},
		{
			name:      "window disappearance before geometry refresh refuses click",
			lists:     [][]window.Info{{target}, {}},
			wantErr:   "not found",
			wantClick: "",
		},
		{
			name:      "unavailable coordinate contract refuses click",
			lists:     [][]window.Info{{target}, {target}},
			spaceErr:  coordinateError,
			wantErr:   "cannot safely map window coordinates",
			wantClick: "",
		},
		{
			name:      "click transport failure is returned without success output",
			lists:     [][]window.Info{{target}, {target}},
			inputErr:  clickError,
			wantErr:   clickError.Error(),
			wantClick: "click:120,230",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := &coverageTitleClickInput{Inputter: &pftest.Inputter{Err: tc.inputErr}, space: space, spaceErr: tc.spaceErr}
			manager := &pftest.Manager{Lists: tc.lists}
			stdout, stderr, code := captureRunIO(t, []string{"input", "click-window", "--title", "Editor", "--x", "20", "--y", "30"}, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					return pftest.New(nil, in, manager, nil), nil
				}
			})
			if tc.wantSuccess {
				if code != 0 || !strings.Contains(stdout, "click event sent to window editor-window") {
					t.Fatalf("unique title click: code=%d stdout=%q stderr=%q", code, stdout, stderr)
				}
			} else {
				if code == 0 || !strings.Contains(stderr, tc.wantErr) {
					t.Fatalf("unsafe or failed click returned code=%d stdout=%q stderr=%q, want error containing %q", code, stdout, stderr, tc.wantErr)
				}
				if strings.Contains(stdout, "click event sent") {
					t.Fatalf("failed click reported success: %q", stdout)
				}
			}
			calls := strings.Join(in.Calls, ",")
			if tc.wantClick == "" {
				if strings.Contains(calls, "click:") {
					t.Fatalf("rejected click reached input backend: %q", calls)
				}
			} else if !strings.Contains(calls, tc.wantClick) {
				t.Fatalf("input calls=%q, want click %q", calls, tc.wantClick)
			}
		})
	}
}

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

func TestInputHotkeyCLIValidatesBeforeOpeningSession(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"missing", nil, "choose exactly one"},
		{"bad output", []string{"--combo", "F8", "--output", "text"}, "unsupported output"},
		{"bad button", []string{"--button", "wheel"}, "invalid mouse button"},
		{"both filters", []string{"--button", "left", "--combo", "F8"}, "choose exactly one"},
		{"bad chord", []string{"--combo", "Ctrl+"}, "unsupported key"},
		{"bad event", []string{"--combo", "F8", "--event", "held"}, "unsupported transition"},
		{"malformed flag", []string{"--combo", "F8", "--timeout", "bad"}, "invalid argument"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := 0
			cmd := inputHotkeyCmd(func(context.Context) (*perfuncted.Session, error) {
				called++
				return nil, errors.New("unexpected open")
			})
			cmd.SetArgs(append([]string{"listen"}, tc.args...))
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			cmd.SetOut(&bytes.Buffer{})
			err := cmd.ExecuteContext(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v; want %q", err, tc.want)
			}
			if called != 0 {
				t.Fatalf("opened backend for invalid CLI filter (%d calls)", called)
			}
		})
	}
}

func TestInputHotkeyCLISelectsObservationOnly(t *testing.T) {
	cases := []struct{ args []string }{
		{[]string{"--combo", "F8", "--event", "press"}},
		{[]string{"--button", "left", "--event", "both"}},
		{[]string{"--button", "middle", "--event", "release"}},
		{[]string{"--button", "right"}},
		{[]string{"--button", "1"}},
		{[]string{"--button", "2"}},
		{[]string{"--button", "3"}},
	}
	sentinel := errors.New("observation opener selected")
	for i, tc := range cases {
		t.Run(fmt.Sprintf("valid-%d", i), func(t *testing.T) {
			called := 0
			cmd := inputHotkeyCmd(func(context.Context) (*perfuncted.Session, error) { called++; return nil, sentinel })
			cmd.SetArgs(append([]string{"listen"}, tc.args...))
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			cmd.SetOut(&bytes.Buffer{})
			if err := cmd.ExecuteContext(context.Background()); !errors.Is(err, sentinel) {
				t.Fatal(err)
			}
			if called != 1 {
				t.Fatalf("observer opened %d times", called)
			}
		})
	}
}

func TestInputHotkeyCLICanceledAndTimedOutContext(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		ctx   context.Context
		flags []string
		want  error
	}{
		{canceled, []string{"--combo", "F8"}, context.Canceled},
		{context.Background(), []string{"--combo", "F8", "--timeout", "10ms"}, context.DeadlineExceeded},
	} {
		cmd := inputHotkeyCmd(func(ctx context.Context) (*perfuncted.Session, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		})
		cmd.SetArgs(append([]string{"listen"}, tc.flags...))
		cmd.SilenceUsage = true
		cmd.SilenceErrors = true
		if err := cmd.ExecuteContext(tc.ctx); !errors.Is(err, tc.want) {
			t.Fatal(err)
		}
	}
}

// TestInputHotkeyCLIControlledX11 exercises the actual Session, command,
// JSON streaming and cancellation using synthetic input in isolated Xvfb.
// It does not establish physical-input provenance on a native Xorg desktop.
func TestInputHotkeyCLIControlledX11(t *testing.T) {
	if os.Getenv("PF_X11_OBSERVATION_INTEGRATION") != "1" {
		t.Skip("requires isolated Xvfb")
	}
	display, stop, err := x11test.StartXvfbNoReset()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	t.Setenv("DISPLAY", display)
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("XDG_SESSION_TYPE", "x11")
	sender, err := xgb.NewConnDisplay(display)
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	if err := xtest.Init(sender); err != nil {
		t.Fatal(err)
	}
	// F8 is typically X11 keysym 0xffc5; derive the actual mapped keycode.
	reply, err := xproto.GetKeyboardMapping(sender, xproto.Setup(sender).MinKeycode,
		byte(int(xproto.Setup(sender).MaxKeycode)-int(xproto.Setup(sender).MinKeycode)+1)).Reply()
	if err != nil {
		t.Fatal(err)
	}
	key := byte(0)
	stride := int(reply.KeysymsPerKeycode)
	for i := 0; i+stride <= len(reply.Keysyms); i += stride {
		for _, sym := range reply.Keysyms[i : i+stride] {
			if uint32(sym) == 0xffc5 {
				key = byte(int(xproto.Setup(sender).MinKeycode) + i/stride)
				break
			}
		}
		if key != 0 {
			break
		}
	}
	if key == 0 {
		t.Fatal("F8 absent from isolated keymap")
	}
	root := xproto.Setup(sender).DefaultScreen(sender).Root
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var out bytes.Buffer
	cmd := newRootCmd(defaultOpenPFFactory)
	cmd.SetArgs([]string{"input", "hotkey", "listen", "--combo", "F8", "--event", "both"})
	cmd.SetOut(&out)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	finished := make(chan error, 1)
	go func() { finished <- cmd.ExecuteContext(ctx) }()
	// Generate multiple candidate transitions after registration has time to
	// finish; no unbounded retries or production input injection.
	time.Sleep(200 * time.Millisecond)
	for j := 0; j < 5; j++ {
		for _, kind := range []byte{xproto.KeyPress, xproto.KeyRelease} {
			if err := xtest.FakeInputChecked(sender, kind, key, xproto.TimeCurrentTime, root, 0, 0, 0).Check(); err != nil {
				t.Fatal(err)
			}
		}
		time.Sleep(60 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-finished:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("CLI streaming listener did not cancel")
	}
	output := out.String()
	if !strings.Contains(output, `"source":"x11-xi2"`) ||
		!strings.Contains(output, `"transition":"press"`) ||
		!strings.Contains(output, `"transition":"release"`) {
		t.Fatalf("missing JSON hotkey transitions: %q", output)
	}
}

type cliObservationSub struct {
	events chan input.ObservationEvent
	err    error
}

func (s *cliObservationSub) Events() <-chan input.ObservationEvent { return s.events }
func (s *cliObservationSub) Err() error                            { return s.err }
func (s *cliObservationSub) Close() error                          { return nil }

type cliFailedWriter struct{ err error }

func (w cliFailedWriter) Write([]byte) (int, error) { return 0, w.err }

func TestInputHotkeyStreamContract(t *testing.T) {
	t.Run("cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		sub := &cliObservationSub{events: make(chan input.ObservationEvent)}
		if err := streamInputObservation(ctx, sub, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("deadline", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		sub := &cliObservationSub{events: make(chan input.ObservationEvent)}
		if err := streamInputObservation(ctx, sub, &bytes.Buffer{}); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	})
	for _, tc := range []struct {
		name  string
		cause error
		want  error
	}{
		{"normal", nil, nil},
		{"subscription-cancel", context.Canceled, nil},
		{"event-loss", input.ErrObservationLoss, input.ErrObservationLoss},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := make(chan input.ObservationEvent)
			close(events)
			sub := &cliObservationSub{events: events, err: tc.cause}
			err := streamInputObservation(context.Background(), sub, &bytes.Buffer{})
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
	t.Run("json", func(t *testing.T) {
		events := make(chan input.ObservationEvent, 2)
		events <- input.ObservationEvent{Kind: "hotkey", ID: "F8", Transition: "press", Source: "x11-xi2"}
		events <- input.ObservationEvent{Kind: "hotkey", ID: "F8", Transition: "release", Source: "x11-xi2"}
		close(events)
		var buf bytes.Buffer
		if err := streamInputObservation(context.Background(), &cliObservationSub{events: events}, &buf); err != nil {
			t.Fatal(err)
		}
		if strings.Count(buf.String(), "x11-xi2") != 2 {
			t.Fatal(buf.String())
		}
	})
	t.Run("writer-error", func(t *testing.T) {
		events := make(chan input.ObservationEvent, 1)
		events <- input.ObservationEvent{Kind: "hotkey", ID: "F8", Transition: "press"}
		sentinel := errors.New("failed writer")
		err := streamInputObservation(context.Background(), &cliObservationSub{events: events}, cliFailedWriter{err: sentinel})
		if !errors.Is(err, sentinel) {
			t.Fatal(err)
		}
	})
}

func TestInputHotkeyCLIRejectsSessionWithoutObserver(t *testing.T) {
	cmd := newRootCmd(func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, nil, nil, nil), nil }
	})
	cmd.SetArgs([]string{"input", "hotkey", "listen", "--combo", "F8"})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Fatal("CLI accepted unavailable observation capability")
	}
}

type unsupportedPointerLocationInputter struct {
	*pftest.Inputter
}

func (unsupportedPointerLocationInputter) PointerLocation(context.Context) (int, int, error) {
	return 0, 0, perfuncted.ErrUnsupported
}

type cursorPositionWindowManager struct {
	*pftest.Manager
	x, y       int
	closeCalls int
}

func (m *cursorPositionWindowManager) CursorPosition(context.Context) (int, int, error) {
	return m.x, m.y, nil
}

func (*cursorPositionWindowManager) SupportedOperations() []string {
	return []string{"cursor-position"}
}

func (m *cursorPositionWindowManager) Close() error {
	m.closeCalls++
	return m.Manager.Close()
}

func TestInputLocationFallsBackToWindowCursorWhenInjectionBackendCannotReportIt(t *testing.T) {
	inputSession := pftest.New(nil, unsupportedPointerLocationInputter{Inputter: &pftest.Inputter{}}, nil, nil)
	windowManager := &cursorPositionWindowManager{Manager: &pftest.Manager{}, x: 73, y: 118}
	windowSession := pftest.New(nil, nil, windowManager, nil)
	var stdout bytes.Buffer
	cmd := inputCmd(
		func(context.Context) (*perfuncted.Session, error) { return inputSession, nil },
		nil,
		func(context.Context) (*perfuncted.Session, error) { return windowSession, nil },
	)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"location"})
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("input location failed with a window cursor backend available: %v", err)
	}
	if got := stdout.String(); got != "73,118\n" {
		t.Fatalf("input location output = %q, want %q", got, "73,118\n")
	}
	if windowManager.closeCalls != 1 {
		t.Fatalf("fallback window session closed %d times, want once", windowManager.closeCalls)
	}
}

func TestInputLocationJSONDoesNotFallback(t *testing.T) {
	s := pftest.New(nil, unsupportedPointerLocationInputter{Inputter: &pftest.Inputter{}}, nil, nil)
	var stdout bytes.Buffer
	cmd := inputCmd(func(context.Context) (*perfuncted.Session, error) { return s, nil }, nil, func(context.Context) (*perfuncted.Session, error) {
		t.Fatal("unexpected window fallback")
		return nil, nil
	})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"location", "--output", "json"})
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); !errors.Is(err, perfuncted.ErrUnsupported) || stdout.Len() != 0 {
		t.Fatalf("got %q, %v", stdout.String(), err)
	}
}

type observedPointerInputter struct {
	*pftest.Inputter
	result input.PointerObservation
}

func (p observedPointerInputter) ObservePointer(context.Context) (input.PointerObservation, error) {
	return p.result, nil
}

func TestInputLocationJSONObservation(t *testing.T) {
	want := input.PointerObservation{
		X: -42, Y: 0,
		CoordinateSpace: input.CoordinateSpaceInfo{Kind: input.CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1},
		Provider:        "gnome-native",
		ObservedAt:      time.Date(2026, 11, 24, 15, 0, 0, 123, time.UTC),
	}
	s := pftest.New(nil, observedPointerInputter{Inputter: &pftest.Inputter{}, result: want}, nil, nil)
	var stdout bytes.Buffer
	cmd := inputCmd(func(context.Context) (*perfuncted.Session, error) { return s, nil }, nil, func(context.Context) (*perfuncted.Session, error) { t.Fatal("unexpected fallback"); return nil, nil })
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"location", "--output", "json"})
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var got input.PointerObservation
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

type failedInputReader struct{}

func (failedInputReader) Read([]byte) (int, error) {
	return 0, errors.New("synthetic stdin read failure")
}

// Input requests that cannot be fulfilled without a capability must not open
// any desktop, and failed stdin reads must not inject a partial payload.
func TestInputCLIRejectsMissingCapabilityAndFailedStdinWithoutAuthorization(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(file, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		args  []string
		input io.Reader
		want  string
	}{
		{"relative pointer needs window", []string{"move-relative"}, nil, "requires window capability"},
		{"window click needs window", []string{"click-window", "--title", "Editor"}, nil, "requires windows capability"},
		{"screenshot click needs outputs", []string{"click-screen", "--x", "0", "--y", "0"}, nil, "requires outputs capability"},
		{"file selection needs dialog", []string{"select-file", "--title", "Open", "--path", file}, nil, "requires window and clipboard capabilities"},
		{"failed stdin read", []string{"type", "--stdin"}, failedInputReader{}, "synthetic stdin read failure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opens := 0
			cmd := inputCmd(func(context.Context) (*perfuncted.Session, error) {
				opens++
				return nil, errors.New("desktop authorization attempted")
			}, nil)
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			cmd.SetArgs(tc.args)
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			if tc.input != nil {
				cmd.SetIn(tc.input)
			}
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), tc.want) || opens != 0 {
				t.Fatalf("args=%v unauthorized opens=%d err=%v expected %q", tc.args, opens, err, tc.want)
			}
		})
	}
}

type relativeSyncFailureInput struct {
	*relativePointerInput
	err   error
	calls int
}

func (i *relativeSyncFailureInput) Sync(context.Context) error { i.calls++; return i.err }

// Relative moves first verify an authoritative logical coordinate space and
// read a fresh cursor location. Failure during the actual move or the
// subsequently requested sync must never produce a successful move receipt.
func TestRelativePointerCLIPropagatesLateMovementAndSyncFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		sync bool
	}{
		{"movement refused", false},
		{"post-movement synchronization refused", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failure := errors.New("synthetic pointer backend denied late operation")
			i, w := newRelativePointerBackend(100, 200)
			input := &relativeSyncFailureInput{relativePointerInput: i, err: failure}
			if !tc.sync {
				i.Inputter.Err = failure
			}
			opens := 0
			args := []string{"input", "move-relative", "--dx=7", "--dy=-3", "--verify=false"}
			if tc.sync {
				args = append([]string{"--sync"}, args...)
			}
			stdout, stderr, exit := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opens++
					return perfuncted.NewSessionForTesting(nil, input, w, nil, nil), nil
				}
			})
			expectedSyncs := 0
			if tc.sync {
				expectedSyncs = 1
			}
			if exit == 0 || stdout != "" || !strings.Contains(stderr, failure.Error()) || opens != 1 || input.calls != expectedSyncs {
				t.Fatalf("late failure misreported args=%v exit=%d out=%q err=%q opens=%d syncs=%d", args, exit, stdout, stderr, opens, input.calls)
			}
			if tc.sync && (len(i.Calls) != 1 || i.Calls[0] != "move:107,197") {
				t.Fatalf("synchronization attempted without a successful move: %v", i.Calls)
			}
			if !tc.sync && (len(i.Calls) != 1 || i.Calls[0] != "move:107,197" || i.x != 100 || i.y != 200) {
				t.Fatalf("failed move changed pointer position or attempted unexpected calls: position=%d,%d calls=%v", i.x, i.y, i.Calls)
			}
		})
	}
}

// The command factory may be used without a window-capable session opener.
// It must reject relative motion before attempting input authorization.
func TestRelativePointerRequiresWindowOpenerBeforeAuthorization(t *testing.T) {
	opens := 0
	cmd := inputCmd(func(context.Context) (*perfuncted.Session, error) {
		opens++
		return nil, errors.New("unexpected desktop authorization")
	}, nil)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"move-relative", "--dx=7"})
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	err := cmd.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "requires window capability") || stdout.Len() != 0 || opens != 0 {
		t.Fatalf("unsupported relative motion requested desktop access err=%v opens=%d out=%q", err, opens, stdout.String())
	}
}

type relativePointerInput struct {
	*pftest.Inputter
	x, y        int
	locationErr error
	spaceErr    error
	space       input.CoordinateSpaceInfo
}

func (i *relativePointerInput) PointerCoordinateSpace(context.Context) (input.CoordinateSpaceInfo, error) {
	return i.space, i.spaceErr
}
func (i *relativePointerInput) PointerLocation(context.Context) (int, int, error) {
	return i.x, i.y, i.locationErr
}
func (i *relativePointerInput) MouseMove(ctx context.Context, x, y int) error {
	if err := i.Inputter.MouseMove(ctx, x, y); err != nil {
		return err
	}
	i.x, i.y = x, y
	return nil
}

type relativePointerWindow struct {
	*pftest.Manager
	in               *relativePointerInput
	cursorErr        error
	freeze           bool
	frozenX, frozenY int
}

func (w *relativePointerWindow) SupportedOperations() []string { return []string{"cursor-position"} }
func (w *relativePointerWindow) CursorPosition(context.Context) (int, int, error) {
	if w.freeze {
		return w.frozenX, w.frozenY, w.cursorErr
	}
	return w.in.x, w.in.y, w.cursorErr
}
func runRelativePointerCLI(t *testing.T, i *relativePointerInput, w *relativePointerWindow, args ...string) (string, string, int) {
	t.Helper()
	return captureRunIO(t, args, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) {
			return perfuncted.NewSessionForTesting(nil, i, w, nil, nil), nil
		}
	})
}
func newRelativePointerBackend(x, y int) (*relativePointerInput, *relativePointerWindow) {
	i := &relativePointerInput{Inputter: &pftest.Inputter{}, x: x, y: y, space: input.CoordinateSpaceInfo{Kind: input.CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1}}
	return i, &relativePointerWindow{Manager: &pftest.Manager{}, in: i}
}
func TestRelativePointerCLISuccessAndFallbackToCompositor(t *testing.T) {
	for _, tc := range []struct {
		name     string
		fallback bool
		verify   bool
	}{
		{"input coordinates", false, true},
		{"compositor fallback", true, true},
		{"no verification", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i, w := newRelativePointerBackend(100, 200)
			if tc.fallback {
				i.locationErr = input.ErrNotSupported
			}
			args := []string{"input", "move-relative", "--dx=13", "--dy=-7"}
			if !tc.verify {
				args = append(args, "--verify=false")
			}
			out, stderr, code := runRelativePointerCLI(t, i, w, args...)
			if code != 0 || !strings.Contains(out, "113,193") || len(i.Calls) != 1 || i.Calls[0] != "move:113,193" {
				t.Fatalf("relative movement: code=%d out=%q stderr=%q calls=%v", code, out, stderr, i.Calls)
			}
		})
	}
}
func TestRelativePointerCLIRejectsUnsafeCoordinatesBeforeInput(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		x, y   int
		modify func(*relativePointerInput, *relativePointerWindow)
		want   string
	}{
		{"positive-overflow", []string{"input", "move-relative", "--dx=2", "--verify=false"}, math.MaxInt - 1, 0, nil, "overflows"},
		{"negative-overflow", []string{"input", "move-relative", "--dx=-2", "--verify=false"}, math.MinInt + 1, 0, nil, "overflows"},
		{"y-overflow", []string{"input", "move-relative", "--dy=2", "--verify=false"}, 0, math.MaxInt - 1, nil, "overflows"},
		{"untrusted-scale", []string{"input", "move-relative", "--dx=2"}, 0, 0, func(i *relativePointerInput, _ *relativePointerWindow) { i.space.ScaleX = 1.25 }, "logical pointer coordinate scale"},
		{"coordinate-space-failure", []string{"input", "move-relative", "--dx=2"}, 0, 0, func(i *relativePointerInput, _ *relativePointerWindow) {
			i.spaceErr = errors.New("synthetic coordinate space failure")
		}, "synthetic coordinate space failure"},
		{"location-failure", []string{"input", "move-relative", "--dx=2"}, 0, 0, func(i *relativePointerInput, _ *relativePointerWindow) {
			i.locationErr = errors.New("synthetic cursor query failed")
		}, "synthetic cursor query failed"},
		{"compositor-failure", []string{"input", "move-relative", "--dx=2"}, 0, 0, func(i *relativePointerInput, w *relativePointerWindow) {
			i.locationErr = input.ErrNotSupported
			w.cursorErr = errors.New("synthetic compositor error")
		}, "synthetic compositor error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			i, w := newRelativePointerBackend(tc.x, tc.y)
			if tc.modify != nil {
				tc.modify(i, w)
			}
			out, stderr, code := runRelativePointerCLI(t, i, w, tc.args...)
			if code == 0 || !strings.Contains(stderr, tc.want) || len(i.Calls) != 0 {
				t.Fatalf("unsafe operation: code=%d out=%q stderr=%q calls=%v", code, out, stderr, i.Calls)
			}
		})
	}
}

func TestRelativePointerCLIRequiresFreshCompositorVerification(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*relativePointerWindow)
		want      string
	}{
		{"stale-position", func(w *relativePointerWindow) { w.freeze = true; w.frozenX = 100; w.frozenY = 200 }, "relative pointer move unverified"},
		{"compositor-read-error", func(w *relativePointerWindow) { w.cursorErr = errors.New("synthetic post-move cursor failure") }, "relative pointer position verification"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i, w := newRelativePointerBackend(100, 200)
			tc.configure(w)
			out, stderr, code := runRelativePointerCLI(t, i, w, "input", "move-relative", "--dx=7", "--dy=9")
			if code == 0 || !strings.Contains(stderr, tc.want) || len(i.Calls) != 1 || i.Calls[0] != "move:107,209" {
				t.Fatalf("unverified cursor move: code=%d out=%q stderr=%q calls=%v", code, out, stderr, i.Calls)
			}
		})
	}
}

func TestInputSmoothCLIRejectsInvalidFlagsBeforeSession(t *testing.T) {
	for _, args := range [][]string{
		{"move", "--x=5", "--duration=-1s"},
		{"move", "--x=5", "--duration=11s"},
		{"move", "--x=5", "--duration=20ms", "--easing=nope"},
		{"move", "--x=5", "--duration=20ms", "--curve=NaN"},
		{"move", "--x=5", "--duration=20ms", "--variation=17"},
		{"move", "--x=5", "--duration=20ms", "--from-x=1"},
		{"drag-and-drop", "--duration=20ms", "--easing=nope"},
		{"drag-and-drop", "--duration=10ms", "--curve=0.8"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			opens := 0
			cmd := inputCmd(func(context.Context) (*perfuncted.Session, error) {
				opens++
				return pftest.New(nil, &pftest.Inputter{}, nil, nil), nil
			}, nil)
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs(args)
			if err := cmd.ExecuteContext(context.Background()); err == nil {
				t.Fatal("accepted invalid motion")
			}
			if opens != 0 || out.Len() != 0 {
				t.Fatalf("session opened or success output: opens=%d out=%q", opens, out.String())
			}
		})
	}
}
func TestInputSmoothCLIEmitsSingleOutcome(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		last string
		out  string
		drag bool
	}{
		{"immediate", []string{"move", "--x=7", "--y=9"}, "move:7,9", "moved to 7,9", false},
		{"smooth-explicit", []string{"move", "--from-x=1", "--from-y=2", "--x=70", "--y=90", "--duration=40ms", "--easing=ease-in-out", "--seed=3", "--variation=2"}, "move:70,90", "moved to 70,90", false},
		{"smooth-drag", []string{"drag-and-drop", "--x1=1", "--y1=2", "--x2=70", "--y2=90", "--duration=40ms", "--curve=0.2"}, "mouseup", "dragged 1,2 to 70,90", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &pftest.Inputter{}
			cmd := inputCmd(func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, backend, nil, nil), nil }, nil)
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs(tc.args)
			if err := cmd.ExecuteContext(context.Background()); err != nil {
				t.Fatal(err)
			}
			calls := backend.Calls
			if len(calls) == 0 || calls[len(calls)-1] != tc.last {
				t.Fatalf("calls=%v", calls)
			}
			if strings.TrimSpace(out.String()) != tc.out {
				t.Fatalf("output=%q", out.String())
			}
			if tc.drag {
				down, up := 0, 0
				for _, c := range calls {
					if c == "mousedown" {
						down++
					}
					if c == "mouseup" {
						up++
					}
				}
				if down != 1 || up != 1 || len(calls) < 4 {
					t.Fatalf("drag calls=%v", calls)
				}
			}
		})
	}
}
func TestInputSmoothCLIUnknownOriginDoesNotClaimSuccess(t *testing.T) {
	backend := &pftest.Inputter{Err: input.ErrNotSupported}
	cmd := inputCmd(func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, backend, nil, nil), nil }, nil)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"move", "--x=10", "--y=20", "--duration=40ms"})
	err := cmd.ExecuteContext(context.Background())
	if !errors.Is(err, input.ErrNotSupported) || len(backend.Calls) != 0 || out.Len() != 0 {
		t.Fatalf("unknown origin err=%v calls=%v out=%q", err, backend.Calls, out.String())
	}
}

type inputSyncOnlyFailure struct {
	*pftest.Inputter
	syncFailure error
	calls       int
}

func (f *inputSyncOnlyFailure) Sync(context.Context) error { f.calls++; return f.syncFailure }

// Every --sync command must finish its input action, then refuse to report
// success if the compositor does not acknowledge the synchronization barrier.
// A mock that fails every input call would never reach these branches.
func TestInputCommandsRejectPostMutationSynchronizationFailures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		wantCall string
	}{
		{"move", []string{"move", "--x", "5", "--y", "9"}, "move:5,9"},
		{"click", []string{"click", "--x", "4", "--y", "8"}, "click:4,8"},
		{"double click", []string{"double-click"}, "mousedown"},
		{"drag", []string{"drag-and-drop", "--x1", "1", "--y1", "2", "--x2", "7", "--y2", "8"}, "move:1,2"},
		{"center", []string{"click-center", "--rect", "0,0,10,10"}, "click:5,5"},
		{"type", []string{"type", "hello"}, "type:hello"},
		{"keydown", []string{"keydown", "ctrl"}, "down:ctrl"},
		{"keyup", []string{"keyup", "ctrl"}, "up:ctrl"},
		{"mousedown", []string{"mousedown", "--button", "2"}, "mousedown"},
		{"mouseup", []string{"mouseup", "--button", "2"}, "mouseup"},
		{"scroll up", []string{"scroll", "up", "--clicks", "2"}, "scroll-up:2"},
		{"scroll down", []string{"scroll", "down", "--clicks", "2"}, "scroll-down:2"},
		{"scroll left", []string{"scroll", "left", "--clicks", "2"}, "scroll-left:2"},
		{"scroll right", []string{"scroll", "right", "--clicks", "2"}, "scroll-right:2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errSync := errors.New("synthetic compositor barrier failed")
			input := &inputSyncOnlyFailure{Inputter: &pftest.Inputter{}, syncFailure: errSync}
			args := append([]string{"--sync", "input"}, tc.args...)
			out, stderr, exit := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, input, nil, nil), nil }
			})
			if exit == 0 || out != "" || !strings.Contains(stderr, errSync.Error()) || input.calls != 1 {
				t.Fatalf("sync failure was hidden: args=%v status=%d out=%q stderr=%q syncCalls=%d inputCalls=%v", args, exit, out, stderr, input.calls, input.Inputter.Calls)
			}
			if len(input.Inputter.Calls) == 0 {
				t.Fatal("sync called without performing any input mutation")
			}
			found := false
			for _, call := range input.Inputter.Calls {
				if call == tc.wantCall {
					found = true
				}
			}
			if !found {
				t.Fatalf("mutating action %q was not executed before sync: %v", tc.wantCall, input.Inputter.Calls)
			}
		})
	}
}

type syntheticWindowClickSyncFailure struct {
	*coverageTitleClickInput
	failure error
}

func (s *syntheticWindowClickSyncFailure) Sync(context.Context) error { return s.failure }

type syntheticPickerActivationFailure struct {
	*pftest.Manager
	failure error
}

func (s *syntheticPickerActivationFailure) ActivateByID(context.Context, string) error {
	return s.failure
}

func TestClickWindowRejectsFailedSynchronizationWithoutSuccessOutput(t *testing.T) {
	failure := errors.New("synthetic synchronization refused")
	in := &syntheticWindowClickSyncFailure{
		coverageTitleClickInput: &coverageTitleClickInput{
			Inputter: &pftest.Inputter{},
			space:    input.CoordinateSpaceInfo{Kind: input.CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1},
		},
		failure: failure,
	}
	manager := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "editor", Title: "Editor", X: 10, Y: 20, W: 100, H: 100}}}}
	opener := func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, in, manager, nil), nil }
	cmd := inputCmd(opener, &cliConfig{sync: true}, opener)
	cmd.SetArgs([]string{"click-window", "--id", "editor", "--x", "3", "--y", "5"})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	if err := cmd.ExecuteContext(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("failed synchronization not reported: %v", err)
	}
	if stdout.Len() != 0 || len(in.Inputter.Calls) != 1 || in.Inputter.Calls[0] != "click:13,25" {
		t.Fatalf("unexpected output=%q input calls=%v", stdout.String(), in.Inputter.Calls)
	}
}

func TestFilePickerStopsOnMissingTargetOrFailedActivation(t *testing.T) {
	failure := errors.New("synthetic activation denied")
	destination := filepath.Join(t.TempDir(), "test-file.txt")
	if err := os.WriteFile(destination, []byte("private synthetic fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		manager   window.Manager
		wantError error
	}{
		{name: "missing target", manager: &pftest.Manager{Lists: [][]window.Info{{}}}, wantError: window.ErrWindowNotFound},
		{name: "activation rejected", manager: &syntheticPickerActivationFailure{Manager: &pftest.Manager{Lists: [][]window.Info{{{NativeID: "chooser", Title: "File Upload", Active: true}}}}, failure: failure}, wantError: failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := &pftest.Inputter{}
			cb := &pftest.Clipboard{Text: "previous clipboard"}
			opener := func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, in, tc.manager, cb), nil }
			cmd := inputCmd(opener, nil, nil, nil, opener)
			cmd.SetArgs([]string{"select-file", "--title", "File Upload", "--path", destination})
			var stdout bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(io.Discard)
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			err := cmd.ExecuteContext(context.Background())
			if !errors.Is(err, tc.wantError) {
				t.Fatalf("target failure not reported: %v, wanted %v", err, tc.wantError)
			}
			if stdout.Len() != 0 || len(in.Calls) != 0 || cb.Text != "previous clipboard" {
				t.Fatalf("rejected file selection leaked output=%q, input=%v, clipboard=%q", stdout.String(), in.Calls, cb.Text)
			}
		})
	}
}

func TestRelativePointerMovePropagatesSecondaryOpenerFailure(t *testing.T) {
	failure := errors.New("synthetic window authorization denied")
	openCalls := 0
	opener := func(context.Context) (*perfuncted.Session, error) { openCalls++; return nil, failure }
	cmd := inputCmd(nil, nil, opener)
	cmd.SetArgs([]string{"move-relative", "--dx", "4", "--dy", "5"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	if err := cmd.ExecuteContext(context.Background()); !errors.Is(err, failure) || openCalls != 1 {
		t.Fatalf("secondary authorization failure not propagated: err=%v openCalls=%d", err, openCalls)
	}
}

func TestRemoteDesktopCLIOptionalInfoCanBuildOptions(t *testing.T) {
	for _, tt := range []struct {
		name               string
		required, optional []perfuncted.Capability
		remember           bool
	}{
		{name: "required input", required: []perfuncted.Capability{perfuncted.CapabilityInput}},
		{name: "optional info includes input", optional: []perfuncted.Capability{perfuncted.CapabilityInput, perfuncted.CapabilityWindows, perfuncted.CapabilityScreen}},
		{name: "remember user consent", optional: []perfuncted.Capability{perfuncted.CapabilityInput}, remember: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel() // options must validate without touching host
			_, err := defaultOpenPFFactory(&cliConfig{inputBackend: "remote-desktop", required: tt.required, optional: tt.optional, rememberInputPermission: tt.remember})(ctx)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("options failed before canceled open: %v", err)
			}
		})
	}
}
func TestRemoteDesktopCLIRejectsInvalidConfigurations(t *testing.T) {
	for _, cfg := range []cliConfig{{inputBackend: "missing"}, {inputBackend: "auto", rememberInputPermission: true}} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := defaultOpenPFFactory(&cfg)(ctx)
		if err == nil || !strings.Contains(err.Error(), "--") {
			t.Errorf("config %+v expected argument error, got %v", cfg, err)
		}
	}
}
