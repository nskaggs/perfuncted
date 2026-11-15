package window

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Malformed compositor results cannot be exposed as a partial window list,
// and the temporary script must be unloaded after the failure.
func TestSyntheticKWinMalformedWindowSnapshotFailsClosedAndUnloadsScript(t *testing.T) {
	manager, provider := newSyntheticKWinManager(t)
	provider.mu.Lock()
	provider.listOverride = `[{"id":"7","title":"valid"},{"id":false}]`
	provider.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	windows, err := manager.List(ctx)
	if err == nil || len(windows) != 0 {
		t.Fatalf("malformed compositor snapshot accepted: windows=%v err=%v", windows, err)
	}
	if !strings.Contains(err.Error(), "window") && !strings.Contains(err.Error(), "id") {
		t.Logf("malformed snapshot rejected: %v", err)
	}
	provider.mu.Lock()
	loads, starts, unloads := provider.loadCount, provider.startCount, provider.unloadCount
	provider.mu.Unlock()
	if loads != 1 || starts != 1 || unloads != 1 {
		t.Fatalf("rejected KWin snapshot leaked a script: loads=%d starts=%d unloads=%d", loads, starts, unloads)
	}
}

func TestKWinNilManagerCloseIsIdempotent(t *testing.T) {
	var manager *KWinScriptManager
	if err := manager.Close(); err != nil {
		t.Fatalf("nil manager cleanup failed: %v", err)
	}
	if err := (&KWinScriptManager{}).Close(); err != nil {
		t.Fatalf("disconnected manager cleanup failed: %v", err)
	}
}
