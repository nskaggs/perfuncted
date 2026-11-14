//go:build linux

package window

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A D-Bus script start failure must not be interpreted as a successful
// desktop mutation, an empty desktop inventory, or a valid cursor location.
// Every method must release the temporary registered script and permit a
// subsequent authorized request on the same manager.
func TestKWinDesktopOperationsRejectFailedScriptStartAndRecover(t *testing.T) {
	manager, provider := newSyntheticKWinManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	operations := []struct {
		name string
		run  func() error
	}{
		{"list-desktops", func() error { _, err := manager.ListDesktops(ctx); return err }},
		{"switch-desktop", func() error { return manager.SwitchDesktop(ctx, "desktop-1") }},
		{"assign-window-desktop", func() error { return manager.SetWindowDesktopByID(ctx, "7", "desktop-2") }},
		{"keep-above", func() error { return manager.SetKeepAboveByID(ctx, "7", true) }},
		{"keep-below", func() error { return manager.SetKeepBelowByID(ctx, "7", true) }},
		{"cursor-position", func() error {
			x, y, err := manager.CursorPosition(ctx)
			if x != 0 || y != 0 {
				t.Errorf("failed cursor lookup leaked %d,%d", x, y)
			}
			return err
		}},
		{"shade", func() error { return manager.SetShadeByID(ctx, "7", true) }},
		{"tile", func() error { return manager.TileWindowByID(ctx, "7", 10, 20) }},
		{"float", func() error { return manager.FloatWindowByID(ctx, "7") }},
	}
	for _, op := range operations {
		t.Run(op.name, func(t *testing.T) {
			provider.mu.Lock()
			provider.failStart = true
			beforeLoads, beforeStarts, beforeUnloads := provider.loadCount, provider.startCount, provider.unloadCount
			provider.mu.Unlock()
			err := op.run()
			provider.mu.Lock()
			provider.failStart = false
			loads, starts, unloads := provider.loadCount, provider.startCount, provider.unloadCount
			provider.mu.Unlock()
			if err == nil || !strings.Contains(err.Error(), "start") {
				t.Fatalf("failed KWin script was accepted: %v", err)
			}
			if loads != beforeLoads+1 || starts != beforeStarts+1 || unloads != beforeUnloads+1 {
				t.Fatalf("script registration leaked: loads %d->%d, starts %d->%d, unloads %d->%d", beforeLoads, loads, beforeStarts, starts, beforeUnloads, unloads)
			}
		})
	}
	desktops, err := manager.ListDesktops(ctx)
	if err != nil || len(desktops) != 2 || !desktops[0].Current {
		t.Fatalf("failed scripts poisoned later desktop read: %+v, %v", desktops, err)
	}
	provider.mu.Lock()
	loads, unloads := provider.loadCount, provider.unloadCount
	provider.mu.Unlock()
	if loads != len(operations)+1 || unloads != loads {
		t.Fatalf("scripts not released after recovery: %d loads / %d unloads", loads, unloads)
	}
}
