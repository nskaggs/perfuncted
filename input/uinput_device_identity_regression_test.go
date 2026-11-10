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

// A symlink to an unrelated character device is not proof that the kernel's
// uinput endpoint exists. Probing must inspect the path itself, not its target.
func TestUinputProbeRejectsSymlinkToUnrelatedCharacterDevice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uinput")
	if err := os.Symlink("/dev/null", path); err != nil {
		t.Fatal(err)
	}
	result := checkUinputPath(path)
	if result.Available || !strings.Contains(result.Reason, "device") {
		t.Fatalf("symlink to unrelated character device advertised as uinput: %+v", result)
	}
}
