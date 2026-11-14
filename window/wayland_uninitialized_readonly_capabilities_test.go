package window

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// A manager with no compositor connection must not advertise window mutation
// capabilities. Its sync is a no-op, not a request to an absent display.
func TestWaylandUninitializedManagerHasReadOnlyCapabilities(t *testing.T) {
	var manager WaylandWindowManager
	want := []string{"discover", "info", "active-title"}
	if got := manager.SupportedOperations(); !reflect.DeepEqual(got, want) {
		t.Fatalf("unconnected manager advertised control capabilities: %v want %v", got, want)
	}
	if err := manager.Sync(context.Background()); err != nil {
		t.Fatalf("unconnected manager sync failed: %v", err)
	}
	var absent *WaylandWindowManager
	if got := absent.SupportedOperations(); len(got) != 0 {
		t.Fatalf("nil manager advertised capabilities: %v", got)
	}
	if err := absent.Sync(context.Background()); err == nil {
		t.Fatal("nil manager was accepted as connected")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := manager.Sync(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled no-op sync ignored revocation: %v", err)
	}
}
