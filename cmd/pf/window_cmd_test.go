package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"io"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/v2"
	"github.com/nskaggs/perfuncted/v2/input"
	"github.com/nskaggs/perfuncted/v2/output"
	"github.com/nskaggs/perfuncted/v2/pftest"
	"github.com/nskaggs/perfuncted/v2/window"
)

func TestScreenPixelActionsRefuseMixedScaleMultiOutputMosaics(t *testing.T) {
	cases := []struct {
		name string
		outs []output.Info
	}{
		{"negative origin mixed scaling", []output.Info{
			{Name: "left", Available: true, Geometry: output.Geometry{X: -2560, Y: 0, W: 2560, H: 1440}, ResolutionW: 3840, ResolutionH: 2160, ScaleNumerator: 3, ScaleDenominator: 2},
			{Name: "right", Available: true, Geometry: output.Geometry{X: 0, Y: 0, W: 1920, H: 1080}, ResolutionW: 1920, ResolutionH: 1080, Scale: 1},
		}},
		{"disjoint outputs", []output.Info{
			{Name: "top", Available: true, Geometry: output.Geometry{X: 0, Y: -1200, W: 1600, H: 900}, ResolutionW: 1920, ResolutionH: 1080, ScaleNumerator: 6, ScaleDenominator: 5},
			{Name: "bottom", Available: true, Geometry: output.Geometry{X: 0, Y: 240, W: 2560, H: 1440}, ResolutionW: 3840, ResolutionH: 2160, ScaleNumerator: 3, ScaleDenominator: 2},
		}},
		{"side by side same scale", []output.Info{
			{Name: "first", Available: true, Geometry: output.Geometry{X: 0, Y: 0, W: 1600, H: 900}, ResolutionW: 1600, ResolutionH: 900, Scale: 1},
			{Name: "second", Available: true, Geometry: output.Geometry{X: 1600, Y: 0, W: 1600, H: 900}, ResolutionW: 1600, ResolutionH: 900, Scale: 1},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := &coordinateTestInput{Inputter: &pftest.Inputter{}, space: input.CoordinateSpaceInfo{Kind: input.CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1}}
			outs := &outputTestLister{outs: tc.outs}
			_, stderr, code := captureRunIO(t, []string{"input", "click-screen", "--x", "700", "--y", "400"}, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					return pftest.NewWithOutputs(nil, in, nil, outs, nil), nil
				}
			})
			if code == 0 || len(in.Calls) != 0 {
				t.Fatalf("unsupported output mosaic was accepted: code=%d stderr=%q calls=%v", code, stderr, in.Calls)
			}
		})
	}
}

func TestScreenPixelActionFailsClosedAfterHotplugChangesTopology(t *testing.T) {
	in := &coordinateTestInput{Inputter: &pftest.Inputter{}, space: input.CoordinateSpaceInfo{Kind: input.CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1}}
	outs := &outputTestLister{outs: []output.Info{
		{Name: "first", Available: true, Geometry: output.Geometry{W: 1600, H: 900}, ResolutionW: 1920, ResolutionH: 1080, ScaleNumerator: 6, ScaleDenominator: 5},
	}}
	run := func() (string, int) {
		_, stderr, code := captureRunIO(t, []string{"input", "click-screen", "--x", "960", "--y", "540"}, func(*cliConfig) sessionOpener {
			return func(context.Context) (*perfuncted.Session, error) {
				return pftest.NewWithOutputs(nil, in, nil, outs, nil), nil
			}
		})
		return stderr, code
	}
	if stderr, code := run(); code != 0 {
		t.Fatalf("single monitor should be supported: code=%d stderr=%q", code, stderr)
	}
	baseline := len(in.Calls)
	if baseline != 1 || !strings.Contains(in.Calls[0], "800,450") {
		t.Fatalf("wrong single-monitor mapping: %v", in.Calls)
	}
	outs.outs = append(outs.outs, output.Info{
		Name: "hotplugged", Available: true, Geometry: output.Geometry{X: -1920, Y: 0, W: 1920, H: 1080},
		ResolutionW: 1920, ResolutionH: 1080, Scale: 1,
	})
	if stderr, code := run(); code == 0 {
		t.Fatalf("hotplugged display should invalidate screen coordinate conversion: %q", stderr)
	}
	if len(in.Calls) != baseline {
		t.Fatalf("invalid post-hotplug action produced input: %v", in.Calls)
	}
	outs.outs = outs.outs[:1]
	if stderr, code := run(); code != 0 {
		t.Fatalf("restored single output must remap safely: %q", stderr)
	}
	if len(in.Calls) != baseline+1 {
		t.Fatalf("recovered output did not accept valid mapped click: %v", in.Calls)
	}
}

// The CLI's opaque predicate must not mistake a fully transparent frame for
// evidence of a visible window or an authorized nontransparent capture.
func TestOpaqueScreenPredicateRejectsTransparentFrames(t *testing.T) {
	predicate, err := screenPredicate(" opaque ")
	if err != nil {
		t.Fatal(err)
	}
	transparent := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	transparent.SetNRGBA(1, 1, color.NRGBA{R: 255, G: 90, B: 50, A: 0})
	if predicate(context.Background(), transparent) {
		t.Fatal("transparent pixels satisfy opaque predicate")
	}
	transparent.SetNRGBA(1, 1, color.NRGBA{R: 255, A: 1})
	if !predicate(context.Background(), transparent) {
		t.Fatal("nonzero alpha pixel not recognized")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if predicate(ctx, transparent) {
		t.Fatal("canceled predicate returned success")
	}
}

type deniedStableWindowManager struct {
	*pftest.Manager
	rejection error
	attempted []string
}

func (m *deniedStableWindowManager) reject(name string) error {
	m.attempted = append(m.attempted, name)
	return m.rejection
}
func (m *deniedStableWindowManager) ActivateByID(context.Context, string) error {
	return m.reject("activate")
}
func (m *deniedStableWindowManager) MoveByID(context.Context, string, int, int) error {
	return m.reject("move")
}
func (m *deniedStableWindowManager) ResizeByID(context.Context, string, int, int) error {
	return m.reject("resize")
}
func (m *deniedStableWindowManager) FullscreenByID(context.Context, string) error {
	return m.reject("fullscreen")
}
func (m *deniedStableWindowManager) UnfullscreenByID(context.Context, string) error {
	return m.reject("unfullscreen")
}
func (m *deniedStableWindowManager) CloseWindowByID(context.Context, string) error {
	return m.reject("close")
}
func (m *deniedStableWindowManager) MinimizeByID(context.Context, string) error {
	return m.reject("minimize")
}
func (m *deniedStableWindowManager) MaximizeByID(context.Context, string) error {
	return m.reject("maximize")
}

// Normal discovery must resolve the exact window before the rejecting backend
// is invoked. An operation rejected by the compositor must never print a
// success message (or attempt an alternate title-based operation).
func TestWindowCommandsReportRealActionRejectionWithoutFalseSuccess(t *testing.T) {
	failure := errors.New("synthetic compositor refused window mutation")
	cases := []struct {
		name   string
		args   []string
		action string
	}{
		{"activate", []string{"window", "activate", "Editor"}, "activate"},
		{"activate-id", []string{"window", "activate-id", "private-id"}, "activate"},
		{"move", []string{"window", "move", "--title", "Editor", "--x", "12", "--y", "23"}, "move"},
		{"resize", []string{"window", "resize", "--title", "Editor", "--w", "101", "--h", "102"}, "resize"},
		{"fullscreen", []string{"window", "fullscreen", "Editor"}, "fullscreen"},
		{"unfullscreen", []string{"window", "unfullscreen", "Editor"}, "unfullscreen"},
		{"close", []string{"window", "close", "Editor"}, "close"},
		{"close-id", []string{"window", "close-id", "private-id"}, "close"},
		{"minimize", []string{"window", "minimize", "Editor"}, "minimize"},
		{"maximize", []string{"window", "maximize", "Editor"}, "maximize"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manager := &deniedStableWindowManager{Manager: &pftest.Manager{Lists: [][]window.Info{{{NativeID: "private-id", Title: "Editor", X: 10, Y: 20, W: 200, H: 100}}}}, rejection: failure}
			opens := 0
			cmd := newRootCmd(func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opens++
					return pftest.New(nil, nil, manager, nil), nil
				}
			})
			var out, stderr bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&stderr)
			cmd.SetArgs(tc.args)
			err := cmd.ExecuteContext(context.Background())
			if !errors.Is(err, failure) {
				t.Fatalf("provider rejection lost for %v: %v", tc.args, err)
			}
			if opens != 1 {
				t.Fatalf("unexpected session count %d", opens)
			}
			if len(manager.attempted) != 1 || manager.attempted[0] != tc.action {
				t.Fatalf("wrong backend operation: %v expected %s", manager.attempted, tc.action)
			}
			if out.Len() != 0 {
				t.Fatalf("rejected operation reported success: %q", out.String())
			}
		})
	}
}

