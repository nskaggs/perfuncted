package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/v2"
	"github.com/nskaggs/perfuncted/v2/input"
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

type coordinateTestInput struct {
	*pftest.Inputter
	space   input.CoordinateSpaceInfo
	failure error
}

func (i *coordinateTestInput) PointerCoordinateSpace(context.Context) (input.CoordinateSpaceInfo, error) {
	return i.space, i.failure
}

type outputTestLister struct{ outs []output.Info }

func (l *outputTestLister) List(context.Context) ([]output.Info, error) { return l.outs, nil }
func (l *outputTestLister) Close() error                                { return nil }

func TestScreenPixelsClickActualMappedCoordinates(t *testing.T) {
	in := &coordinateTestInput{Inputter: &pftest.Inputter{}, space: input.CoordinateSpaceInfo{Kind: input.CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1}}
	outs := &outputTestLister{outs: []output.Info{{Available: true, Geometry: output.Geometry{W: 1600, H: 900}, ResolutionW: 1920, ResolutionH: 1080}}}
	out, errOut, code := captureRunIO(t, []string{"input", "click-screen", "--x", "1200", "--y", "600"}, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) {
			return pftest.NewWithOutputs(nil, in, nil, outs, nil), nil
		}
	})
	if code != 0 {
		t.Fatalf("code=%d output=%q err=%q", code, out, errOut)
	}
	if calls := strings.Join(in.Calls, ","); !strings.Contains(calls, "1000") || !strings.Contains(calls, "500") {
		t.Fatalf("mouse calls=%q, expected logical 1000,500", calls)
	}
}

func TestWindowRelativeClickTargetsStableIDOnly(t *testing.T) {
	in := &coordinateTestInput{Inputter: &pftest.Inputter{}, space: input.CoordinateSpaceInfo{Kind: input.CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1}}
	mgr := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "a", Title: "Duplicate", X: 100, Y: 200, W: 600, H: 300}, {NativeID: "b", Title: "Duplicate", X: 300, Y: 400, W: 200, H: 200}}}}
	out, errOut, code := captureRunIO(t, []string{"input", "click-window", "--id", "b", "--x", "20", "--y", "30"}, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, in, mgr, nil), nil }
	})
	if code != 0 {
		t.Fatalf("code=%d output=%q err=%q", code, out, errOut)
	}
	if calls := strings.Join(in.Calls, ","); !strings.Contains(calls, "320") || !strings.Contains(calls, "430") {
		t.Fatalf("mouse calls=%q expected 320,430", calls)
	}
}

func TestFileChooserRestoresClipboardAfterSuccessAndInputFailure(t *testing.T) {
	file := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(file, []byte("epub-test"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{true: "typing-fails", false: "success"}[fail], func(t *testing.T) {
			in := &pftest.Inputter{}
			if fail {
				in.Err = os.ErrPermission
			}
			mgr := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "dialog-1", Title: "File Upload", AppID: "chooser", Active: true}}, {{NativeID: "dialog-1", Title: "File Upload", AppID: "chooser", Active: true}}, {{NativeID: "dialog-1", Title: "File Upload", AppID: "chooser", Active: true}}, {{NativeID: "dialog-1", Title: "File Upload", AppID: "chooser", Active: true}}, {{NativeID: "dialog-1", Title: "File Upload", AppID: "chooser", Active: true}}, {{NativeID: "dialog-1", Title: "File Upload", AppID: "chooser", Active: true}}, {{NativeID: "dialog-1", Title: "File Upload", AppID: "chooser", Active: true}}, {{NativeID: "dialog-1", Title: "File Upload", AppID: "chooser", Active: true}}, {}}}
			cb := &pftest.Clipboard{Text: "prior clipboard"}
			stdout, stderr, code := captureRunIO(t, []string{"input", "select-file", "--id", "dialog-1", "--path", file}, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, in, mgr, cb), nil }
			})
			if fail && code == 0 || !fail && code != 0 {
				t.Fatalf("fail=%v code=%d stdout=%q stderr=%q", fail, code, stdout, stderr)
			}
			if cb.Text != "prior clipboard" {
				t.Fatalf("clipboard not restored: %q", cb.Text)
			}
			if len(mgr.Activated) != 1 || mgr.Activated[0] != "dialog-1" {
				t.Errorf("wrong focused target: %v", mgr.Activated)
			}
			if !fail && len(in.Calls) != 4 {
				t.Errorf("expected enter selection + confirmation, calls=%v", in.Calls)
			}
		})
	}
}

