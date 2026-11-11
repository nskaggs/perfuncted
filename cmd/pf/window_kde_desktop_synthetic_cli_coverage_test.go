package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
)

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
