//go:build linux

package input

import (
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
)

func TestUinputProbePreservesCharacterDeviceOpenFailure(t *testing.T) {
	info, err := os.Lstat("/dev/tty")
	if err != nil {
		t.Fatalf("stat Linux controlling-terminal device: %v", err)
	}
	if info.Mode()&os.ModeDevice == 0 || info.Mode()&os.ModeCharDevice == 0 {
		t.Fatalf("/dev/tty is not a character device: %v", info.Mode())
	}

	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err == nil {
		_ = tty.Close()
		t.Skip("the test process has a controlling terminal")
	}
	if !errors.Is(err, syscall.ENXIO) {
		t.Fatalf("open /dev/tty without a controlling terminal: %v, want ENXIO", err)
	}

	got := checkUinputPath("/dev/tty")
	if got.Name != "uinput" || got.Available || !strings.Contains(got.Reason, "/dev/tty open:") || strings.Contains(got.Reason, "permission denied") {
		t.Fatalf("character-device open failure was accepted or misclassified: %+v", got)
	}
}
