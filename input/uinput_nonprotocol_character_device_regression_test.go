package input

import (
	"os"
	"strings"
	"testing"
)

// A writable character device is not necessarily the uinput protocol.
// Reporting an unrelated device as input-capable breaks backend selection.
func TestUinputProbeRejectsWritableNonUinputCharacterDevice(t *testing.T) {
	if _, err := os.Stat("/dev/null"); err != nil {
		t.Skipf("no synthetic character device: %v", err)
	}
	got := checkUinputPath("/dev/null")
	if got.Available {
		t.Fatalf("unrelated writable character device advertised as uinput: %+v", got)
	}
	if got.Name != "uinput" || !strings.Contains(got.Reason, "uinput") {
		t.Fatalf("missing diagnostic for unrelated device: %+v", got)
	}
}
