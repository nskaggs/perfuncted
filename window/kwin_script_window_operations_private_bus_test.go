//go:build linux

package window

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// Exercise the actual temporary-script callback and cleanup protocol for
// handle lookups rather than using an in-memory fake window manager.
func TestKWinPrivateBusInfoByIDIteratorOperationsAndCleanup(t *testing.T) {
	manager, provider := newSyntheticKWinManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	info, err := manager.InfoByID(ctx, "7")
	if err != nil || info.NativeID != "7" || info.Title != "Editor" || info.PID != 33 || info.X != -100 {
		t.Fatalf("native handle resolution %+v %v", info, err)
	}
	if _, err := manager.InfoByID(ctx, "nonexistent-window"); !errors.Is(err, ErrWindowNotFound) {
		t.Fatalf("nonexistent native handle accepted: %v", err)
	}
	count := 0
	for win, err := range manager.IterateWindows(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		count++
		if win.NativeID != "7" {
			t.Fatalf("wrong native window %q", win.NativeID)
		}
		break
	}
	if count != 1 {
		t.Fatalf("iterator produced %d windows", count)
	}
	if err := manager.FullscreenByID(ctx, "7"); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("unimplemented compositor capability falsely succeeded: %v", err)
	}
	if err := manager.UnfullscreenByID(ctx, "7"); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("unimplemented unfullscreen falsely succeeded: %v", err)
	}
	if err := manager.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	ops := manager.SupportedOperations()
	for _, required := range []string{"discover", "info", "active-title", "activate", "move", "resize", "close", "minimize", "maximize", "restore"} {
		found := false
		for _, op := range ops {
			if op == required {
				found = true
			}
		}
		if !found {
			t.Fatalf("KWin advertised ops missing %q: %v", required, ops)
		}
	}
	for _, unsupported := range []string{"fullscreen", "unfullscreen"} {
		for _, op := range ops {
			if op == unsupported {
				t.Fatalf("KWin advertises unsupported operation %s", op)
			}
		}
	}
	provider.mu.Lock()
	loads, starts, unloads, path := provider.loadCount, provider.startCount, provider.unloadCount, provider.loadedPath
	provider.mu.Unlock()
	if loads != 3 || starts != 3 || unloads != 3 {
		t.Fatalf("KWin native lookup leaked scripts: loads=%d starts=%d unloads=%d", loads, starts, unloads)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("KWin script temp file leaked: %v", err)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := manager.InfoByID(canceled, "7"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled native handle query: %v", err)
	}
	if err := manager.Sync(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled KWin sync: %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.InfoByID(ctx, "7"); err == nil {
		t.Fatal("closed KWin transport accepted new command")
	}
}
func TestKWinPrivateBusListAndActionFailuresNeverFabricateWindow(t *testing.T) {
	manager, provider := newSyntheticKWinManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	provider.mu.Lock()
	provider.noCallback = true
	provider.mu.Unlock()
	short, stop := context.WithTimeout(ctx, 60*time.Millisecond)
	defer stop()
	if _, err := manager.List(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("callback timeout reported verified empty list: %v", err)
	}
	provider.mu.Lock()
	provider.noCallback = false
	loads, unloads := provider.loadCount, provider.unloadCount
	provider.mu.Unlock()
	if loads != unloads {
		t.Fatalf("failed list leaked KWin script: loads=%d unloads=%d", loads, unloads)
	}
	if err := manager.MoveByID(ctx, "7", -100, 30); err != nil {
		t.Fatal(err)
	}
	if err := kwinActionResultByID("7", kwinScriptErrorPrefix+"synthetic compositor rejected movement"); err == nil || !strings.Contains(err.Error(), "rejected movement") {
		t.Fatalf("explicit KWin script error not propagated %v", err)
	}
	if err := kwinActionResultByID("7", ""); !errors.Is(err, ErrWindowNotFound) {
		t.Fatalf("KWin empty callback fabricated window: %v", err)
	}
}
