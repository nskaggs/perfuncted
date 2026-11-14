package window

import (
	"context"
	"strings"
	"testing"
)

// An unavailable X11 backend must not panic or try to access connection
// state when an application attempts operations during startup/shutdown.
func TestX11NilBackendRejectsAdmittedOperationsWithoutPanic(t *testing.T) {
	var backend *X11Backend
	if err := backend.Close(); err != nil {
		t.Fatalf("nil backend Close: %v", err)
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("repeated nil backend Close: %v", err)
	}
	if title, err := backend.ActiveTitle(context.Background()); title != "" || err == nil || !strings.Contains(err.Error(), "backend is nil") {
		t.Fatalf("nil backend leaked title or did not fail: title=%q err=%v", title, err)
	}
	if err := backend.Sync(context.Background()); err == nil || !strings.Contains(err.Error(), "backend is nil") {
		t.Fatalf("nil backend Sync succeeded: %v", err)
	}
}