// Window reads and mutations may have completed successfully, but a failed
// output sink must never be acknowledged as a delivered result. Test both
// encoding modes and multi-line records, including failure after a first line.
func TestWindowCLIPropagatesOutputDisconnection(t *testing.T) {
	denied := errors.New("synthetic window output disconnected")
	info := window.Info{NativeID: "editor-id", ID: 55, Title: "Editor", X: 10, Y: 20, W: 300, H: 220, PID: 42}
	for _, tc := range []struct {
		name                   string
		args                   []string
		failAt, expectedWrites int
	}{
		{"plain list", []string{"window", "list"}, 1, 1},
		{"JSON list", []string{"window", "list", "--output=json"}, 1, 1},
		{"active title", []string{"window", "active"}, 1, 1},
		{"geometry plain", []string{"window", "get-geometry", "Editor"}, 1, 1},
		{"geometry JSON", []string{"window", "get-geometry", "Editor", "--output=json"}, 1, 1},
		{"find by title first line", []string{"window", "find-by-title", "Editor"}, 1, 1},
		{"find by title second line", []string{"window", "find-by-title", "Editor"}, 2, 2},
		{"find by title third line", []string{"window", "find-by-title", "Editor"}, 3, 3},
		{"activate by title", []string{"window", "activate", "Editor", "--verify=false"}, 1, 1},
		{"activate by ID", []string{"window", "activate-id", "editor-id", "--verify=false"}, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := &pftest.Manager{Lists: [][]window.Info{{info}}, Titles: []string{"Editor"}}
			opens := 0
			root := newRootCmd(func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opens++
					return pftest.New(nil, nil, manager, nil), nil
				}
			})
			root.SilenceUsage = true
			root.SilenceErrors = true
			w := &failWindowNthWrite{failAt: tc.failAt, failure: denied}
			var stderr bytes.Buffer
			root.SetOut(w)
			root.SetErr(&stderr)
			root.SetArgs(tc.args)
			err := root.ExecuteContext(context.Background())
			if !errors.Is(err, denied) || opens != 1 || w.count != tc.expectedWrites {
				t.Fatalf("failed output accepted args=%v err=%v opens=%d writes=%d expected=%d accepted=%q", tc.args, err, opens, w.count, tc.expectedWrites, w.sent.String())
			}
		})
	}
}

// Invalid local window selectors, timeouts and output formats must not open
// a desktop session; session acquisition can prompt for portal authorization.
func TestWindowCLIInvalidLocalArgumentsDoNotOpenDesktopSession(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"unknown list output", []string{"window", "list", "--output", "toml"}},
		{"invalid find match", []string{"window", "find", "unknown-field=value"}},
		{"invalid wait-for match", []string{"window", "wait-for", "unknown-field=value"}},
		{"invalid wait-close match", []string{"window", "wait-close", "unknown-field=value"}},
		{"invalid wait-for poll", []string{"window", "wait-for", "title=Editor", "--poll", "wrong"}},
		{"invalid wait-for timeout", []string{"window", "wait-for", "title=Editor", "--timeout", "wrong"}},
		{"invalid wait-close poll", []string{"window", "wait-close", "title=Editor", "--poll", "wrong"}},
		{"invalid wait-close timeout", []string{"window", "wait-close", "title=Editor", "--timeout", "wrong"}},
		{"invalid move x", []string{"window", "move", "--title", "Editor", "--x", "wrong"}},
		{"invalid move y", []string{"window", "move", "--title", "Editor", "--y", "wrong"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opened := 0
			out, errOut, code := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opened++
					return nil, errors.New("unexpected access to desktop session")
				}
			})
			if code == 0 || opened != 0 || out != "" || strings.Contains(errOut, "unexpected access to desktop session") {
				t.Fatalf("invalid %v accessed %d sessions: code=%d stdout=%q stderr=%q", tc.args, opened, code, out, errOut)
			}
		})
	}
}

// Exercise public command dispatch through a deterministic native-ID manager.
// Output must describe the actual selected window, and invalid requests must
// not pass through as successful control operations.
func TestWindowExtendedCLIBehaviorsAndInvalidInputFailClosed(t *testing.T) {
	info := window.Info{NativeID: "id-editor", ID: 27, Title: "Editor - file.txt", AppID: "org.editor", PID: 445, X: 12, Y: 21, W: 640, H: 480, Active: true}
	cases := []struct {
		name     string
		args     []string
		windows  []window.Info
		titles   []string
		expected string
		ok       bool
	}{
		{"plain list", []string{"window", "list"}, []window.Info{info}, nil, "Editor - file.txt", true},
		{"JSON list", []string{"window", "list", "--output=json"}, []window.Info{info}, nil, "\"NativeID\":\"id-editor\"", true},
		{"empty list", []string{"window", "list"}, nil, nil, "", true},
		{"invalid output format", []string{"window", "list", "--output=csv"}, []window.Info{info}, nil, "unknown output format", false},
		{"active title", []string{"window", "active"}, []window.Info{info}, []string{"Editor - file.txt"}, "Editor - file.txt", true},
		{"geometry plain", []string{"window", "get-geometry", "Editor"}, []window.Info{info}, nil, "12,21,652,501", true},
		{"geometry JSON", []string{"window", "get-geometry", "Editor", "--output=json"}, []window.Info{info}, nil, "\"w\":640", true},
		{"geometry wrong format", []string{"window", "get-geometry", "Editor", "--output=xml"}, []window.Info{info}, nil, "invalid --output", false},
		{"find by title", []string{"window", "find-by-title", "Editor"}, []window.Info{info}, nil, "pid=445", true},
		{"visible", []string{"window", "is-visible", "Editor"}, []window.Info{info}, nil, "true", true},
		{"not visible", []string{"window", "is-visible", "Absent"}, []window.Info{info}, nil, "false", true},
		{"unknown stable ID", []string{"window", "activate-id", "unknown"}, []window.Info{info}, nil, "not found", false},
		{"activate by stable ID", []string{"window", "activate-id", "id-editor", "--verify=false"}, []window.Info{info}, nil, "activation requested", true},
		{"close by stable ID", []string{"window", "close-id", "id-editor"}, []window.Info{info}, nil, "close requested", true},
		{"close by title", []string{"window", "close", "Editor"}, []window.Info{info}, nil, "closed: Editor", true},
		{"minimize by title", []string{"window", "minimize", "Editor"}, []window.Info{info}, nil, "minimized: Editor", true},
		{"maximize by title", []string{"window", "maximize", "Editor"}, []window.Info{info}, nil, "maximized: Editor", true},
		{"fullscreen by title", []string{"window", "fullscreen", "Editor"}, []window.Info{info}, nil, "fullscreen: Editor", true},
		{"unfullscreen by title", []string{"window", "unfullscreen", "Editor"}, []window.Info{info}, nil, "unfullscreen: Editor", true},
		{"resize invalid dimension", []string{"window", "resize", "--title=Editor", "--w=0", "--h=300"}, []window.Info{info}, nil, "invalid", false},
		{"resize normal", []string{"window", "resize", "--title=Editor", "--w=320", "--h=240"}, []window.Info{info}, nil, "resized", true},
		{"move preserve x", []string{"window", "move", "--title=Editor", "--x=keep", "--y=41"}, []window.Info{info}, nil, "12,41", true},
		{"move preserve y", []string{"window", "move", "--title=Editor", "--x=-11", "--y=keep"}, []window.Info{info}, nil, "-11,21", true},
		{"move bad x", []string{"window", "move", "--title=Editor", "--x=NaN", "--y=2"}, []window.Info{info}, nil, "invalid", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &pftest.Manager{Lists: [][]window.Info{tc.windows}, Titles: tc.titles}
			out, errOut, code := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, nil, m, nil), nil }
			})
			if tc.ok && (code != 0 || !strings.Contains(out, tc.expected)) {
				t.Fatalf("%v code=%d output=%q err=%q expected=%q", tc.args, code, out, errOut, tc.expected)
			}
			if !tc.ok && (code == 0 || !strings.Contains(strings.ToLower(errOut), strings.ToLower(tc.expected))) {
				t.Fatalf("invalid request %v accepted or wrong error: code=%d output=%q err=%q expected=%q", tc.args, code, out, errOut, tc.expected)
			}
		})
	}
}

