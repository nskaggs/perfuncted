//go:build linux

package window

import (
	"context"
	"errors"
	"strings"
	"syscall"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestKWinRunScriptReportsTemporaryScriptCleanupFailure(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	manager, provider := newSyntheticKWinManager(t)
	provider.mu.Lock()
	provider.replaceLoadedScriptPath = true
	provider.mu.Unlock()

	result, err := manager.ActiveTitle(context.Background())
	if result != "Editor" {
		t.Fatalf("script result = %q, want callback result despite cleanup failure", result)
	}
	if !errors.Is(err, syscall.ENOTEMPTY) || !strings.Contains(err.Error(), "remove temp script") {
		t.Fatalf("script error = %v, want surfaced temp-script cleanup failure", err)
	}
}

func TestKWinRunScriptJoinsCleanupErrorsAfterBusDisconnect(t *testing.T) {
	manager, provider := newSyntheticKWinManager(t)
	provider.mu.Lock()
	provider.closeClientConnOnStart = manager.conn
	provider.mu.Unlock()

	result, err := manager.ActiveTitle(context.Background())
	if result != "" {
		t.Fatalf("script result = %q, want no result after D-Bus disconnect", result)
	}
	if err == nil || !strings.Contains(err.Error(), "unloadScript") || !strings.Contains(err.Error(), "release D-Bus name") {
		t.Fatalf("script error = %v, want start and cleanup failures joined", err)
	}
}

func TestRequestKWinNameRejectsNameOwnedBySameConnection(t *testing.T) {
	_, provider := newSyntheticKWinManager(t)
	const serviceName = "org.kde.pflistCoverageDuplicate"
	reply, err := provider.conn.RequestName(serviceName, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claim synthetic callback name: reply=%v err=%v", reply, err)
	}
	t.Cleanup(func() { _, _ = provider.conn.ReleaseName(serviceName) })

	err = requestKWinName(context.Background(), provider.conn, serviceName)
	if err == nil || !strings.Contains(err.Error(), "already taken") || !strings.Contains(err.Error(), serviceName) {
		t.Fatalf("duplicate RequestName error = %v, want already-taken error for %s", err, serviceName)
	}
}
