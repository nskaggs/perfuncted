package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A mistyped script option must fail before a window mutation, image capture,
// or hash of an unintended region. Silently ignoring options makes defaults
// unexpectedly authoritative and can publish a screenshot to another path.
func TestScriptCommandsRejectUnknownOptionsBeforeSideEffects(t *testing.T) {
	for _, tc := range []struct{ name, script string }{
		{"window move", "window move --title Editor --x 20 --y 30 --z 90"},
		{"window resize", "window resize --title Editor --w 300 --h 200 --depth 90"},
		{"screen hash", "screen hash --rect 0,0,2,2 --region 1,1,1,1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _, _ := newScriptFixture(t)
			if err := s.run(strings.NewReader(tc.script + "\n")); err == nil || !strings.Contains(err.Error(), "unknown") {
				t.Fatalf("unknown option was ignored: %q err=%v", tc.script, err)
			}
		})
	}
	t.Run("screen grab", func(t *testing.T) {
		s, _, _, _ := newScriptFixture(t)
		dest := filepath.Join(t.TempDir(), "capture.png")
		err := s.run(strings.NewReader("screen grab --rect 0,0,2,2 --out '" + dest + "' --output typo\n"))
		if err == nil || !strings.Contains(err.Error(), "unknown") {
			t.Fatalf("unknown screen capture option accepted: %v", err)
		}
		if _, err := os.Stat(dest); !os.IsNotExist(err) {
			t.Fatalf("unexpected screenshot created: %v", err)
		}
	})
}
