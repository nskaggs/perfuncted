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
)

// Every valid CLI operation must stop at an unavailable accessibility session
// rather than emitting fabricated success or reaching a privileged primitive.
func TestAccessibilityCLIValidCommandsPropagateAuthorizationDenial(t *testing.T) {
	denied := errors.New("synthetic AT-SPI authorization denied")
	for _, tc := range []struct {
		name   string
		args   []string
		window bool
	}{
		{"apps", []string{"apps"}, false},
		{"tree scoped", []string{"tree", "--app", "Editor"}, false},
		{"find scoped", []string{"find", "--app", "Editor", "--name", "Save"}, false},
		{"focused", []string{"focused"}, false},
		{"at point default", []string{"at-point"}, false},
		{"at point coordinates", []string{"at-point", "14", "18"}, false},
		{"events", []string{"events", "--buffer", "2"}, false},
		{"semantic action", []string{"action", "--app", "Editor", "--role", "button"}, false},
		{"semantic focus", []string{"focus", "--app", "Editor", "--role", "textbox"}, false},
		{"semantic text", []string{"text", "--app", "Editor", "--role", "entry", "--value", "new"}, false},
		{"raw focus", []string{"raw", "--op", "focus", "--bus", "org.test.App", "--path", "/node", "--generation", "1"}, false},
		{"raw reopen", []string{"raw", "--op", "reopen"}, false},
		{"window scoped tree", []string{"tree", "--window-id", "123"}, true},
		{"window scoped action", []string{"action", "--window", "Editor", "--role", "button"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			direct, windowOpens := 0, 0
			mainOpen := func(context.Context) (*perfuncted.Session, error) { direct++; return nil, denied }
			windowOpen := func(context.Context) (*perfuncted.Session, error) { windowOpens++; return nil, denied }
			cmd := accessibilityCmd(mainOpen, windowOpen)
			// Production's root command suppresses Cobra usage text on
			// operational failures. Preserve that contract in this direct harness.
			cmd.SilenceUsage = true
			var out, errOut bytes.Buffer
			cmd.SetArgs(tc.args)
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			err := cmd.ExecuteContext(context.Background())
			if !errors.Is(err, denied) {
				t.Fatalf("authorization failure lost: %v", err)
			}
			if out.Len() != 0 {
				t.Fatalf("denied action published result: %q", out.String())
			}
			if tc.window {
				if direct != 0 || windowOpens != 1 {
					t.Fatalf("wrong session opened: main=%d window=%d", direct, windowOpens)
				}
			} else if direct != 1 || windowOpens != 0 {
				t.Fatalf("wrong session opened: main=%d window=%d", direct, windowOpens)
			}
		})
	}
}

func TestAccessibilityAtPointRejectsMalformedCoordinatesBeforeOpeningSession(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"one coordinate", []string{"1"}, "expects zero or two coordinates"},
		{"invalid x", []string{"left", "2"}, "at-point x"},
		{"invalid y", []string{"1", "down"}, "at-point y"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opens := 0
			args := append([]string{"a11y", "at-point"}, tc.args...)
			out, stderr, code := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opens++
					return nil, errors.New("unexpected session open")
				}
			})
			if code == 0 || out != "" || !strings.Contains(stderr, tc.want) || opens != 0 {
				t.Fatalf("malformed coordinates reached session: args=%v code=%d stdout=%q stderr=%q opens=%d", args, code, out, stderr, opens)
			}
		})
	}
}

func TestAccessibilityWindowTitleAndIDConflictFailsBeforeATSPICorrelation(t *testing.T) {
	opens := 0
	args := []string{"a11y", "tree", "--window", "Editor", "--window-id", "different-id"}
	out, stderr, code := captureRunIO(t, args, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) {
			opens++
			manager := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "managed-id", Title: "Editor"}}}}
			return perfuncted.NewSessionForTesting(nil, nil, manager, nil, nil, cliAutomationFake{}), nil
		}
	})
	if code == 0 || out != "" || !strings.Contains(stderr, "identify different managed windows") || opens != 1 {
		t.Fatalf("conflicting managed window selectors accepted: code=%d stdout=%q stderr=%q opens=%d", code, out, stderr, opens)
	}
}

// An empty document-selection batch cannot be applied by the native AT-SPI
// backend. Reject it before desktop authorization rather than opening a
// session only to discover a known-invalid request after connecting.
func TestAccessibilityEmptyDocumentSelectionsRejectBeforeDesktopAuthorization(t *testing.T) {
	for _, input := range []string{"[]", "null", " [] ", "\nnull"} {
		t.Run(strings.TrimSpace(input), func(t *testing.T) {
			opens := 0
			args := []string{"a11y", "raw", "--bus", "org.test.App", "--path", "/editor", "--generation", "1", "--op", "set-document-text-selections", "--selections", input}
			stdout, stderr, code := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opens++
					return nil, errors.New("desktop authorization reached")
				}
			})
			if code == 0 || opens != 0 || stdout != "" || !strings.Contains(stderr, "must not be empty") {
				t.Fatalf("empty selection=%q: opened=%d code=%d stdout=%q stderr=%q", input, opens, code, stdout, stderr)
			}
		})
	}
}

