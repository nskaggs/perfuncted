package input

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A writable regular file is not the kernel uinput device. Probe must not
// advertise hardware event authority based merely on successful open(2).
func TestUinputProbeRejectsRegularFileMasqueradingAsDevice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uinput")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	result := checkUinputPath(path)
	if result.Available || !strings.Contains(result.Reason, "device") {
		t.Fatalf("regular file advertised as uinput device: %+v", result)
	}
}