// The find/wait CLI commands must report a fresh concrete window, not a
// successful parse or an unverified cached title. This exercises the same
// WindowBundle that resolves native window IDs for interactive operations.
func TestWindowFindAndWaitCLISyntheticStateAndTimeouts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		matches []window.Info
		want    string
		success bool
	}{
		{"find by title", []string{"window", "find", "title=Editor"}, []window.Info{{NativeID: "w-100", ID: 100, Title: "Editor", AppID: "org.editor", Active: true}}, "Editor", true},
		{"find by ID", []string{"window", "find", "id=100"}, []window.Info{{NativeID: "w-100", ID: 100, Title: "Editor", AppID: "org.editor"}}, "Editor", true},
		{"find missing", []string{"window", "find", "title=Missing"}, []window.Info{{NativeID: "w-100", Title: "Editor"}}, "not found", false},
		{"wait existing", []string{"window", "wait-for", "title=Editor", "--poll", "1ms", "--timeout", "150ms"}, []window.Info{{NativeID: "w-100", Title: "Editor"}}, "Editor", true},
		{"wait missing expires", []string{"window", "wait-for", "title=Missing", "--poll", "1ms", "--timeout", "20ms"}, nil, "deadline exceeded", false},
		{"wait close missing is immediate", []string{"window", "wait-close", "title=Missing", "--poll", "1ms", "--timeout", "150ms"}, nil, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mgr := &pftest.Manager{Lists: [][]window.Info{tc.matches}}
			out, errOut, code := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, nil, mgr, nil), nil }
			})
			if tc.success {
				if code != 0 || !strings.Contains(out, tc.want) {
					t.Fatalf("CLI %v did not confirm window state: code=%d output=%q err=%q", tc.args, code, out, errOut)
				}
			} else if code == 0 || !strings.Contains(strings.ToLower(errOut), strings.ToLower(tc.want)) {
				t.Fatalf("CLI %v fabricated successful state: code=%d output=%q err=%q", tc.args, code, out, errOut)
			}
		})
	}
}

func TestWindowVerificationRejectsInvalidArguments(t *testing.T) {
	if err := waitWindowActive(context.Background(), nil, 0, time.Millisecond); err == nil {
		t.Fatal("activation verification accepted a nonpositive timeout")
	}
	if err := waitWindowGone(context.Background(), nil, time.Second, time.Millisecond); err == nil {
		t.Fatal("disappearance verification accepted an empty ID")
	}
}

func TestWindowVerificationPropagatesBackendErrors(t *testing.T) {
	wantErr := errors.New("synthetic window backend failure")
	manager := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "editor", Title: "Editor"}}}}
	session := pftest.New(nil, nil, manager, nil)
	t.Cleanup(func() { _ = session.Close() })

	target, err := session.Windows().WithContext(context.Background()).Find(window.Match{TitleExact: "Editor"})
	if err != nil {
		t.Fatalf("find verification target: %v", err)
	}

	manager.Err = wantErr
	if err := waitWindowActive(context.Background(), target, time.Second, time.Millisecond); !errors.Is(err, wantErr) {
		t.Fatalf("activation verification error = %v, want %v", err, wantErr)
	}
	if err := waitWindowGone(context.Background(), target, time.Second, time.Millisecond); !errors.Is(err, wantErr) {
		t.Fatalf("disappearance verification error = %v, want %v", err, wantErr)
	}
}

// A native KWin tile request is not success until it can be confirmed by a
// fresh backend read. Missing targets and permission failures are equally
// terminal: never report a tile or float operation that did not complete.
func TestKDETileAndFloatCommandsPreserveLateFailures(t *testing.T) {
	denied := errors.New("synthetic authorization rejected")
	stale := errors.New("synthetic window info read failed after mutation")
	for _, tc := range []struct {
		name          string
		args          []string
		openerFailure error
		missing       bool
		stale         bool
		want          string
		action        string
	}{
		{name: "tile authorization", args: []string{"window", "tile", "Editor", "--x=13", "--y=17"}, openerFailure: denied, want: denied.Error()},
		{name: "float authorization", args: []string{"window", "float", "Editor"}, openerFailure: denied, want: denied.Error()},
		{name: "tile missing target", args: []string{"window", "tile", "Missing", "--x=13", "--y=17"}, missing: true, want: "not found"},
		{name: "tile verification read fails", args: []string{"window", "tile", "Editor", "--x=13", "--y=17"}, stale: true, want: "native window tiling not confirmed", action: "tile:uuid-a"},
		{name: "float verification read fails", args: []string{"window", "float", "Editor"}, stale: true, want: "native window floating not confirmed", action: "float:uuid-a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newKDEWindowState()
			if tc.stale {
				m.readErr = stale
			}
			opens := 0
			stdout, stderr, code := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opens++
					if tc.openerFailure != nil {
						return nil, tc.openerFailure
					}
					return perfuncted.NewSessionForTesting(nil, nil, m, nil, nil), nil
				}
			})
			if code == 0 || stdout != "" || !strings.Contains(stderr, tc.want) || opens != 1 || m.action != tc.action {
				t.Fatalf("rejected tile/float code=%d stdout=%q stderr=%q opens=%d action=%q (want %q, action %q)", code, stdout, stderr, opens, m.action, tc.want, tc.action)
			}
			if tc.stale && !strings.Contains(stderr, stale.Error()) {
				t.Fatalf("verification error lost: %q", stderr)
			}
		})
	}
}

// A stale compositor confirmation arriving with caller cancellation must not
// be reported as a successful KWin mutation verification.
func TestKDEStateWaiterRejectsSuccessAfterCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	err := waitForKWinState(ctx, func(context.Context) (bool, error) {
		calls++
		cancel()
		return true, nil
	})
	if calls != 1 {
		t.Fatalf("probe count=%d, want 1", calls)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("late cancellation acknowledged as verified success: %v", err)
	}
}

// A successful probe cannot override its own simultaneous failure.
func TestKDEStateWaiterPreservesProbeErrorWhenMarkedReady(t *testing.T) {
	sentinel := errors.New("compositor disconnected during confirmation")
	err := waitForKWinState(context.Background(), func(context.Context) (bool, error) { return true, sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("lost compositor failure: %v", err)
	}
}

// A backend failure and caller revocation are independent causes. The
// verification API must retain both rather than silently losing cancellation.
func TestKDEStateWaiterRetainsCancellationWithConcurrentProbeError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backendErr := errors.New("synthetic compositor query failed")
	err := waitForKWinState(ctx, func(context.Context) (bool, error) {
		cancel()
		return false, backendErr
	})
	if !errors.Is(err, context.Canceled) || !errors.Is(err, backendErr) {
		t.Fatalf("lost cancellation or compositor error: %v", err)
	}
}

func TestParseWindowMatchArgsPreservesMultiwordCLISelection(t *testing.T) {
	got, err := parseWindowMatchArgs([]string{"title=Project Notes", "app_id=org.example.Editor", "visible", "active=false"})
	if err != nil {
		t.Fatal(err)
	}
	if got.TitleExact != "Project Notes" || got.AppID != "org.example.Editor" || !got.VisibleOnly || got.Active == nil || *got.Active {
		t.Fatalf("window selection lost CLI fields: %+v", got)
	}
	empty, err := parseWindowMatchArgs(nil)
	if err != nil || empty.TitleExact != "" || empty.Active != nil || empty.VisibleOnly {
		t.Fatalf("empty match: %+v %v", empty, err)
	}
}

func TestParseWindowMatchArgsRejectsInvalidPIDWithoutMatchingEverything(t *testing.T) {
	got, err := parseWindowMatchArgs([]string{"pid=not-a-number"})
	if err == nil || !strings.Contains(err.Error(), "parse pid") {
		t.Fatalf("malformed PID accepted: %+v %v", got, err)
	}
	if got.PID != nil || got.TitleContains != "" || got.TitleExact != "" {
		t.Fatalf("error returned partial match: %+v", got)
	}
}

func TestParseWindowMatchArgsPreservesQuotedPositionalTitle(t *testing.T) {
	got, err := parseWindowMatchArgs([]string{"Quarterly Planning", "active"})
	if err != nil {
		t.Fatalf("quoted positional title should be one token: %v", err)
	}
	if got.TitleContains != "Quarterly Planning" || got.Active == nil || !*got.Active {
		t.Fatalf("quoted title match altered: %+v", got)
	}
}

func TestCLIStableIDLeadingZeroDecimalDoesNotSelectWrongWindow(t *testing.T) {
	m := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "8", ID: 8, Title: "wrong"}, {NativeID: "10", ID: 10, Title: "correct"}}}}
	pf := pftest.New(nil, nil, m, nil)
	defer pf.Close()
	target, err := findWindowByStableID(context.Background(), pf.Windows(), "010")
	if err != nil {
		t.Fatal(err)
	}
	if id := target.CachedInfo().ID; id != 10 {
		t.Fatalf("decimal ID 010 selected window %d, want 10", id)
	}
}

func TestCLINumericHashLeadingZeroDecimal(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want uint32
	}{{"010", 10}, {"08", 8}, {"0x10", 16}, {"abcdef", 0xabcdef}} {
		got, err := parseHash(tc.raw)
		if err != nil || got != tc.want {
			t.Errorf("parseHash(%q) = %d, %v; want %d", tc.raw, got, err, tc.want)
		}
	}
}

