package main

import (
	"context"
	"errors"
	"image"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
)

// An empty rectangle is the Screenshotter API sentinel for full-screen grab.
// Explicit --rect arguments must not silently turn a zero-area crop into a
// full-screen capture or normalize backwards coordinates into a different crop.
func TestExplicitCLIRectangleRejectsEmptyAndReversedBounds(t *testing.T) {
	for _, bad := range []string{
		"1,1,1,1", "5,7,5,9", "5,7,8,7", "8,7,5,9", "5,9,8,7",
	} {
		t.Run(bad, func(t *testing.T) {
			got, err := parseRect(bad)
			if err == nil {
				t.Fatalf("explicit invalid crop %q accepted as %v (empty may request full-screen)", bad, got)
			}
		})
	}
	got, err := parseRect("1, 2, 3, 4")
	if err != nil || got != image.Rect(1, 2, 3, 4) {
		t.Fatalf("valid crop became %v, %v", got, err)
	}
}

func TestInvalidExplicitScreenCropRejectedBeforeOpeningSession(t *testing.T) {
	for _, args := range [][]string{
		{"screen", "grab", "--rect", "1,1,1,1", "--out", filepath.Join(t.TempDir(), "private.png")},
		{"screen", "hash", "--rect", "8,7,5,9"},
		{"screen", "grab-region", "--rect", "1,1,1,1", "--out", "-"},
	} {
		opened := false
		_, stderr, code := captureRunIO(t, args, func(*cliConfig) sessionOpener {
			return func(context.Context) (*perfuncted.Session, error) {
				opened = true
				return nil, errors.New("unwanted capture session")
			}
		})
		if code == 0 || opened || !strings.Contains(stderr, "--rect requires") {
			t.Fatalf("invalid crop opened session: args=%v code=%d opened=%t stderr=%q", args, code, opened, stderr)
		}
	}
}
