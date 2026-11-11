package window

import (
	"context"
	"testing"
)

// KWin's cursor script promises a JSON object with both numeric coordinates.
// A valid position may be (0,0), but absent/null fields are not evidence for it.
func TestKWinCursorRejectsMissingOrNullCompositorCoordinates(t *testing.T) {
	manager, provider := newSyntheticKWinManager(t)
	for _, tc := range []struct{ name, payload string }{
		{"null-object", "null"},
		{"empty-object", "{}"},
		{"missing-x", `{"y":13}`},
		{"missing-y", `{"x":-4}`},
		{"null-x", `{"x":null,"y":13}`},
		{"null-y", `{"x":-4,"y":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider.mu.Lock()
			provider.listOverride = tc.payload
			provider.mu.Unlock()
			x, y, err := manager.CursorPosition(context.Background())
			if err == nil {
				t.Fatalf("accepted malformed cursor payload %q as %d,%d", tc.payload, x, y)
			}
		})
	}
}
func TestKWinCursorAcceptsValidZeroAndNegativeCoordinates(t *testing.T) {
	manager, provider := newSyntheticKWinManager(t)
	for _, tc := range []struct {
		payload string
		x, y    int
	}{
		{`{"x":0,"y":0}`, 0, 0},
		{`{"x":-23,"y":107}`, -23, 107},
	} {
		provider.mu.Lock()
		provider.listOverride = tc.payload
		provider.mu.Unlock()
		x, y, err := manager.CursorPosition(context.Background())
		if err != nil || x != tc.x || y != tc.y {
			t.Fatalf("payload=%q got=(%d,%d) err=%v", tc.payload, x, y, err)
		}
	}
}
