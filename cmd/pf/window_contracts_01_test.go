package main

import (
	"bytes"
	"context"
	"errors"
	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
	"strings"
	"testing"
	"time"
)

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
		{"activate by title", []string{"window", "activate", "Editor"}, 1, 1},
		{"activate by ID", []string{"window", "activate-id", "editor-id"}, 1, 1},
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
		{"activate by stable ID", []string{"window", "activate-id", "id-editor"}, []window.Info{info}, nil, "activation requested", true},
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
	if err := waitWindowGone(context.Background(), nil, "", time.Second, time.Millisecond); err == nil {
		t.Fatal("disappearance verification accepted an empty ID")
	}
}

func TestWindowVerificationPropagatesBackendErrors(t *testing.T) {
	wantErr := errors.New("synthetic window backend failure")
	manager := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "editor", Title: "Editor"}}}}
	session := pftest.New(nil, nil, manager, nil)
	t.Cleanup(func() { _ = session.Close() })

	target, err := session.Windows.Find(context.Background(), window.Match{TitleExact: "Editor"})
	if err != nil {
		t.Fatalf("find verification target: %v", err)
	}

	manager.Err = wantErr
	if err := waitWindowActive(context.Background(), target, time.Second, time.Millisecond); !errors.Is(err, wantErr) {
		t.Fatalf("activation verification error = %v, want %v", err, wantErr)
	}
	if err := waitWindowGone(context.Background(), session.Windows, "editor", time.Second, time.Millisecond); !errors.Is(err, wantErr) {
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
