package screen

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// A missing capture backend has no diagnostic state and must not attempt
// initialization, even if callers exercise optional methods through a typed
// nil interface value.
func TestNilPortalBackendDiagnosticsAndConfigurationFailClosed(t *testing.T) {
	var backend *PortalDBusBackend
	backend.SetCaptureTimeout(time.Second)
	backend.recordFailure(errors.New("private screenshot name should not be retained"))
	if got := backend.Diagnostics(); !reflect.DeepEqual(got, []string{"backend unavailable"}) {
		t.Fatalf("unavailable portal exposed diagnostic state: %q", got)
	}
	// A nil error should never overwrite the last meaningful observation.
	live := &PortalDBusBackend{}
	live.recordFailure(errors.New("synthetic capture failure"))
	live.recordFailure(nil)
	for _, line := range live.Diagnostics() {
		if line == "last failure: synthetic capture failure" {
			return
		}
	}
	t.Fatalf("nil error erased portal failure: %q", live.Diagnostics())
}

// Cancellation before portal setup must take precedence over a missing or
// invalid bus address and must not create any D-Bus connection.
func TestPortalBackendConstructionRejectsPreCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, addr := range []string{"", "unix:path=/nonexistent/perfuncted-portal.sock"} {
		backend, err := NewPortalDBusBackendForBusContext(ctx, addr)
		if backend != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled portal construction leaked backend=%v error=%v address=%q", backend, err, addr)
		}
	}
}