func TestFileChooserRefusesToTypeIntoUnfocusedWindow(t *testing.T) {
	file := filepath.Join(t.TempDir(), "test.epub")
	_ = os.WriteFile(file, []byte("test"), 0600)
	in := &pftest.Inputter{}
	mgr := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "dialog-1", Title: "File Upload", AppID: "chooser", Active: false}}}}
	_, _, code := captureRunIO(t, []string{"input", "select-file", "--id", "dialog-1", "--path", file}, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) {
			return pftest.New(nil, in, mgr, &pftest.Clipboard{Text: "original"}), nil
		}
	})
	if code == 0 || len(in.Calls) != 0 {
		t.Fatalf("unfocused chooser accepted input: code=%d calls=%v", code, in.Calls)
	}
}

func TestScreenClickRejectsUntrustedBackendCoordinateMetadata(t *testing.T) {
	for _, space := range []input.CoordinateSpaceInfo{
		{},
		{Kind: input.CoordinateSpaceLogical, ScaleX: 0, ScaleY: 0},
		{Kind: input.CoordinateSpaceLogical, ScaleX: 2, ScaleY: 2},
		{Kind: input.CoordinateSpacePhysical, ScaleX: math.NaN(), ScaleY: 1},
	} {
		t.Run(fmt.Sprint(space), func(t *testing.T) {
			in := &coordinateTestInput{Inputter: &pftest.Inputter{}, space: space}
			outs := &outputTestLister{outs: []output.Info{{Available: true, Geometry: output.Geometry{W: 1600, H: 900}, ResolutionW: 1920, ResolutionH: 1080}}}
			_, stderr, code := captureRunIO(t, []string{"input", "click-screen", "--x", "600", "--y", "300"}, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					return pftest.NewWithOutputs(nil, in, nil, outs, nil), nil
				}
			})
			if code == 0 || len(in.Calls) > 0 {
				t.Fatalf("invalid coordinate space %+v accepted, code=%d calls=%v stderr=%q", space, code, in.Calls, stderr)
			}
		})
	}
}

func TestScreenshotClickCommandRequiresOutputs(t *testing.T) {
	root := newRootCmd(func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) { return nil, nil }
	})
	if findCommandPath(root, "input", "click-screen") == nil {
		t.Fatal("missing screenshot-to-logical click command")
	}
}
func TestFileChooserCommandRequiresARealFileBeforeOpeningSession(t *testing.T) {
	opened := false
	factory := func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) { opened = true; return nil, nil }
	}
	for _, path := range []string{"relative.epub", filepath.Join(t.TempDir(), "missing.epub")} {
		_, errOut, code := captureRunIO(t, []string{"input", "select-file", "--title", "File Upload", "--path", path}, factory)
		if code == 0 || opened {
			t.Fatalf("path=%q code=%d opened=%v stderr=%q", path, code, opened, errOut)
		}
	}
}
func TestInfoWarningMentionsExplicitConsent(t *testing.T) {
	report := infoReport{Compositor: "KDE Plasma Wayland", Capabilities: map[string]capabilityEntry{"input": {Supported: true, Backend: "*input.XTestBackend"}}}
	warning := inputBackendWarning(report)
	if !strings.Contains(warning, "remote-desktop") || !strings.Contains(warning, "native Wayland") {
		t.Fatalf("warning=%q", warning)
	}
}