func TestCLIStableIDPrefersExactOpaqueHandleOverNumericAlias(t *testing.T) {
	m := &pftest.Manager{Lists: [][]window.Info{{
		{NativeID: "10", ID: 10, Title: "numeric alias"},
		{NativeID: "010", Title: "exact opaque handle"},
	}}}
	pf := pftest.New(nil, nil, m, nil)
	defer pf.Close()
	target, err := findWindowByStableID(context.Background(), pf.Windows(), "010")
	if err != nil {
		t.Fatal(err)
	}
	if id := target.ID().String(); id != "010" {
		t.Fatalf("selected native ID %q, want exact 010", id)
	}
}

// Run actual Cobra leaf commands against a deliberately denied synthetic
// desktop. No operation is allowed to report success or mask an authorization
// failure with a secondary backend error.
func TestWindowAndScreenCommandsPropagateDesktopAuthorizationDenial(t *testing.T) {
	denied := errors.New("synthetic desktop authorization denied")
	cases := []struct {
		name string
		args []string
	}{
		{"window list", []string{"window", "list"}},
		{"window list JSON", []string{"window", "list", "--output", "json"}},
		{"window activate", []string{"window", "activate", "Editor"}},
		{"window activate by id", []string{"window", "activate-id", "window-id"}},
		{"window active title", []string{"window", "active"}},
		{"window move", []string{"window", "move", "--title", "Editor", "--x", "11", "--y", "20"}},
		{"window resize", []string{"window", "resize", "--title", "Editor", "--w", "300", "--h", "200"}},
		{"window fullscreen", []string{"window", "fullscreen", "Editor"}},
		{"window unfullscreen", []string{"window", "unfullscreen", "Editor"}},
		{"window find", []string{"window", "find", "title=Editor"}},
		{"window wait", []string{"window", "wait-for", "title=Editor"}},
		{"window wait close", []string{"window", "wait-close", "title=Editor"}},
		{"window geometry", []string{"window", "get-geometry", "Editor"}},
		{"window by title", []string{"window", "find-by-title", "Editor"}},
		{"window visible", []string{"window", "is-visible", "Editor"}},
		{"window watch", []string{"window", "watch"}},
		{"window close", []string{"window", "close", "Editor"}},
		{"window close id", []string{"window", "close-id", "window-id"}},
		{"window minimize", []string{"window", "minimize", "Editor"}},
		{"screen grab", []string{"screen", "grab", "--rect", "0,0,2,2"}},
		{"screen hash", []string{"screen", "hash", "--rect", "0,0,2,2"}},
		{"screen pixel", []string{"screen", "pixel", "--x", "1", "--y", "1"}},
		{"screen all pixels", []string{"screen", "get-all-pixels"}},
		{"screen grab region", []string{"screen", "grab-region", "--rect", "0,0,2,2"}},
		{"screen watch", []string{"screen", "watch", "--duration", "1ms"}},
		{"screen resolution", []string{"screen", "resolution"}},
		{"screen wait predicate", []string{"screen", "wait-for-fn", "--timeout", "1ms"}},
		{"screen wait settle", []string{"screen", "wait-for-settle", "--timeout", "1ms"}},
		{"screen pixel batch", []string{"screen", "get-multiple-pixels", "--points", "1,2;3,4"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opened := 0
			cmd := newRootCmd(func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { opened++; return nil, denied }
			})
			var stdout, stderr bytes.Buffer
			cmd.SetArgs(tc.args)
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			err := cmd.ExecuteContext(context.Background())
			if !errors.Is(err, denied) {
				t.Fatalf("denial not propagated for %v: err=%v stdout=%q stderr=%q", tc.args, err, stdout.String(), stderr.String())
			}
			if opened != 1 {
				t.Fatalf("expected exactly one session authorization for %v, got %d", tc.args, opened)
			}
			if stdout.Len() != 0 {
				t.Fatalf("denied command published partial success: %q", stdout.String())
			}
		})
	}
}

func TestWindowWaitForZeroPollUsesSafeDefaultAndFindMissingRemainsError(t *testing.T) {
	backend := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "one", Title: "Editor", X: 1, Y: 2, W: 100, H: 80}}}}
	for _, tc := range []struct {
		name      string
		args      []string
		want      string
		wantError bool
	}{
		{name: "zero interval", args: []string{"wait-for", "title=Editor", "--poll=0s", "--timeout=200ms"}, want: "Editor"},
		{name: "find missing", args: []string{"find-by-title", "Missing"}, want: "not found", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := windowCmd(func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, nil, backend, nil), nil }, nil)
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			cmd.SetArgs(tc.args)
			var stdout bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(io.Discard)
			err := cmd.ExecuteContext(context.Background())
			if tc.wantError {
				if !errors.Is(err, window.ErrWindowNotFound) || stdout.Len() != 0 {
					t.Fatalf("missing match: err=%v stdout=%q", err, stdout.String())
				}
			} else if err != nil || !strings.Contains(stdout.String(), tc.want) {
				t.Fatalf("zero interval wait: err=%v stdout=%q", err, stdout.String())
			}
		})
	}
}

func TestScreenWatchZeroPollUsesBoundedDefault(t *testing.T) {
	sc := &pftest.Screenshotter{Frames: []image.Image{pftest.SolidImage(2, 2, color.RGBA{A: 255})}}
	opens := 0
	cmd := screenCmd(func(context.Context) (*perfuncted.Session, error) { opens++; return pftest.New(sc, nil, nil, nil), nil })
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"watch", "--rect=0,0,2,2", "--poll=0s", "--duration=15ms", "--output=json"})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if opens != 1 || !strings.Contains(stdout.String(), `"event":"initial"`) {
		t.Fatalf("zero poll watch: opens=%d output=%q", opens, stdout.String())
	}
}

func TestScreenshotTerminalPreflightPropagatesClosedFileStatError(t *testing.T) {
	file, err := os.Create(filepath.Join(t.TempDir(), "closed-screenshot-output"))
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := refuseBinaryTerminal(file); err == nil || !strings.Contains(err.Error(), "inspect screenshot output") {
		t.Fatalf("closed screenshot file accepted: %v", err)
	}
}

// An activation event is not an acknowledgement of focus. Only an observed
// transition of the selected stable window may authorize a verified success.
func TestWindowCLIReportsVerifiedFocusOnlyAfterSelectedWindowBecomesActive(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"title", []string{"window", "activate", "Editor", "--verify-timeout=250ms", "--verify-poll=1ms"}},
		{"stable ID", []string{"window", "activate-id", "editor", "--verify-timeout=250ms", "--verify-poll=1ms"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inactive := window.Info{NativeID: "editor", Title: "Editor", Active: false}
			active := inactive
			active.Active = true
			mgr := &pftest.Manager{Lists: [][]window.Info{{inactive}, {inactive}, {active}}}
			out, stderr, code := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					return pftest.New(nil, nil, mgr, nil), nil
				}
			})
			if code != 0 || !strings.Contains(out, "activation verified: ") || len(mgr.Activated) != 1 || mgr.Activated[0] != "editor" {
				t.Fatalf("selected window focus not verified: code=%d stdout=%q stderr=%q activated=%q", code, out, stderr, mgr.Activated)
			}
		})
	}
}

// A close request alone is insufficient: observe the stable target disappear
// from an independently refreshed window listing before reporting success.
func TestWindowCLIReportsVerifiedCloseOnlyAfterTargetDisappears(t *testing.T) {
	info := window.Info{NativeID: "dialog", Title: "Save dialog"}
	mgr := &pftest.Manager{Lists: [][]window.Info{{info}, {info}, {}}}
	out, stderr, code := captureRunIO(t, []string{"window", "close-id", "dialog", "--verify", "--verify-timeout=250ms", "--verify-poll=1ms"}, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) {
			return pftest.New(nil, nil, mgr, nil), nil
		}
	})
	if code != 0 || !strings.Contains(out, "close verified: dialog") {
		t.Fatalf("disappearance verification not acknowledged: code=%d stdout=%q stderr=%q", code, out, stderr)
	}
}

// A discovered window may be destroyed before the CLI refreshes its details.
// Discovery is not permission to report stale geometry or claim success.
type disappearingInfoWindowManager struct {
	*pftest.Manager
	failure   error
	infoCalls int
}

//nolint:unparam // This backend deliberately always fails refresh after a successful discovery.
func (m *disappearingInfoWindowManager) InfoByID(context.Context, string) (window.Info, error) {
	m.infoCalls++
	return window.Info{}, m.failure
}

