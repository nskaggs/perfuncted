package window

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Cursor coordinates originate with the compositor, and must not be silently
// rounded, overflowed, or accepted from the wrong JSON type. On any invalid
// coordinate the public readback must return neither stale nor partial data.
func TestKWinCursorPrivateBusRejectsMalformedOrOutOfRangeCoordinates(t *testing.T) {
	manager, provider := newSyntheticKWinManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, tc := range []struct{ name, reply, fragment string }{
		{"invalid JSON", `{"x":0,`, "cursor position"},
		{"string x", `{"x":"2","y":1}`, "unmarshal"},
		{"boolean y", `{"x":2,"y":true}`, "unmarshal"},
		{"array result", `[2,3]`, "unmarshal"},
		{"x outside platform int", `{"x":1e30,"y":1}`, "cursor x"},
		{"y outside platform int", `{"x":2,"y":-1e30}`, "cursor y"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider.mu.Lock()
			provider.listOverride = tc.reply
			provider.mu.Unlock()
			x, y, err := manager.CursorPosition(ctx)
			if err == nil || x != 0 || y != 0 || !strings.Contains(err.Error(), tc.fragment) {
				t.Fatalf("cursor payload=%q returned %d,%d err=%v", tc.reply, x, y, err)
			}
		})
	}
	for _, tc := range []struct {
		payload string
		x, y    int
	}{
		{`{"x":-2147483648,"y":2147483647}`, -2147483648, 2147483647},
		{`{"x":2.5,"y":-1.25}`, 2, -1},
		{`{"x":2147483648,"y":-2147483649}`, 2147483648, -2147483649},
	} {
		provider.mu.Lock()
		provider.listOverride = tc.payload
		provider.mu.Unlock()
		x, y, err := manager.CursorPosition(ctx)
		if err != nil || x != tc.x || y != tc.y {
			t.Fatalf("valid cursor position %q = %d,%d err=%v", tc.payload, x, y, err)
		}
	}
	provider.mu.Lock()
	loads, starts, unloads := provider.loadCount, provider.startCount, provider.unloadCount
	provider.mu.Unlock()
	if loads != 9 || starts != 9 || unloads != 9 {
		t.Fatalf("cursor scripts leaked: %d loads %d starts %d unloads", loads, starts, unloads)
	}
}
