package sessionregistry

import (
	"github.com/nskaggs/perfuncted/internal/workflow"
	"os"
	"testing"
	"time"
)

func workflowReceive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("workflow test timed out")
		var zero T
		return zero
	}
}

func workflowAwaitState(t *testing.T, w *workflow.Lifecycle, want workflow.State) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		_, state, changed := w.Snapshot()
		if state == want {
			return
		}
		select {
		case <-changed:
		case <-deadline:
			t.Fatalf("state %s, want %s", state, want)
		}
	}
}

func shortPrivateSocketDir(t testing.TB) string {
	t.Helper()
	dir, err := os.MkdirTemp("/var/tmp", "pf-")
	if err != nil {
		t.Fatalf("create short socket directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("remove short socket directory: %v", err)
		}
	})
	return dir
}