func TestKDEDesktopCommandsReturnSessionOpenFailures(t *testing.T) {
	openErr := errors.New("synthetic session startup failure")
	for _, args := range [][]string{
		{"desktop", "list"},
		{"desktop", "switch", "desk-alpha"},
		{"window", "active-info"},
		{"window", "cursor"},
		{"window", "set-desktop", "Editor", "desk-beta"},
		{"window", "keep-above", "Editor", "on"},
		{"window", "shade", "Editor", "on"},
	} {
		t.Run(strings.Join(args, "-"), func(t *testing.T) {
			_, stderr, code := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { return nil, openErr }
			})
			if code == 0 || !strings.Contains(stderr, openErr.Error()) {
				t.Fatalf("session open failure was lost: code=%d stderr=%q", code, stderr)
			}
		})
	}
}

type kdeDesktopSwitchWithoutPublishedCurrent struct{ pftest.Manager }

func (*kdeDesktopSwitchWithoutPublishedCurrent) SupportedOperations() []string {
	return []string{"list-desktops", "switch-desktop"}
}

func (*kdeDesktopSwitchWithoutPublishedCurrent) ListDesktops(context.Context) ([]window.DesktopInfo, error) {
	return []window.DesktopInfo{{ID: "desk-current", Current: true}}, nil
}

func (*kdeDesktopSwitchWithoutPublishedCurrent) SwitchDesktop(context.Context, string) error {
	return nil
}

func TestKDEDesktopSwitchVerificationRejectsUnpublishedStableID(t *testing.T) {
	manager := &kdeDesktopSwitchWithoutPublishedCurrent{}
	_, stderr, code := captureRunIO(t, []string{"desktop", "switch", "desk-missing"}, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) {
			return perfuncted.NewSessionForTesting(nil, nil, manager, nil, nil), nil
		}
	})
	if code == 0 || !strings.Contains(stderr, "virtual desktop switch verification") || !strings.Contains(stderr, window.ErrDesktopNotFound.Error()) {
		t.Fatalf("switch reported success without compositor confirmation: code=%d stderr=%q", code, stderr)
	}
}

func TestKDEActiveInfoRejectsMissingAndUnreadableActiveWindow(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(*syntheticKDEWindowState)
		want    string
	}{
		{"no active window", func(m *syntheticKDEWindowState) { m.Lists = [][]window.Info{{}} }, "expected one active window"},
		{"active window listing failed", func(m *syntheticKDEWindowState) { m.Err = errors.New("synthetic active list failure") }, "synthetic active list failure"},
		{"active window details failed", func(m *syntheticKDEWindowState) { m.readErr = errors.New("synthetic active info failure") }, "synthetic active info failure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := newKDEWindowState()
			tc.prepare(manager)
			_, stderr, code := runKDEWindowStateCLI(t, manager, "window", "active-info")
			if code == 0 || !strings.Contains(stderr, tc.want) {
				t.Fatalf("active-info result code=%d stderr=%q, want %q", code, stderr, tc.want)
			}
		})
	}
}

func TestKDEWindowCommandsDoNotClaimVerifiedMutationAfterReadFailure(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		want       string
		wantAction string
	}{
		{"desktop assignment", []string{"window", "set-desktop", "Editor", "desk-two"}, "window desktop assignment verification", "desktop:uuid-a:desk-two"},
		{"keep above", []string{"window", "keep-above", "Editor", "on"}, "keep-above verification", "above:uuid-a"},
		{"shade", []string{"window", "shade", "Editor", "on"}, "shade verification", "shade:uuid-a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := newKDEWindowState()
			manager.readErr = errors.New("synthetic post-mutation state failure")
			_, stderr, code := runKDEWindowStateCLI(t, manager, tc.args...)
			if code == 0 || !strings.Contains(stderr, tc.want) || !strings.Contains(stderr, manager.readErr.Error()) {
				t.Fatalf("mutation verification result code=%d stderr=%q", code, stderr)
			}
			if manager.action != tc.wantAction {
				t.Fatalf("verification failed before dispatch: action=%q want=%q", manager.action, tc.wantAction)
			}
		})
	}
}

