//go:build linux

package input

import (
	"context"
	"strings"
	"testing"
)

// A non-ASCII character may case-fold to an ASCII letter. A physical key
// operation must not silently transliterate that character into another key.
func TestPortalRemoteKeyCodeRejectsNonASCIICasefoldAliases(t *testing.T) {
	for _, name := range []string{"İ", "K", "𝑨"} { // Non-ASCII letters cannot authorize a different ASCII key
		if code, err := remoteKeyCode(name); err == nil || code != 0 || !strings.Contains(err.Error(), "unknown key") {
			t.Fatalf("Unicode alias %q mapped to physical code %d: %v", name, code, err)
		}
		b, _, _ := newSyntheticRevocableRemoteDesktop(t)
		if err := b.KeyDown(context.Background(), name); err == nil || !strings.Contains(err.Error(), "unknown key") {
			t.Fatalf("invalid requested Unicode key %q pressed device: %v", name, err)
		}
		if len(b.heldKeys) != 0 {
			t.Fatalf("invalid key acquired physical key ownership: %+v", b.heldKeys)
		}
	}
	for _, key := range []string{"a", "A", "ctrl", "f12", "/", "?"} {
		if _, err := remoteKeyCode(key); err != nil {
			t.Fatalf("valid ASCII physical key %q rejected: %v", key, err)
		}
	}
}
