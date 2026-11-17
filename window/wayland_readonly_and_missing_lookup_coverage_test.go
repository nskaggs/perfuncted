package window

import (
	"context"
	"errors"
	"testing"
)

func TestWaylandReadOnlyRestoreAndMissingInfoRemainFailClosed(t *testing.T) {
	t.Run("read-only ext protocol rejects restore before handle request", func(t *testing.T) {
		manager, wire, handleID := newStubWaylandManager("Viewer", false, false)
		if err := manager.RestoreByID(context.Background(), "50"); !errors.Is(err, ErrNotSupported) {
			t.Fatalf("RestoreByID error = %v, want ErrNotSupported", err)
		}
		if sawAnyHandleRequest(wire, handleID) {
			t.Fatal("read-only backend sent a handle control request")
		}
	})

	t.Run("missing info returns not found without handle request", func(t *testing.T) {
		manager, wire, handleID := newStubWaylandManager("Viewer", true, true)
		info, err := manager.InfoByID(context.Background(), "999")
		if !errors.Is(err, ErrWindowNotFound) || info != (Info{}) {
			t.Fatalf("InfoByID returned info=%+v err=%v, want empty info and ErrWindowNotFound", info, err)
		}
		if sawAnyHandleRequest(wire, handleID) {
			t.Fatal("missing lookup sent a handle request")
		}
	})
}