type kdeDesktopMutationWithoutPublishedState struct{ *syntheticKDEWindowState }

func (m *kdeDesktopMutationWithoutPublishedState) SetWindowDesktopByID(_ context.Context, id, desktop string) error {
	m.action = "desktop:" + id + ":" + desktop
	return nil
}

func TestKDEDesktopAssignmentWaitsForCompositorStateUntilCallerDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	manager := &kdeDesktopMutationWithoutPublishedState{syntheticKDEWindowState: newKDEWindowState()}
	_, stderr, code := captureRunIOContext(ctx, t, []string{"window", "set-desktop", "Editor", "desk-two"}, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) {
			return perfuncted.NewSessionForTesting(nil, nil, manager, nil, nil), nil
		}
	})
	if code == 0 || !strings.Contains(stderr, "window desktop assignment verification") || !strings.Contains(stderr, context.DeadlineExceeded.Error()) {
		t.Fatalf("assignment with stale compositor state was accepted: code=%d stderr=%q", code, stderr)
	}
	if manager.action != "desktop:uuid-a:desk-two" {
		t.Fatalf("verification deadline occurred before mutation dispatch: action=%q", manager.action)
	}
}

func TestKDEWindowCommandsRejectMissingTitlesBeforeMutation(t *testing.T) {
	for _, args := range [][]string{
		{"window", "set-desktop", "Missing", "desk-two"},
		{"window", "keep-above", "Missing", "on"},
		{"window", "shade", "Missing", "on"},
	} {
		t.Run(strings.Join(args, "-"), func(t *testing.T) {
			manager := newKDEWindowState()
			manager.Lists = [][]window.Info{{}}
			_, stderr, code := runKDEWindowStateCLI(t, manager, args...)
			if code == 0 || !strings.Contains(stderr, window.ErrWindowNotFound.Error()) {
				t.Fatalf("missing target result code=%d stderr=%q", code, stderr)
			}
			if manager.action != "" {
				t.Fatalf("missing target dispatched mutation %q", manager.action)
			}
		})
	}
}

func TestKDEWindowCommandsApplyOffStateAndPropagateOutputFailure(t *testing.T) {
	for _, tc := range []struct {
		args       []string
		wantOutput string
		wantAction string
	}{
		{[]string{"window", "keep-below", "Editor", "off"}, "keep-below false: uuid-a", "below:uuid-a"},
		{[]string{"window", "shade", "Editor", "off"}, "shade false: uuid-a", "shade:uuid-a"},
	} {
		manager := newKDEWindowState()
		out, stderr, code := runKDEWindowStateCLI(t, manager, tc.args...)
		if code != 0 || !strings.Contains(out, tc.wantOutput) || stderr != "" || manager.action != tc.wantAction {
			t.Fatalf("off-state result code=%d out=%q stderr=%q action=%q", code, out, stderr, manager.action)
		}
	}

	writeErr := errors.New("synthetic output stream closed")
	manager := &syntheticKDEDesktopCLIManager{current: "desk-alpha"}
	command := desktopCmd(func(context.Context) (*perfuncted.Session, error) {
		return perfuncted.NewSessionForTesting(nil, nil, manager, nil, nil), nil
	})
	command.SetArgs([]string{"list"})
	command.SetOut(rejectCLIWrite{err: writeErr})
	command.SetErr(io.Discard)
	if err := command.Execute(); !errors.Is(err, writeErr) {
		t.Fatalf("desktop list output error = %v, want %v", err, writeErr)
	}
}

type rejectCLIWrite struct{ err error }

func (w rejectCLIWrite) Write([]byte) (int, error) { return 0, w.err }

type syntheticKDEDesktopCLIManager struct {
	pftest.Manager
	current   string
	switched  string
	listErr   error
	switchErr error
	cursorErr error
	listCalls int
}