func TestWindowCommandsRejectStaleWindowInfoBeforeOutputOrMutation(t *testing.T) {
	failure := errors.New("synthetic window disappeared before info refresh")
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"list matches", []string{"window", "find", "title:Editor"}},
		{"move using existing position", []string{"window", "move", "--title", "Editor", "--x", "-", "--y", "13"}},
		{"get geometry", []string{"window", "get-geometry", "Editor"}},
		{"find title", []string{"window", "find-by-title", "Editor"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := &disappearingInfoWindowManager{
				Manager: &pftest.Manager{Lists: [][]window.Info{{{NativeID: "private-window", Title: "Editor", X: 10, Y: 20, W: 640, H: 480}}}}, failure: failure,
			}
			cmd := newRootCmd(func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, nil, manager, nil), nil }
			})
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			cmd.SetArgs(tc.args)
			err := cmd.ExecuteContext(context.Background())
			if !errors.Is(err, failure) {
				t.Fatalf("stale info error missing: %v", err)
			}
			if manager.infoCalls == 0 {
				t.Fatalf("command did not refresh stale window before returning")
			}
			if stdout.Len() != 0 {
				t.Fatalf("command published stale window info: %q", stdout.String())
			}
		})
	}
}

// Exercise actual Cobra command paths against a session with a deterministic,
// concrete window backend. Verify the resulting request/output, not only that
// command argument parsing succeeds.
func TestSyntheticWindowCommandsAcrossLifecycle(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		output    string
		activated string
	}{
		{"list plain", []string{"window", "list"}, "Editor", ""},
		{"list json", []string{"window", "list", "--output", "json"}, `"Title":"Editor"`, ""},
		{"active title", []string{"window", "active"}, "Editor", ""},
		{"activate", []string{"window", "activate", "Editor"}, "activation verified", "desktop-window"},
		{"move keep x", []string{"window", "move", "--title", "Editor", "--y", "350"}, "to 100,350", ""},
		{"move keep y", []string{"window", "move", "--title", "Editor", "--x", "205"}, "to 205,200", ""},
		{"resize", []string{"window", "resize", "--title", "Editor", "--w", "320", "--h", "180"}, "320x180", ""},
		{"fullscreen", []string{"window", "fullscreen", "Editor"}, "fullscreen: Editor", ""},
		{"unfullscreen", []string{"window", "unfullscreen", "Editor"}, "unfullscreen: Editor", ""},
		{"get geometry", []string{"window", "get-geometry", "Editor"}, "100,200,740,680", ""},
		{"get geometry json", []string{"window", "get-geometry", "Editor", "--output", "json"}, `"x":100`, ""},
		{"find by title", []string{"window", "find-by-title", "Editor"}, "x=100 y=200", ""},
		{"visible", []string{"window", "is-visible", "Editor"}, "true", ""},
		{"not visible", []string{"window", "is-visible", "Absent"}, "false", ""},
		{"close", []string{"window", "close", "Editor"}, "closed: Editor", ""},
		{"minimize", []string{"window", "minimize", "Editor"}, "minimized: Editor", ""},
		{"maximize", []string{"window", "maximize", "Editor"}, "maximized: Editor", ""},
		{"activate stable ID", []string{"window", "activate-id", "desktop-window"}, "activation verified", "desktop-window"},
		{"close stable ID", []string{"window", "close-id", "desktop-window"}, "close requested", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr := &pftest.Manager{Titles: []string{"Editor"}, Lists: [][]window.Info{{{NativeID: "desktop-window", ID: 77, Title: "Editor", AppID: "editor", X: 100, Y: 200, W: 640, H: 480, Active: true}}}}
			out, errOut, code := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, nil, mgr, nil), nil }
			})
			if code != 0 || !strings.Contains(out, tc.output) {
				t.Fatalf("CLI %v returned %d stdout=%q stderr=%q, want %q", tc.args, code, out, errOut, tc.output)
			}
			if tc.activated != "" && (len(mgr.Activated) != 1 || mgr.Activated[0] != tc.activated) {
				t.Fatalf("wrong activated ID: %v", mgr.Activated)
			}
		})
	}
}

func TestSyntheticWindowCommandsFailClosedOnBackendErrors(t *testing.T) {
	for _, cmd := range [][]string{
		{"window", "list"}, {"window", "active"}, {"window", "activate", "Editor"},
		{"window", "move", "--title", "Editor", "--x", "10", "--y", "20"},
		{"window", "resize", "--title", "Editor"}, {"window", "fullscreen", "Editor"},
		{"window", "unfullscreen", "Editor"}, {"window", "get-geometry", "Editor"},
		{"window", "is-visible", "Editor"}, {"window", "close", "Editor"},
		{"window", "minimize", "Editor"}, {"window", "maximize", "Editor"},
	} {
		t.Run(strings.Join(cmd, "_"), func(t *testing.T) {
			mgr := &pftest.Manager{Err: errors.New("synthetic compositor disconnected")}
			out, errOut, code := captureRunIO(t, cmd, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, nil, mgr, nil), nil }
			})
			if code == 0 || strings.Contains(out, "requested") || strings.Contains(out, "maximized") || !strings.Contains(errOut, "synthetic compositor disconnected") {
				t.Fatalf("unavailable backend was not rejected: code=%d out=%q err=%q", code, out, errOut)
			}
		})
	}
}

func TestSyntheticInputCommandsHaveRealSideEffects(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"move", []string{"input", "move", "--x", "17", "--y", "24"}, "move:17,24"},
		{"click", []string{"input", "click", "--x", "17", "--y", "24", "--repeat", "3"}, "click:17,24"},
		{"double click", []string{"input", "double-click", "--x", "17", "--y", "24"}, "mousedown"},
		{"drag", []string{"input", "drag-and-drop", "--x1", "1", "--y1", "2", "--x2", "50", "--y2", "60"}, "move:50,60"},
		{"center", []string{"input", "click-center", "--rect", "10,20,30,40"}, "click:20,30"},
		{"type", []string{"input", "type", "literal text"}, "type-literal:literal text"},
		{"keydown", []string{"input", "keydown", "a"}, "down:a"},
		{"keyup", []string{"input", "keyup", "a"}, "up:a"},
		{"scroll up", []string{"input", "scroll", "up", "--clicks", "2"}, "scroll-up:2"},
		{"scroll down", []string{"input", "scroll", "down", "--clicks", "2"}, "scroll-down:2"},
		{"scroll left", []string{"input", "scroll", "left", "--clicks", "2"}, "scroll-left:2"},
		{"scroll right", []string{"input", "scroll", "right", "--clicks", "2"}, "scroll-right:2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := &pftest.Inputter{}
			out, errOut, code := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, in, nil, nil), nil }
			})
			if tc.name == "double click" && strings.Join(in.Calls, ",") != "move:17,24,mousedown,mouseup,mousedown,mouseup" {
				t.Fatalf("double click should press and release twice: %v", in.Calls)
			}
			if code != 0 || !strings.Contains(strings.Join(in.Calls, ","), tc.want) {
				t.Fatalf("command %v code=%d out=%q err=%q calls=%v expected %q", tc.args, code, out, errOut, in.Calls, tc.want)
			}
		})
	}
}

type failWindowNthWrite struct {
	count   int
	failAt  int
	failure error
	sent    bytes.Buffer
}

func (w *failWindowNthWrite) Write(p []byte) (int, error) {
	w.count++
	if w.count == w.failAt {
		return 0, w.failure
	}
	return w.sent.Write(p)
}

// A partial window listing must not continue emitting subsequent windows when
// the output consumer disconnects. This also protects stream consumers from
// interpreting a truncated list as complete.
func TestWindowListPlainStopsOnFailedSecondWindowWrite(t *testing.T) {
	denied := errors.New("synthetic broken output pipe")
	w := &failWindowNthWrite{failAt: 2, failure: denied}
	wins := []window.Info{
		{NativeID: "one", Title: "Editor"},
		{NativeID: "two", Title: "Private Dialog"},
		{NativeID: "three", Title: "Should not be written"},
	}
	err := printWindowListPlain(w, wins)
	if !errors.Is(err, denied) || w.count != 2 || !strings.Contains(w.sent.String(), "Editor") || strings.Contains(w.sent.String(), "Private Dialog") || strings.Contains(w.sent.String(), "Should not be written") {
		t.Fatalf("failed window-list sink acknowledged: err=%v writes=%d out=%q", err, w.count, w.sent.String())
	}
}

// Once a session has opened, read failures must propagate without replacing
// the backend error with an empty list, missing-window result, or false title.
func TestWindowReadCommandsRejectBackendFailuresWithoutResults(t *testing.T) {
	failure := errors.New("synthetic compositor list unavailable")
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"plain list", []string{"window", "list"}},
		{"JSON list", []string{"window", "list", "--output=json"}},
		{"active title", []string{"window", "active"}},
		{"activate by native id", []string{"window", "activate-id", "existing-id"}},
		{"close by native id", []string{"window", "close-id", "existing-id"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opens := 0
			out, stderr, code := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opens++
					return pftest.New(nil, nil, &pftest.Manager{Err: failure}, nil), nil
				}
			})
			if opens != 1 || code == 0 || out != "" || !strings.Contains(stderr, failure.Error()) {
				t.Fatalf("failed window read %v accepted: opens=%d code=%d out=%q stderr=%q", tc.args, opens, code, out, stderr)
			}
		})
	}
}

