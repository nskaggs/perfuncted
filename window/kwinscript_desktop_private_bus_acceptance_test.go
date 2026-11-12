//go:build linux

package window

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Exercise the public KWin desktop method through the actual private D-Bus
// scripting lifecycle. Parsed desktop state must come from its callback, not
// the session's desktop labels or a previously cached response.
func TestKWinListDesktopsPrivateBusCallbackAndValidation(t *testing.T) {
	manager, provider := newSyntheticKWinManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	got, err := manager.ListDesktops(ctx)
	if err != nil || len(got) != 2 || got[0].ID != "desktop-1" || !got[0].Current || got[1].ID != "desktop-2" || got[1].Current {
		t.Fatalf("KWin desktop callback: got=%+v err=%v", got, err)
	}

	for _, tc := range []struct{ name, data, reason string }{
		{"untrusted non-array", `{"id":"desktop-1"}`, "cannot unmarshal"},
		{"null current", `[{"id":"desktop-1","position":1,"current":null}]`, "null current"},
		{"ambiguous current", `[{"id":"desktop-1","position":1,"current":true},{"id":"desktop-2","position":2,"current":true}]`, "one current"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider.mu.Lock()
			provider.listOverride = tc.data
			provider.mu.Unlock()
			desktops, err := manager.ListDesktops(ctx)
			if err == nil || desktops != nil || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("accepted invalid desktop response: got=%+v err=%v", desktops, err)
			}
		})
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.loadCount != 4 || provider.startCount != 4 || provider.unloadCount != 4 {
		t.Fatalf("KWin scripts leaked: loads=%d starts=%d unloads=%d", provider.loadCount, provider.startCount, provider.unloadCount)
	}
}