func (m *syntheticKDEDesktopCLIManager) SupportedOperations() []string {
	return []string{"list-desktops", "switch-desktop", "cursor-position"}
}
func (m *syntheticKDEDesktopCLIManager) ListDesktops(ctx context.Context) ([]window.DesktopInfo, error) {
	m.listCalls++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.listErr != nil {
		return nil, m.listErr
	}
	return []window.DesktopInfo{
		{ID: "desk-alpha", Name: "Work", Position: 1, Current: m.current == "desk-alpha"},
		{ID: "desk-beta", Name: "Review", Position: 2, Current: m.current == "desk-beta"},
	}, nil
}
func (m *syntheticKDEDesktopCLIManager) SwitchDesktop(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.switchErr != nil {
		return m.switchErr
	}
	if id != "desk-alpha" && id != "desk-beta" {
		return window.ErrDesktopNotFound
	}
	m.current = id
	m.switched = id
	return nil
}

func runSyntheticKDEDesktopCLI(t *testing.T, manager *syntheticKDEDesktopCLIManager, args ...string) (string, string, int) {
	t.Helper()
	return captureRunIO(t, args, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) {
			return perfuncted.NewSessionForTesting(nil, nil, manager, nil, nil), nil
		}
	})
}

func TestKDEDesktopCLIListAndSwitchWithSyntheticBackend(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		want      string
		current   string
		switchTo  string
		listCalls int
	}{
		{"list-plain", []string{"desktop", "list"}, "1\ttrue\tdesk-alpha\tWork", "desk-alpha", "", 1},
		{"list-json", []string{"desktop", "list", "--output=json"}, `"id":"desk-alpha"`, "desk-alpha", "", 1},
		{"switch-verified", []string{"desktop", "switch", "desk-beta"}, "switched to desktop desk-beta", "desk-alpha", "desk-beta", 1},
		{"switch-no-verification", []string{"desktop", "switch", "desk-beta", "--verify=false"}, "switched to desktop desk-beta", "desk-alpha", "desk-beta", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &syntheticKDEDesktopCLIManager{current: tc.current}
			out, errOut, code := runSyntheticKDEDesktopCLI(t, m, tc.args...)
			if code != 0 || !strings.Contains(out, tc.want) {
				t.Fatalf("code=%d out=%q stderr=%q", code, out, errOut)
			}
			if m.switched != tc.switchTo || m.listCalls != tc.listCalls {
				t.Fatalf("unexpected KWin calls: switched=%q lists=%d", m.switched, m.listCalls)
			}
		})
	}
}

func TestKDEDesktopCLIRejectsInvalidArgumentsAndBackendFailures(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		want   string
		setErr func(*syntheticKDEDesktopCLIManager)
	}{
		{"invalid-output", []string{"desktop", "list", "--output=xml"}, "unknown output format", nil},
		{"blank-id", []string{"desktop", "switch", " "}, "desktop ID must not be blank", nil},
		{"missing-id", []string{"desktop", "switch"}, "accepts 1 arg", nil},
		{"missing-desktop", []string{"desktop", "switch", "desk-missing"}, "virtual desktop not found", nil},
		{"list-error", []string{"desktop", "list"}, "synthetic list failure", func(m *syntheticKDEDesktopCLIManager) { m.listErr = errors.New("synthetic list failure") }},
		{"switch-error", []string{"desktop", "switch", "desk-beta"}, "synthetic switch failure", func(m *syntheticKDEDesktopCLIManager) { m.switchErr = errors.New("synthetic switch failure") }},
		{"verification-error", []string{"desktop", "switch", "desk-beta"}, "virtual desktop switch verification", func(m *syntheticKDEDesktopCLIManager) { m.listErr = errors.New("synthetic verification failure") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &syntheticKDEDesktopCLIManager{current: "desk-alpha"}
			if tc.setErr != nil {
				tc.setErr(m)
			}
			out, errOut, code := runSyntheticKDEDesktopCLI(t, m, tc.args...)
			if code == 0 || !strings.Contains(errOut, tc.want) {
				t.Fatalf("code=%d out=%q stderr=%q want %q", code, out, errOut, tc.want)
			}
		})
	}
}

