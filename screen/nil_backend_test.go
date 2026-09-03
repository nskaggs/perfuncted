package screen

import (
	"context"
	"image"
	"strings"
	"testing"
)

func TestWlrScreencopyNilBackendIsSafe(t *testing.T) {
	var backend *WlrScreencopyBackend
	if _, _, err := backend.ResolutionWithContext(context.Background()); err == nil || !strings.Contains(err.Error(), "backend is nil") {
		t.Fatalf("ResolutionWithContext error = %v, want nil-backend error", err)
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil", err)
	}
}

func TestExtCaptureNilBackendIsSafe(t *testing.T) {
	var backend *ExtCaptureBackend
	if _, err := backend.Grab(context.Background(), image.Rect(0, 0, 1, 1)); err == nil || !strings.Contains(err.Error(), "backend is nil") {
		t.Fatalf("Grab error = %v, want nil-backend error", err)
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil", err)
	}
}
