//go:build linux

package screen

import (
	"context"
	"errors"
	"image"
	"strings"
	"testing"
	"unicode/utf8"
)

// A screenshot portal supplies URI paths, which may encode sensitive filenames
// or directory components. The per-backend diagnostic is long lived and can
// appear in capabilities or user-visible failure reports, unlike the direct
// error returned to the caller. It must never publish that untrusted path.
func TestScreenshotPortalDiagnosticsNeverExposePrivateURIPaths(t *testing.T) {
	const secret = "private-health-record-ACCESS-TOKEN"
	for _, tc := range []struct{ name, uri, reason string }{
		{"malformed URI", "file:///tmp/" + secret + "%", "invalid screenshot URI"},
		{"missing private file", "file:///tmp/" + secret + "-not-present.png", "unable to open screenshot file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, portal := newSyntheticScreenshotPortal(t)
			portal.mu.Lock()
			portal.uri = tc.uri
			portal.mu.Unlock()
			img, err := backend.Grab(context.Background(), image.Rectangle{})
			if err == nil || img != nil {
				t.Fatalf("invalid URI accepted: img=%v err=%v", img, err)
			}
			details := strings.Join(backend.Diagnostics(), " ")
			if strings.Contains(details, secret) {
				t.Fatalf("persistent screenshot diagnostics leaked private portal URI: %s", details)
			}
			if !strings.Contains(details, tc.reason) {
				t.Fatalf("diagnostic missed safe failure classification %q: %s", tc.reason, details)
			}
		})
	}
}

func TestScreenshotPortalDiagnosticsBoundUntrustedErrorLength(t *testing.T) {
	backend := &PortalDBusBackend{}
	backend.recordFailure(errors.New(strings.Repeat("malformed-portal-error", 100000)))
	details := strings.Join(backend.Diagnostics(), " ")
	if len(details) > 1024 {
		t.Fatalf("untrusted portal failure allocated %d diagnostic bytes", len(details))
	}
	if !strings.Contains(details, "last failure:") {
		t.Fatalf("missing bounded error diagnostic: %s", details)
	}
}

func TestScreenshotPortalDiagnosticsTruncateOnWholeUTF8Boundary(t *testing.T) {
	backend := &PortalDBusBackend{}
	backend.recordFailure(errors.New(strings.Repeat("x", 255) + "é" + strings.Repeat("sensitive", 100)))
	backend.healthMu.RLock()
	retained := backend.lastFailure
	backend.healthMu.RUnlock()
	if !utf8.ValidString(retained) || len(retained) > 256 {
		t.Fatalf("bounded diagnostic invalid UTF-8 or size: bytes=%d valid=%v", len(retained), utf8.ValidString(retained))
	}
	for _, text := range []string{
		"screen/portal: close root for /private/important.png: inaccessible",
		"screen/portal: close /private/important.png: inaccessible",
		"screen/portal: file:///private/important.png failed",
	} {
		backend.recordFailure(errors.New(text))
		backend.healthMu.RLock()
		got := backend.lastFailure
		backend.healthMu.RUnlock()
		if strings.Contains(got, "/private/") {
			t.Fatalf("retained screenshot path from %q as %q", text, got)
		}
	}
}
