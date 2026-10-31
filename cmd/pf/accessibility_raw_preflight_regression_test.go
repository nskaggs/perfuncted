package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
)

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
