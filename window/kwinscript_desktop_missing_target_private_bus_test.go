//go:build linux

package window

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A KWin compositor that reports no matching virtual desktop must return the
// desktop-specific sentinel, not success or an unrelated missing-window error.
// Rejected operations must release their per-request scripts and must not
// poison the same manager's subsequent legitimate desktop selection.
func TestKWinSwitchDesktopDistinguishesMissingDesktopAndRecovers(t *testing.T) {
	manager, provider := newSyntheticKWinManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	provider.mu.Lock()
	provider.actionOverride = "__pf_desktop_missing__"
	provider.mu.Unlock()
	err := manager.SwitchDesktop(ctx, "nonexistent-desktop")
	if !errors.Is(err, ErrDesktopNotFound) || errors.Is(err, ErrWindowNotFound) {
		t.Fatalf("missing compositor desktop accepted or misclassified: %v", err)
	}
	provider.mu.Lock()
	provider.actionOverride = ""
	provider.mu.Unlock()
	if err := manager.SwitchDesktop(ctx, "desktop-1"); err != nil {
		t.Fatalf("missing-desktop response poisoned next selection: %v", err)
	}
	provider.mu.Lock()
	loads, starts, unloads := provider.loadCount, provider.startCount, provider.unloadCount
	provider.mu.Unlock()
	if loads != 2 || starts != 2 || unloads != 2 {
		t.Fatalf("desktop selection leaked scripts: loads=%d starts=%d unloads=%d", loads, starts, unloads)
	}
}
