package window

import (
	"context"
	"strconv"
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

func TestGnomeManagerRejectsIDsThatLoseJavaScriptPrecision(t *testing.T) {
	manager := &GnomeManager{}
	id := strconv.FormatUint(maxGnomeJSSafeInteger+1, 10)

	err := manager.actOnWindowByID(context.Background(), id, `w.activate()`)
	if err == nil || !strings.Contains(err.Error(), "JavaScript safe integer range") {
		t.Fatalf("actOnWindowByID(%q) error = %v, want safe-integer error", id, err)
	}
}
