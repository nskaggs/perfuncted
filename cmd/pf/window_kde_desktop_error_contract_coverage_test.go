package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
)

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