func TestKDEStateWaiterHandlesImmediateSuccessAndProbeError(t *testing.T) {
	if err := waitForKWinState(context.Background(), func(context.Context) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("synthetic compositor query failed")
	if err := waitForKWinState(context.Background(), func(context.Context) (bool, error) { return false, cause }); !errors.Is(err, cause) {
		t.Fatalf("lost error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForKWinState(ctx, func(context.Context) (bool, error) { return false, nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled wait succeeded: %v", err)
	}
}

func (m *syntheticKDEDesktopCLIManager) CursorPosition(ctx context.Context) (int, int, error) {
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	if m.cursorErr != nil {
		return 0, 0, m.cursorErr
	}
	return -120, 540, nil
}

func TestKDEWindowCursorCLIUsesCompositorReadAndPreservesBackendFailure(t *testing.T) {
	m := &syntheticKDEDesktopCLIManager{}
	out, stderr, code := runSyntheticKDEDesktopCLI(t, m, "window", "cursor")
	if code != 0 || out != "-120,540\n" {
		t.Fatalf("cursor response code=%d out=%q stderr=%q", code, out, stderr)
	}
	m.cursorErr = errors.New("synthetic compositor read denied")
	out, stderr, code = runSyntheticKDEDesktopCLI(t, m, "window", "cursor")
	if code == 0 || !strings.Contains(stderr, "synthetic compositor read denied") {
		t.Fatalf("cursor failure code=%d out=%q stderr=%q", code, out, stderr)
	}
}

func TestKDEStateWaiterRetriesUntilCompositorAcknowledges(t *testing.T) {
	calls := 0
	err := waitForKWinState(context.Background(), func(context.Context) (bool, error) { calls++; return calls == 2, nil })
	if err != nil || calls != 2 {
		t.Fatalf("did not verify eventual compositor state: calls=%d err=%v", calls, err)
	}
}

type syntheticKDEWindowState struct {
	pftest.Manager
	info    window.Info
	action  string
	fail    error
	readErr error
}

func (m *syntheticKDEWindowState) SupportedOperations() []string {
	return []string{"discover", "info", "active-title", "set-window-desktop", "keep-above", "keep-below", "shade-when-supported", "tile-window", "float-window"}
}
func (m *syntheticKDEWindowState) InfoByID(_ context.Context, id string) (window.Info, error) {
	if m.readErr != nil {
		return window.Info{}, m.readErr
	}
	if id != m.info.NativeID {
		return window.Info{}, window.ErrWindowNotFound
	}
	return m.info, nil
}
func (m *syntheticKDEWindowState) SetWindowDesktopByID(_ context.Context, id, desktop string) error {
	m.action = "desktop:" + id + ":" + desktop
	if m.fail != nil {
		return m.fail
	}
	m.info.DesktopIDs = desktop
	return nil
}
func (m *syntheticKDEWindowState) SetKeepAboveByID(_ context.Context, id string, on bool) error {
	m.action = "above:" + id
	if m.fail != nil {
		return m.fail
	}
	m.info.KeepAbove = on
	return nil
}
func (m *syntheticKDEWindowState) SetKeepBelowByID(_ context.Context, id string, on bool) error {
	m.action = "below:" + id
	if m.fail != nil {
		return m.fail
	}
	m.info.KeepBelow = on
	return nil
}
func (m *syntheticKDEWindowState) SetShadeByID(_ context.Context, id string, on bool) error {
	m.action = "shade:" + id
	if m.fail != nil {
		return m.fail
	}
	m.info.Shaded = on
	return nil
}
func (m *syntheticKDEWindowState) TileWindowByID(_ context.Context, id string, x, y int) error {
	m.action = "tile:" + id
	if x != 13 || y != 17 {
		return errors.New("wrong tile coordinates")
	}
	if m.fail != nil {
		return m.fail
	}
	m.info.Tiled = true
	return nil
}
func (m *syntheticKDEWindowState) FloatWindowByID(_ context.Context, id string) error {
	m.action = "float:" + id
	if m.fail != nil {
		return m.fail
	}
	m.info.Tiled = false
	return nil
}

func runKDEWindowStateCLI(t *testing.T, m *syntheticKDEWindowState, args ...string) (string, string, int) {
	t.Helper()
	return captureRunIO(t, args, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) {
			return perfuncted.NewSessionForTesting(nil, nil, m, nil, nil), nil
		}
	})
}
func newKDEWindowState() *syntheticKDEWindowState {
	m := &syntheticKDEWindowState{info: window.Info{NativeID: "uuid-a", Title: "Editor", Active: true, Tiled: true, DesktopIDs: "desk-one"}}
	m.Lists = [][]window.Info{{m.info}}
	return m
}
func TestKDEWindowStateCommandsDispatchStableIDAndVerify(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		want   string
		action string
	}{
		{"active-info", []string{"window", "active-info"}, `"NativeID":"uuid-a"`, ""},
		{"set-desktop", []string{"window", "set-desktop", "Editor", "desk-two"}, "moved window uuid-a to desktop desk-two", "desktop:uuid-a:desk-two"},
		{"above", []string{"window", "keep-above", "Editor", "on"}, "keep-above true: uuid-a", "above:uuid-a"},
		{"below", []string{"window", "keep-below", "Editor", "on"}, "keep-below true: uuid-a", "below:uuid-a"},
		{"shade", []string{"window", "shade", "Editor", "on"}, "shade true: uuid-a", "shade:uuid-a"},
		{"tile", []string{"window", "tile", "Editor", "--x=13", "--y=17"}, "tiled window uuid-a at 13,17", "tile:uuid-a"},
		{"float", []string{"window", "float", "Editor"}, "floating window uuid-a", "float:uuid-a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newKDEWindowState()
			out, stderr, code := runKDEWindowStateCLI(t, m, tc.args...)
			if code != 0 || !strings.Contains(out, tc.want) || m.action != tc.action {
				t.Fatalf("code=%d output=%q stderr=%q action=%q want=%q", code, out, stderr, m.action, tc.action)
			}
		})
	}
}
func TestKDEWindowStateCommandFailuresDoNotClaimSuccess(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
		fail bool
	}{
		{"blank-tile-title", []string{"window", "tile", " "}, "window title cannot be blank", false},
		{"blank-float-title", []string{"window", "float", " "}, "window title cannot be blank", false},
		{"bad-above", []string{"window", "keep-above", "Editor", "maybe"}, "state must be on or off", false},
		{"bad-below", []string{"window", "keep-below", "Editor", "maybe"}, "state must be on or off", false},
		{"bad-shade", []string{"window", "shade", "Editor", "maybe"}, "shade state must be on or off", false},
		{"blank-desktop", []string{"window", "set-desktop", "Editor", " "}, "title and desktop ID must not be empty", false},
		{"missing-window", []string{"window", "float", "Missing"}, "window", false},
		{"backend-tile", []string{"window", "tile", "Editor", "--x=13", "--y=17"}, "synthetic mutation refused", true},
		{"backend-float", []string{"window", "float", "Editor"}, "synthetic mutation refused", true},
		{"backend-shade", []string{"window", "shade", "Editor", "on"}, "synthetic mutation refused", true},
		{"backend-desktop", []string{"window", "set-desktop", "Editor", "desk-two"}, "synthetic mutation refused", true},
		{"backend-above", []string{"window", "keep-above", "Editor", "on"}, "synthetic mutation refused", true},
		{"backend-below", []string{"window", "keep-below", "Editor", "on"}, "synthetic mutation refused", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newKDEWindowState()
			if tc.fail {
				m.fail = errors.New("synthetic mutation refused")
			}
			out, stderr, code := runKDEWindowStateCLI(t, m, tc.args...)
			if code == 0 || !strings.Contains(stderr, tc.want) {
				t.Fatalf("code=%d out=%q stderr=%q want=%q", code, out, stderr, tc.want)
			}
		})
	}
}
