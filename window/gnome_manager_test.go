package window

import (
	"context"
	"strings"
	"testing"
)

func TestGnomeManagerNilReceiverReturnsError(t *testing.T) {
	var manager *GnomeManager
	if _, err := manager.eval(context.Background(), `"ok"`); err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Fatalf("eval error = %v, want initialization error", err)
	}
}

func TestGnomeManagerNilReceiverCloseIsSafe(t *testing.T) {
	var manager *GnomeManager
	if err := manager.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil", err)
	}
}
