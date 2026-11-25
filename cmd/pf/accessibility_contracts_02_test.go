package main

import (
	"bytes"
	"context"
	"errors"
	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/accessibility"
	"strings"
	"testing"
)

// Raw operations are a defense-in-depth boundary: even direct dispatch must
// reject malformed local parameters before touching an accessibility backend.
func TestRawAccessibilityDispatchRejectsMalformedInputsBeforeBackend(t *testing.T) {
	for _, tc := range []struct {
		name    string
		request rawInvocation
		message string
	}{
		{"scroll alignment", rawInvocation{op: "scroll", alignment: "bogus-alignment"}, "unknown alignment"},
		{"scroll point space", rawInvocation{op: "scroll-to-point", coord: "bogus-space"}, "unknown coordinate space"},
		{"position space", rawInvocation{op: "set-position", coord: "bogus-space"}, "unknown coordinate space"},
		{"extents space", rawInvocation{op: "set-extents", coord: "bogus-space"}, "unknown coordinate space"},
		{"malformed selections", rawInvocation{op: "set-document-text-selections", selectionsJSON: "{not json"}, "invalid --selections JSON"},
		{"unknown operation", rawInvocation{op: "unrecognized-op"}, "unknown raw op"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, err := invokeRawOp(context.Background(), nil, accessibility.NodeID{}, tc.request)
			if value != nil || err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("malformed request returned value=%v err=%v (want %q)", value, err, tc.message)
			}
		})
	}
}

func TestAccessibilityRawMalformedRequestNeverOpensAuthorizationSession(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"missing operation", []string{"a11y", "raw", "--bus", "org.test.App", "--path", "/node", "--generation", "1"}},
		{"missing bus", []string{"a11y", "raw", "--op", "focus", "--path", "/node", "--generation", "1"}},
		{"missing path", []string{"a11y", "raw", "--op", "focus", "--bus", "org.test.App", "--generation", "1"}},
		{"missing generation", []string{"a11y", "raw", "--op", "focus", "--bus", "org.test.App", "--path", "/node"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opened := 0
			out, errOut, code := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opened++
					return nil, errors.New("unnecessary D-Bus authorization")
				}
			})
			if code == 0 || opened != 0 || strings.Contains(errOut, "unnecessary D-Bus") || out != "" {
				t.Fatalf("malformed request %v accessed desktop %d times: code=%d out=%q err=%q", tc.args, opened, code, out, errOut)
			}
		})
	}
}

func TestAccessibilityRawInvalidTypedOperationNeverAcquiresSession(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"unknown raw operation", []string{"--op", "not-an-operation"}},
		{"invalid scroll alignment", []string{"--op", "scroll", "--alignment", "wrong"}},
		{"invalid scroll coordinate", []string{"--op", "scroll-to-point", "--coordinate-space", "wrong"}},
		{"invalid position coordinate", []string{"--op", "set-position", "--coordinate-space", "wrong"}},
		{"invalid extents coordinate", []string{"--op", "set-extents", "--coordinate-space", "wrong"}},
		{"invalid document selections JSON", []string{"--op", "set-document-text-selections", "--selections", "{"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opened := 0
			args := append([]string{"a11y", "raw", "--bus", "org.example.App", "--path", "/node", "--generation", "1"}, tc.args...)
			out, errOut, code := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opened++
					return nil, errors.New("unexpected authorization")
				}
			})
			if opened != 0 || code == 0 || out != "" || strings.Contains(errOut, "unexpected authorization") {
				t.Fatalf("invalid typed op %v triggered %d sessions: code=%d stdout=%q stderr=%q", args, opened, code, out, errOut)
			}
		})
	}
}

// Incomplete semantic requests must not initialize the AT-SPI desktop,
// especially when opening the session may trigger user authorization.
func TestAccessibilitySemanticCommandsRequireScopeBeforeOpeningSession(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"tree", []string{"a11y", "tree"}},
		{"find", []string{"a11y", "find", "--name", "Save"}},
		{"action", []string{"a11y", "action", "--role", "button", "--action", "press"}},
		{"focus", []string{"a11y", "focus", "--role", "textbox"}},
		{"text", []string{"a11y", "text", "--role", "entry", "--value", "confidential"}},
		{"empty app scope", []string{"a11y", "tree", "--app", "   "}},
		{"empty window scope", []string{"a11y", "focus", "--window", " "}},
		{"explicit zero PID", []string{"a11y", "find", "--pid", "0"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			attempts := 0
			root := newRootCmd(func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					attempts++
					return nil, errors.New("unexpected accessibility session authorization")
				}
			})
			var out, errOut bytes.Buffer
			root.SetArgs(tc.args)
			root.SetOut(&out)
			root.SetErr(&errOut)
			err := root.ExecuteContext(context.Background())
			if err == nil || !strings.Contains(err.Error(), "explicit --app") {
				t.Fatalf("missing scope returned %v", err)
			}
			if attempts != 0 {
				t.Fatalf("scope-free request accessed desktop %d times", attempts)
			}
			if out.Len() != 0 {
				t.Fatalf("unscoped request produced output: %q", out.String())
			}
		})
	}
}