// The ID selector must reject an absent ID without consulting the compositor.
func TestFindWindowByStableIDRejectsEmptyIDBeforeListing(t *testing.T) {
	pf := pftest.New(nil, nil, &pftest.Manager{Err: errors.New("unexpected backend listing")}, nil)
	defer pf.Close()
	_, err := findWindowByStableID(context.Background(), pf.Windows(), "")
	if !errors.Is(err, window.ErrInvalidArgument) || strings.Contains(err.Error(), "unexpected backend listing") {
		t.Fatalf("empty window ID queried backend: %v", err)
	}
}

func TestFindWindowByStableIDHandlesDuplicateTitles(t *testing.T) {
	mgr := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "uuid-one", Title: "Remote Control", ID: 1}, {NativeID: "uuid-two", Title: "Remote Control", ID: 2}}}}
	pf := pftest.New(nil, nil, mgr, nil)
	defer pf.Close()
	target, err := findWindowByStableID(context.Background(), pf.Windows(), "uuid-two")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got := target.ID().String(); got != "uuid-two" {
		t.Fatalf("target=%q", got)
	}
	_, err = findWindowByStableID(context.Background(), pf.Windows(), "absent")
	if !errors.Is(err, window.ErrWindowNotFound) {
		t.Fatalf("missing id error=%v", err)
	}
}

func TestWindowIDCLICommandsWorkWithDuplicateTitles(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"activate-by-id", []string{"window", "activate-id", "uuid-two", "--verify=false"}, "activation requested"},
		{"close-by-id", []string{"window", "close-id", "uuid-two"}, "close requested"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mgr := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "uuid-one", Title: "Duplicate"}, {NativeID: "uuid-two", Title: "Duplicate"}}}}
			out, errOut, code := captureRunIO(t, tt.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, nil, mgr, nil), nil }
			})
			if code != 0 || !strings.Contains(out, tt.want) {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errOut)
			}
			if tt.name == "activate-by-id" && (len(mgr.Activated) != 1 || mgr.Activated[0] != "uuid-two") {
				t.Errorf("activated=%v", mgr.Activated)
			}
		})
	}
}

func TestClickWindowCoordinateConversion(t *testing.T) {
	info := window.Info{X: 100, Y: 200, W: 400, H: 200}
	tests := []struct {
		x, y           int
		kind           input.CoordinateSpaceKind
		scaleX, scaleY float64
		wantX, wantY   int
		wantErr        bool
	}{
		{40, 20, input.CoordinateSpaceLogical, 1, 1, 140, 220, false},
		{40, 20, input.CoordinateSpacePhysical, 1.25, 1.25, 175, 275, false},
		{400, 20, input.CoordinateSpaceLogical, 1, 1, 0, 0, true},
		{-1, 20, input.CoordinateSpaceLogical, 1, 1, 0, 0, true},
		{40, 20, "", 0, 0, 0, 0, true},
	}
	for _, tt := range tests {
		x, y, err := windowRelativePoint(info, tt.x, tt.y, input.CoordinateSpaceInfo{Kind: tt.kind, ScaleX: tt.scaleX, ScaleY: tt.scaleY})
		if (err != nil) != tt.wantErr || (!tt.wantErr && (x != tt.wantX || y != tt.wantY)) {
			t.Errorf("%+v: point=(%d,%d) err=%v", tt, x, y, err)
		}
	}
}

func TestFindWindowByStableIDAcceptsX11PrintedHexID(t *testing.T) {
	mgr := &pftest.Manager{Lists: [][]window.Info{{{ID: 0xe00017, Title: "X11 window"}, {ID: 0xe00018, Title: "other"}}}}
	pf := pftest.New(nil, nil, mgr, nil)
	defer pf.Close()
	target, err := findWindowByStableID(context.Background(), pf.Windows(), "0xe00017")
	if err != nil || target.CachedInfo().ID != 0xe00017 {
		t.Fatalf("target=%v err=%v", target, err)
	}
}

func TestWaitWindowGoneRequiresConfirmedDisappearance(t *testing.T) {
	mgr := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "dialog-uuid", Title: "File Upload"}}, {{NativeID: "dialog-uuid", Title: "File Upload"}}, {}}}
	pf := pftest.New(nil, nil, mgr, nil)
	defer pf.Close()
	target, err := findWindowByStableID(context.Background(), pf.Windows(), "dialog-uuid")
	if err != nil {
		t.Fatal(err)
	}
	if err := waitWindowGone(context.Background(), target, 100*time.Millisecond, 5*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	stuck := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "dialog-uuid", Title: "File Upload"}}}}
	pf2 := pftest.New(nil, nil, stuck, nil)
	defer pf2.Close()
	target2, err := findWindowByStableID(context.Background(), pf2.Windows(), "dialog-uuid")
	if err != nil {
		t.Fatal(err)
	}
	if err := waitWindowGone(context.Background(), target2, 15*time.Millisecond, 5*time.Millisecond); err == nil {
		t.Fatal("ignored still open file chooser")
	}
}

type deniedWindowSync struct {
	*pftest.Manager
	syncErr   error
	syncCalls int
}

func (m *deniedWindowSync) Sync(context.Context) error { m.syncCalls++; return m.syncErr }

// A successful compositor mutation is not a verified completed operation if
// the explicitly requested synchronization fails afterward. In particular,
// callers must not publish a success receipt on a failed sync barrier.
func TestWindowMutationsDoNotAcknowledgeFailedSynchronization(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"activate by title", []string{"window", "activate", "Editor"}},
		{"activate by id", []string{"window", "activate-id", "editor-id"}},
		{"move", []string{"window", "move", "--title", "Editor", "--x", "23", "--y", "24"}},
		{"resize", []string{"window", "resize", "--title", "Editor", "--w", "640", "--h", "480"}},
		{"fullscreen", []string{"window", "fullscreen", "Editor"}},
		{"unfullscreen", []string{"window", "unfullscreen", "Editor"}},
		{"close by id", []string{"window", "close-id", "editor-id"}},
		{"close by title", []string{"window", "close", "Editor"}},
		{"minimize", []string{"window", "minimize", "Editor"}},
		{"maximize", []string{"window", "maximize", "Editor"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failure := errors.New("synthetic compositor sync barrier failed")
			m := &deniedWindowSync{Manager: &pftest.Manager{Lists: [][]window.Info{{{NativeID: "editor-id", Title: "Editor", X: 10, Y: 20, W: 300, H: 220}}}}, syncErr: failure}
			opens := 0
			args := append([]string{"--sync"}, tc.args...)
			stdout, stderr, exit := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { opens++; return pftest.New(nil, nil, m, nil), nil }
			})
			if opens != 1 || m.syncCalls != 1 || exit == 0 || stdout != "" || !strings.Contains(stderr, failure.Error()) {
				t.Fatalf("failed sync misreported as success args=%v opens=%d syncs=%d exit=%d out=%q err=%q", args, opens, m.syncCalls, exit, stdout, stderr)
			}
		})
	}
}

// The window-close waiter must propagate a compositor query error rather
// than interpreting an unavailable backend as an empty window list.
func TestWaitClosePropagatesCompositorQueryFailure(t *testing.T) {
	failure := errors.New("synthetic window-list transport failure")
	backend := &pftest.Manager{Err: failure}
	opens := 0
	cmd := windowCmd(func(context.Context) (*perfuncted.Session, error) {
		opens++
		return pftest.New(nil, nil, backend, nil), nil
	}, nil)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"wait-close", "title=Editor", "--poll=1ms", "--timeout=200ms"})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	if err := cmd.ExecuteContext(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("backend query failure lost: %v", err)
	}
	if opens != 1 || stdout.Len() != 0 {
		t.Fatalf("query failure reported success: opens=%d output=%q", opens, stdout.String())
	}
}

// A denied desktop session cannot act on or report a maximized window.
func TestMaximizePropagatesSessionAuthorizationFailure(t *testing.T) {
	denied := errors.New("synthetic window authorization denied")
	opens := 0
	cmd := windowCmd(func(context.Context) (*perfuncted.Session, error) { opens++; return nil, denied }, nil)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"maximize", "Editor"})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	if err := cmd.ExecuteContext(context.Background()); !errors.Is(err, denied) {
		t.Fatalf("authorization failure lost: %v", err)
	}
	if opens != 1 || stdout.Len() != 0 {
		t.Fatalf("authorization rejection reported success: opens=%d output=%q", opens, stdout.String())
	}
}

// A nonexistent maximize target must not dispatch a mutation to a different
// available window with a similar title.
func TestMaximizeMissingTargetRefusesMutation(t *testing.T) {
	manager := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "different", Title: "Unrelated"}}}}
	cmd := windowCmd(func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, nil, manager, nil), nil }, nil)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"maximize", "Editor"})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	if err := cmd.ExecuteContext(context.Background()); !errors.Is(err, window.ErrWindowNotFound) {
		t.Fatalf("missing target accepted: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("missing target claimed success: %q", stdout.String())
	}
}

