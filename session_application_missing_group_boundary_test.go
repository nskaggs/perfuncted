//go:build linux

package perfuncted

import (
	"context"
	"errors"
	"syscall"
	"testing"

	sessionruntime "github.com/nskaggs/perfuncted/internal/runtime"
)

// A stale application handle must treat an already vanished owned process
// group as stopped and must never claim to own a subsequent process.
func TestApplicationControlTreatsMissingOwnedProcessGroupAsStopped(t *testing.T) {
	const absentPGID = 1 << 30 // Above the Linux pid_max limit.
	if err := syscall.Kill(-absentPGID, 0); !errors.Is(err, syscall.ESRCH) {
		t.Skipf("cannot establish absent process group: %v", err)
	}
	app := &Application{proc: sessionruntime.NewGroup(nil, absentPGID)}
	if err := app.Stop(context.Background()); err != nil {
		t.Fatalf("stopping a vanished owned group must be idempotent: %v", err)
	}
	if err := app.Kill(); err != nil {
		t.Fatalf("killing a vanished owned group must be idempotent: %v", err)
	}
	if !app.proc.Retired() {
		t.Fatal("vanished group not retired from managed process state")
	}
	if app.ownsPID(int32(absentPGID)) {
		t.Fatal("missing process group claimed PID ownership")
	}
}

func TestSessionStopApplicationNilAndLogPathOwnership(t *testing.T) {
	session := &Session{}
	if err := session.stopApplication(nil); err != nil {
		t.Fatalf("stopping absent application = %v", err)
	}
	if path := session.LogPath(); path != "" {
		t.Fatalf("uninitialized session exposed log path %q", path)
	}
	const owned = "/private/owned-session-logs"
	session.infra = &sessionInfra{logDir: owned}
	if path := session.LogPath(); path != owned {
		t.Fatalf("session log path = %q, want %q", path, owned)
	}
}
