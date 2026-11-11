package window

import (
	"context"
	"strings"
	"testing"
)

// KWin 6 exposes a maximizeMode bitmask, not a boolean maximized property.
// The compositor's reported state must reflect full, not partial, maximization.
func TestKWinWindowListingReadsFullMaximizeMode(t *testing.T) {
	manager, provider := newSyntheticKWinManager(t)
	if _, err := manager.List(context.Background()); err != nil {
		t.Fatalf("KWin window listing: %v", err)
	}
	provider.mu.Lock()
	script := provider.loadedScript
	provider.mu.Unlock()
	if !strings.Contains(script, "maximized: Number(w.maximizeMode) === 3") {
		t.Fatalf("window listing did not read KWin's maximizeMode (full=3): %s", script)
	}
	if strings.Contains(script, "!!w.maximized") {
		t.Fatal("window listing relied on nonexistent KWin maximized property")
	}
}