type cancellingWatchManager struct {
	*pftest.Manager
	cancel context.CancelFunc
}

func (m *cancellingWatchManager) List(ctx context.Context) ([]window.Info, error) {
	m.cancel()
	return nil, ctx.Err()
}

func TestWindowWatchCancellationDuringEnumerationExitsCleanly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := &cancellingWatchManager{Manager: &pftest.Manager{}, cancel: cancel}
	stdout, stderr, code := captureRunIOContext(ctx, t, []string{"window", "watch", "--output", "json"}, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) {
			return pftest.New(nil, nil, manager, nil), nil
		}
	})
	if code != 0 {
		t.Fatalf("canceled watch exit=%d stdout=%q stderr=%q; want successful shutdown", code, stdout, stderr)
	}
}

type watchRejectingWriter struct{ attempts int }

func (w *watchRejectingWriter) Write([]byte) (int, error) {
	w.attempts++
	return 0, io.ErrClosedPipe
}

// A live watch must not silently claim success when either its backend query
// or its consumer stream fails. No actual compositor or display is accessed.
func TestWindowWatchPropagatesBackendAndStreamFailures(t *testing.T) {
	failure := errors.New("synthetic compositor list failure")
	for _, tc := range []struct {
		name          string
		mode          string
		providerError error
		expected      error
		writes        int
	}{
		{name: "backend failure", mode: "json", providerError: failure, expected: failure},
		{name: "json consumer closed", mode: "json", expected: io.ErrClosedPipe, writes: 1},
		{name: "plain consumer closed", mode: "plain", expected: io.ErrClosedPipe, writes: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "synthetic-window", Title: "Editor"}}}, Err: tc.providerError}
			opens := 0
			cmd := windowCmd(func(context.Context) (*perfuncted.Session, error) {
				opens++
				return pftest.New(nil, nil, manager, nil), nil
			}, nil)
			writer := &watchRejectingWriter{}
			cmd.SetOut(writer)
			cmd.SetErr(io.Discard)
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			cmd.SetArgs([]string{"watch", "--output", tc.mode})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			err := cmd.ExecuteContext(ctx)
			if !errors.Is(err, tc.expected) {
				t.Fatalf("lost watch failure: %v (expected %v)", err, tc.expected)
			}
			if opens != 1 || writer.attempts != tc.writes {
				t.Fatalf("watch opened %d sessions and attempted %d writes, expected %d", opens, writer.attempts, tc.writes)
			}
		})
	}
}

type selectorRecordingManager struct {
	*pftest.Manager
	actions []string
	failure error
}

func (m *selectorRecordingManager) record(action, id string) error {
	m.actions = append(m.actions, action+":"+id)
	return m.failure
}
func (m *selectorRecordingManager) MoveByID(_ context.Context, id string, _, _ int) error {
	return m.record("move", id)
}
func (m *selectorRecordingManager) ResizeByID(_ context.Context, id string, _, _ int) error {
	return m.record("resize", id)
}
func (m *selectorRecordingManager) MinimizeByID(_ context.Context, id string) error {
	return m.record("minimize", id)
}
func (m *selectorRecordingManager) MaximizeByID(_ context.Context, id string) error {
	return m.record("maximize", id)
}
func (m *selectorRecordingManager) FullscreenByID(_ context.Context, id string) error {
	return m.record("fullscreen", id)
}
func (m *selectorRecordingManager) UnfullscreenByID(_ context.Context, id string) error {
	return m.record("unfullscreen", id)
}

func TestWindowOptionalStableIDSelectors(t *testing.T) {
	for _, name := range []string{"move", "resize", "minimize", "maximize", "fullscreen", "unfullscreen", "get-geometry"} {
		t.Run(name, func(t *testing.T) {
			for _, tc := range []struct {
				label, id string
				windows   []window.Info
				wantErr   error
			}{
				{"duplicate titles", "target", []window.Info{{NativeID: "other", Title: "Editor"}, {NativeID: "target", Title: "Editor", X: 10, Y: 20, W: 300, H: 200}}, nil},
				{"opaque priority", "010", []window.Info{{NativeID: "other", ID: 10, Title: "Editor"}, {NativeID: "010", Title: "Editor", X: 10, Y: 20, W: 300, H: 200}}, nil},
				{"hex", "0x2a", []window.Info{{ID: 42, Title: "Editor", X: 10, Y: 20, W: 300, H: 200}}, nil},
				{"missing", "missing", []window.Info{{NativeID: "other", Title: "missing"}}, window.ErrWindowNotFound},
				{"duplicate native IDs", "target", []window.Info{{NativeID: "target", Title: "One"}, {NativeID: "target", Title: "Two"}}, window.ErrWindowAmbiguous},
				{"ambiguous numeric alias", "42", []window.Info{{NativeID: "one", ID: 42, Title: "One"}, {NativeID: "two", ID: 42, Title: "Two"}}, window.ErrWindowAmbiguous},
			} {
				t.Run(tc.label, func(t *testing.T) {
					m := &selectorRecordingManager{Manager: &pftest.Manager{Lists: [][]window.Info{tc.windows}}}
					cmd := windowCmd(func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, nil, m, nil), nil }, nil)
					cmd.SilenceUsage = true
					cmd.SilenceErrors = true
					var out bytes.Buffer
					cmd.SetOut(&out)
					cmd.SetErr(io.Discard)
					cmd.SetArgs([]string{name, "--id", tc.id})
					err := cmd.ExecuteContext(context.Background())
					if tc.wantErr != nil {
						if !errors.Is(err, tc.wantErr) || len(m.actions) != 0 || out.Len() != 0 {
							t.Fatalf("expected %v, got err=%v actions=%v out=%q", tc.wantErr, err, m.actions, out.String())
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if name == "get-geometry" {
						if len(m.actions) != 0 || out.String() != "10,20,310,220\n" {
							t.Fatalf("geometry out=%q actions=%v", out.String(), m.actions)
						}
					} else {
						want := name + ":" + tc.windows[len(tc.windows)-1].StableID()
						if len(m.actions) != 1 || m.actions[0] != want || out.Len() == 0 {
							t.Fatalf("want %q, actions=%v out=%q", want, m.actions, out.String())
						}
					}
				})
			}
		})
	}
}

func TestWindowOptionalIDRejectsConflictsBeforeOpening(t *testing.T) {
	for _, name := range []string{"move", "resize", "minimize", "maximize", "fullscreen", "unfullscreen", "get-geometry"} {
		title := []string{"Editor"}
		if name == "move" || name == "resize" {
			title = []string{"--title", "Editor"}
		}
		for _, args := range [][]string{{name}, {name, "--id", ""}, append([]string{name, "--id", "target"}, title...)} {
			t.Run(strings.Join(args, "/"), func(t *testing.T) {
				opens := 0
				cmd := windowCmd(func(context.Context) (*perfuncted.Session, error) {
					opens++
					return nil, errors.New("unexpected session")
				}, nil)
				cmd.SetArgs(args)
				cmd.SilenceUsage = true
				cmd.SilenceErrors = true
				cmd.SetOut(io.Discard)
				cmd.SetErr(io.Discard)
				if err := cmd.ExecuteContext(context.Background()); err == nil || opens != 0 {
					t.Fatalf("invalid selector %v opened %d sessions: %v", args, opens, err)
				}
			})
		}
	}
}

func TestWindowOptionalIDDispatchFailureAndCancellation(t *testing.T) {
	for _, name := range []string{"move", "resize", "minimize", "maximize", "fullscreen", "unfullscreen"} {
		t.Run(name, func(t *testing.T) {
			for _, canceled := range []bool{false, true} {
				failure := errors.New("provider denied")
				m := &selectorRecordingManager{Manager: &pftest.Manager{Lists: [][]window.Info{{{NativeID: "target", Title: "Editor", W: 300, H: 200}}}}, failure: failure}
				cmd := windowCmd(func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, nil, m, nil), nil }, nil)
				cmd.SetArgs([]string{name, "--id", "target"})
				cmd.SilenceUsage = true
				cmd.SilenceErrors = true
				var out bytes.Buffer
				cmd.SetOut(&out)
				cmd.SetErr(io.Discard)
				ctx, cancel := context.WithCancel(context.Background())
				if canceled {
					cancel()
				}
				err := cmd.ExecuteContext(ctx)
				cancel()
				if canceled {
					if !errors.Is(err, context.Canceled) || len(m.actions) != 0 {
						t.Fatalf("cancellation err=%v actions=%v", err, m.actions)
					}
				} else {
					if !errors.Is(err, failure) || len(m.actions) != 1 || m.actions[0] != name+":target" {
						t.Fatalf("rejection err=%v actions=%v", err, m.actions)
					}
				}
				if out.Len() != 0 {
					t.Fatalf("failed action reported success: %q", out.String())
				}
			}
		})
	}
}

