//go:build linux

package input

import (
	"strings"
	"testing"

	"github.com/bendahl/uinput"
)

// Physical key identifiers are ASCII protocol names, not locale-sensitive
// Unicode text. Case folding must not turn a non-ASCII symbol into a different
// authorized physical key.
func TestUinputRejectsUnicodeAliasesOfPhysicalKeys(t *testing.T) {
	backend := &UinputBackend{}
	for _, tc := range []struct {
		key  string
		want int
	}{
		{"i", uinput.KeyI},
		{"I", uinput.KeyI},
		{"k", uinput.KeyK},
		{"K", uinput.KeyK},
	} {
		got, err := backend.resolveKey(tc.key)
		if err != nil || got != tc.want {
			t.Fatalf("ASCII physical key %q resolved to %d err=%v, want %d", tc.key, got, err, tc.want)
		}
	}
	for _, key := range []string{"İ", "K", "ſ", "ı"} {
		got, err := backend.resolveKey(key)
		if err == nil || got != 0 || !strings.Contains(err.Error(), "unknown key") {
			t.Errorf("Unicode alias %q resolved to physical key %d err=%v", key, got, err)
		}
	}
}