// Explicitly negative event buffering is invalid user input, not a request
// for a default. Refuse before acquiring accessibility authorization.
func TestAccessibilityEventsRejectNegativeBufferBeforeSession(t *testing.T) {
	for _, v := range []string{"-1", "-4096"} {
		t.Run(v, func(t *testing.T) {
			opened := 0
			_, stderr, exit := captureRunIO(t, []string{"a11y", "events", "--buffer=" + v}, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { opened++; return nil, nil }
			})
			if opened != 0 {
				t.Fatalf("negative buffer opened accessibility session %d times", opened)
			}
			if exit == 0 || !strings.Contains(stderr, "buffer") {
				t.Fatalf("exit=%d stderr=%q", exit, stderr)
			}
		})
	}
}

func TestAccessibilityEventsPropagateOutputFailure(t *testing.T) {
	failure := errors.New("event output consumer failed")
	out := &screenCommandFailureWriter{failAt: 1, failure: failure}
	cmd := accessibilityCmd(func(context.Context) (*perfuncted.Session, error) {
		return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil, cliEventsFake{}), nil
	}, nil)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetOut(out)
	cmd.SetArgs([]string{"events"})
	err := cmd.ExecuteContext(context.Background())
	if !errors.Is(err, failure) || out.writes != 1 {
		t.Fatalf("event output error=%v writes=%d, want original writer failure", err, out.writes)
	}
}

// AT-SPI node handles must be real D-Bus object paths. Malformed paths are
// statically invalid and must not trigger a desktop authorization prompt.
func TestAccessibilityRawMalformedObjectPathsNeverOpenDesktopSession(t *testing.T) {
	for _, path := range []string{"node", "/node//child", "/node/../child", "/node-with-hyphen"} {
		t.Run(path, func(t *testing.T) {
			opened := 0
			args := []string{"a11y", "raw", "--op", "focus", "--bus", "org.example.App", "--path", path, "--generation", "1"}
			out, stderr, code := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opened++
					return nil, errors.New("unexpected desktop access")
				}
			})
			if code == 0 || opened != 0 || out != "" || !strings.Contains(stderr, "valid D-Bus object path") {
				t.Fatalf("invalid path %q opened=%d code=%d stdout=%q stderr=%q", path, opened, code, out, stderr)
			}
		})
	}
}

// Invalid process IDs must not trigger AT-SPI authorization or access.
func TestAccessibilityNegativePIDFailsBeforeOpeningSession(t *testing.T) {
	for _, args := range [][]string{
		{"a11y", "tree", "--pid=-1"},
		{"a11y", "find", "--pid=-50"},
		{"a11y", "action", "--pid=-2", "--action", "press"},
		{"a11y", "focus", "--pid=-3"},
		{"a11y", "text", "--pid=-4"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			opened := 0
			_, errText, code := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opened++
					return nil, nil
				}
			})
			if opened != 0 {
				t.Fatalf("opened session %d times for invalid PID", opened)
			}
			if code == 0 || !strings.Contains(strings.ToLower(errText), "pid") {
				t.Fatalf("code=%d stderr=%q", code, errText)
			}
		})
	}
}

func TestAccessibilityNegativeSnapshotBoundsFailBeforeSession(t *testing.T) {
	for _, tc := range []struct {
		flag    string
		command string
	}{
		{"--max-depth=-1", "tree"},
		{"--max-nodes=-2", "find"},
		{"--max-text-bytes=-3", "action"},
		{"--max-total-bytes=-4", "focus"},
		{"--max-depth=-5", "text"},
	} {
		t.Run(tc.command+" "+tc.flag, func(t *testing.T) {
			args := []string{"a11y", tc.command, "--app", "Example", tc.flag}
			count := 0
			_, stderr, exit := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { count++; return nil, nil }
			})
			if count != 0 {
				t.Fatalf("opened desktop session %d times for invalid limit", count)
			}
			if exit == 0 || !strings.Contains(stderr, "--max-") {
				t.Fatalf("exit=%d stderr=%q", exit, stderr)
			}
		})
	}
}

func TestAccessibilityAtPointRejectsOutOfRangeCoordinatesBeforeOpen(t *testing.T) {
	cases := [][]string{
		{"a11y", "at-point", "2147483648", "0"},
		{"a11y", "at-point", "--", "0", "-2147483649"},
		{"a11y", "at-point", "--x=2147483648"},
		{"a11y", "at-point", "--y=-2147483649"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			opened := 0
			_, errText, code := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { opened++; return nil, nil }
			})
			if opened != 0 {
				t.Fatalf("opened accessibility session for out-of-range point: %d", opened)
			}
			if code == 0 || !strings.Contains(errText, "range") {
				t.Fatalf("code=%d stderr=%q", code, errText)
			}
		})
	}
}

func TestAccessibilityAtPointAcceptsSigned32BitBoundaryCoordinates(t *testing.T) {
	for _, args := range [][]string{
		{"a11y", "at-point", "--x=2147483647", "--y=-2147483648"},
		{"a11y", "at-point", "--", "-2147483648", "2147483647"},
		{"a11y", "at-point"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			opened := 0
			_, _, _ = captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opened++
					return nil, errors.New("synthetic session opener reached")
				}
			})
			if opened != 1 {
				t.Fatalf("valid 32-bit coordinate should reach session opener once, got %d", opened)
			}
		})
	}
}