func TestWindowOptionalIDGeometryJSONUsesReportedTitle(t *testing.T) {
	m := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "target", Title: "Reported title", X: 10, Y: 20, W: 300, H: 200}}}}
	out, stderr, code := captureRunIO(t, []string{"window", "get-geometry", "--id", "target", "--output", "json"}, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, nil, m, nil), nil }
	})
	if code != 0 || !strings.Contains(out, `"title":"Reported title"`) || !strings.Contains(out, `"w":300`) {
		t.Fatalf("code=%d out=%q stderr=%q", code, out, stderr)
	}
}

func TestScreenshotPointMapsToLogicalFractionalScale(t *testing.T) {
	outs := []output.Info{{Available: true, Geometry: output.Geometry{W: 1600, H: 900}, ResolutionW: 1920, ResolutionH: 1080}}
	x, y, err := screenshotPointToLogical(outs, 1200, 600)
	if err != nil || x != 1000 || y != 500 {
		t.Fatalf("mapped %d,%d err=%v, want 1000,500", x, y, err)
	}
	for _, bad := range [][]output.Info{{}, {{Available: true, Geometry: output.Geometry{W: 1600, H: 900}, ResolutionW: 0, ResolutionH: 0}}, {outs[0], outs[0]}} {
		if _, _, err := screenshotPointToLogical(bad, 100, 100); err == nil {
			t.Errorf("accepted ambiguous/unknown output geometry %+v", bad)
		}
	}
}

func TestVerifiedActivationObservesTargetWindowNotJustInputDelivery(t *testing.T) {
	m := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "one", Title: "A", Active: false}}, {{NativeID: "one", Title: "A", Active: true}}}}
	pf := pftest.New(nil, nil, m, nil)
	defer pf.Close()
	w, err := pf.Windows().WithContext(context.Background()).Find(window.Match{TitleExact: "A"})
	if err != nil {
		t.Fatal(err)
	}
	if lintShadow36 := waitWindowActive(context.Background(), w, 75*time.Millisecond, 5*time.Millisecond); lintShadow36 != nil {
		t.Fatal(lintShadow36)
	}
	m = &pftest.Manager{Lists: [][]window.Info{{{NativeID: "one", Title: "A", Active: false}}}}
	pf2 := pftest.New(nil, nil, m, nil)
	defer pf2.Close()
	w, err = pf2.Windows().WithContext(context.Background()).Find(window.Match{TitleExact: "A"})
	if err != nil {
		t.Fatal(err)
	}
	if err := waitWindowActive(context.Background(), w, 20*time.Millisecond, 5*time.Millisecond); err == nil {
		t.Fatal("delivered event incorrectly considered verified activation")
	}
}

func TestWaylandXTestInfoExplicitlyWarnsAboutNativeWindows(t *testing.T) {
	report := infoReport{Compositor: "KDE Plasma Wayland", Capabilities: map[string]capabilityEntry{"input": {Supported: true, Backend: "*input.XTestBackend"}}}
	if msg := inputBackendWarning(report); !strings.Contains(msg, "XWayland") || !strings.Contains(msg, "remote-desktop") {
		t.Fatalf("warning=%q", msg)
	}
	report.Compositor = "X11"
	if msg := inputBackendWarning(report); msg != "" {
		t.Errorf("native X11 should not warn, got %q", msg)
	}
}

func TestClickWindowCLIRejectsMissingTargetBeforeOpening(t *testing.T) {
	opened := false
	_, _, code := captureRunIO(t, []string{"input", "click-window", "--x", "15", "--y", "20"}, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) { opened = true; return nil, nil }
	})
	if code == 0 || opened {
		t.Fatalf("missing target code=%d opened=%v", code, opened)
	}
}

var _ = input.CoordinateSpaceLogical

func TestActivateByTitleVerificationRejectsUnfocusedTarget(t *testing.T) {
	m := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "one", Title: "Test Focus", Active: false}}}}
	_, stderr, code := captureRunIO(t, []string{"window", "activate", "Test Focus", "--verify", "--verify-timeout", "150ms", "--verify-poll", "5ms"}, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, nil, m, nil), nil }
	})
	if code == 0 || (!strings.Contains(stderr, "never reported active") && !strings.Contains(stderr, "context deadline exceeded")) {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}
func TestCloseByIDVerificationRequiresWindowDisappearance(t *testing.T) {
	m := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "one", Title: "Dialog"}}}}
	_, stderr, code := captureRunIO(t, []string{"window", "close-id", "one", "--verify", "--verify-timeout", "150ms", "--verify-poll", "5ms"}, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, nil, m, nil), nil }
	})
	if code == 0 || (!strings.Contains(stderr, "remains open") && !strings.Contains(stderr, "context deadline exceeded")) {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func TestActivateByIDVerificationConfirmsActualFocus(t *testing.T) {
	m := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "id-one", Title: "Duplicate", Active: false}, {NativeID: "id-two", Title: "Duplicate", Active: false}}}}
	_, stderr, code := captureRunIO(t, []string{"window", "activate-id", "id-two", "--verify", "--verify-timeout", "150ms", "--verify-poll", "5ms"}, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, nil, m, nil), nil }
	})
	if code == 0 || (!strings.Contains(stderr, "never reported active") && !strings.Contains(stderr, "context deadline exceeded")) {
		t.Fatalf("verify flag ignored: code=%d stderr=%q", code, stderr)
	}
}

func TestWindowRelativePointFuzzTable(t *testing.T) {
	rng := rand.New(rand.NewSource(7319))
	spaces := []input.CoordinateSpaceInfo{{Kind: input.CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1}, {Kind: input.CoordinateSpacePhysical, ScaleX: 1.25, ScaleY: 1.5}, {Kind: input.CoordinateSpacePhysical, ScaleX: 0.5, ScaleY: 0.8}, {Kind: input.CoordinateSpacePhysical, ScaleX: 2, ScaleY: 2}}
	for i := 0; i < 3000; i++ {
		w := window.Info{X: rng.Intn(3001) - 1500, Y: rng.Intn(3001) - 1500, W: rng.Intn(1800) + 1, H: rng.Intn(1200) + 1}
		space := spaces[i%len(spaces)]
		x := rng.Intn(w.W+4) - 2
		y := rng.Intn(w.H+4) - 2
		ax, ay, err := windowRelativePoint(w, x, y, space)
		valid := x >= 0 && y >= 0 && x < w.W && y < w.H
		if valid && err != nil || !valid && err == nil {
			t.Fatalf("case %d: geom=%+v x,y=%d,%d space=%+v: returned %d,%d err %v", i, w, x, y, space, ax, ay, err)
		}
		if valid {
			wantX := int(math.Round(float64(w.X+x) * space.ScaleX))
			wantY := int(math.Round(float64(w.Y+y) * space.ScaleY))
			if ax != wantX || ay != wantY {
				t.Fatalf("case %d: want %d,%d got %d,%d", i, wantX, wantY, ax, ay)
			}
		}
	}
}

func TestWindowRelativePointRejectsOverflowAndNonFinite(t *testing.T) {
	info := window.Info{X: math.MaxInt - 5, Y: math.MaxInt - 5, W: 100, H: 100}
	for _, space := range []input.CoordinateSpaceInfo{
		{Kind: input.CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1},
		{Kind: input.CoordinateSpacePhysical, ScaleX: 2, ScaleY: 2},
		{Kind: input.CoordinateSpacePhysical, ScaleX: math.MaxFloat64, ScaleY: 1},
		{Kind: input.CoordinateSpacePhysical, ScaleX: math.Inf(1), ScaleY: 1},
		{Kind: input.CoordinateSpacePhysical, ScaleX: math.NaN(), ScaleY: 1},
	} {
		if _, _, err := windowRelativePoint(info, 30, 30, space); err == nil {
			t.Errorf("accepted overflow/non-finite coordinate space %+v", space)
		}
	}
}

func FuzzWindowRelativePoint(f *testing.F) {
	for _, seed := range [][4]int{{10, 10, 100, 100}, {-1, 5, 20, 30}, {0, 0, 1, 1}, {math.MaxInt, math.MaxInt, 200, 200}} {
		f.Add(seed[0], seed[1], seed[2], seed[3])
	}
	f.Fuzz(func(t *testing.T, x, y, w, h int) {
		if w < 1 || w > 100000 || h < 1 || h > 100000 {
			return
		}
		px, py, err := windowRelativePoint(window.Info{X: 100, Y: 200, W: w, H: h}, x, y, input.CoordinateSpaceInfo{Kind: input.CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1})
		valid := x >= 0 && x < w && y >= 0 && y < h
		if valid && (err != nil || px != x+100 || py != y+200) || !valid && err == nil {
			t.Fatalf("(%d,%d) dims %dx%d => (%d,%d) err %v", x, y, w, h, px, py, err)
		}
	})
}
