package accessibility

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestATSPIFocusedTraversesPrivateBusAndRejectsIncompleteScope(t *testing.T) {
	backend, id, _ := newSyntheticATSPIBackend(t, false)
	node, err := backend.Focused(context.Background(), SnapshotOptions{Fresh: true, MaxNodes: 12, MaxDepth: 5})
	if err != nil || node.ID != id || node.Name != "Editor — document" || !node.Focused {
		t.Fatalf("private AT-SPI focused node=%+v err=%v", node, err)
	}
	if _, err := backend.Focused(context.Background(), SnapshotOptions{Fresh: true, MaxNodes: 1, MaxDepth: 5}); !errors.Is(err, ErrIncompleteSnapshot) {
		t.Fatalf("truncated focus observation must report incomplete snapshot: %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := backend.Focused(canceled, SnapshotOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Focused=%v", err)
	}
	if _, err := backend.Focused(nil, SnapshotOptions{}); err == nil {
		t.Fatal("nil Focused context accepted")
	}
	backend.Invalidate(id)
	fresh, err := backend.Focused(context.Background(), SnapshotOptions{Fresh: true, MaxDepth: 5})
	if err != nil || fresh.ID.Generation <= id.Generation {
		t.Fatalf("fresh focus lookup retained stale generation: %+v %v", fresh, err)
	}
}

func TestATSPIAtPointValidatesCoordinatesAndPrivateProviderFailure(t *testing.T) {
	backend, _, _ := newSyntheticATSPIBackend(t, false)
	if _, err := backend.AtPoint(nil, 0, 0); err == nil {
		t.Fatal("nil context accepted")
	}
	for _, tc := range []struct {
		name string
		x, y int
	}{
		{"x positive overflow", 1 << 40, 0}, {"x negative overflow", -(1 << 40), 0},
		{"y positive overflow", 0, 1 << 40}, {"y negative overflow", 0, -(1 << 40)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := backend.AtPoint(context.Background(), tc.x, tc.y); err == nil || !strings.Contains(err.Error(), "out of range") {
				t.Fatalf("invalid coordinates (%d,%d) accepted: %v", tc.x, tc.y, err)
			}
		})
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := backend.AtPoint(canceled, 10, 15); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled AtPoint=%v", err)
	}
	// The synthetic desktop intentionally does not implement hit-testing.
	// The provider error must not be replaced with a fabricated target.
	node, err := backend.AtPoint(context.Background(), 10, 15)
	if err == nil || node.ID.valid() {
		t.Fatalf("unsupported private bus hit test returned %+v err=%v", node, err)
	}
}
