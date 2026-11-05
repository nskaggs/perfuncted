package perfuncted

import (
	"context"
	"strings"
	"testing"
)

// An embedded NUL truncates a Linux sockaddr_un pathname. Preflight must
// reject it rather than approving a path the session cannot actually bind.
func TestManagedSessionSocketPathRejectsEmbeddedNUL(t *testing.T) {
	for _, path := range []string{"/tmp/perfuncted\x00/bus", "\x00", "valid\x00suffix"} {
		if err := validateSessionUnixSocketPath(path); err == nil {
			t.Errorf("accepted nonrepresentable Unix socket pathname %q", path)
		}
		if err := validateManagedSessionSocketPath(path); err == nil {
			t.Errorf("accepted nonrepresentable managed socket pathname %q", path)
		}
	}
	if err := validateManagedSessionSocketPath("/tmp/perfuncted-normal/bus"); err != nil {
		t.Fatalf("ordinary socket pathname rejected: %v", err)
	}
}

func TestManagedSessionDBusRejectsEmbeddedNULBeforeSpawn(t *testing.T) {
	infra := &sessionInfra{dbusAddr: "unix:path=/tmp/perfuncted\x00/bus", ctx: context.Background()}
	err := infra.launchDBus(context.Background())
	if err == nil || !strings.Contains(err.Error(), "NUL") {
		t.Fatalf("D-Bus startup did not reject embedded NUL path: %v", err)
	}
	if infra.dbusCmd != nil {
		t.Fatal("attempted D-Bus process spawn for invalid socket pathname")
	}
}
