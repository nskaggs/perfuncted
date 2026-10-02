package window

import (
	"context"
	"errors"
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

// A handle that no longer names a window must be reported as ErrWindowNotFound,
// the same sentinel InfoByID and every other backend use. Callers decide whether
// to retry on it, so a generic script failure is not enough.
func TestGnomeActOnWindowByIDReportsMissingWindow(t *testing.T) {
	var script string
	manager := &GnomeManager{evalJS: func(_ context.Context, js string) (string, error) {
		script = js
		return gnomeWindowMissing, nil
	}}

	for _, action := range []string{
		`w.activate(global.get_current_time())`,
		`w.move_frame(true, 1, 2)`,
		`w.delete(global.get_current_time())`,
	} {
		script = ""
		err := manager.actOnWindowByID(context.Background(), "17", action)
		if !errors.Is(err, ErrWindowNotFound) {
			t.Fatalf("actOnWindowByID(%q) error = %v, want ErrWindowNotFound", action, err)
		}

		// The snippet must return the marker before running the action, so a
		// window that exists still has its action applied.
		if !strings.Contains(script, strconv.Quote(gnomeWindowMissing)) {
			t.Fatalf("script does not return the missing marker:\n%s", script)
		}
		if strings.Contains(script, "throw") {
			t.Fatalf("script still signals a missing window by throwing text:\n%s", script)
		}
		marker := strings.Index(script, "return "+strconv.Quote(gnomeWindowMissing))
		at := strings.Index(script, action)
		if marker < 0 || at < 0 || marker > at {
			t.Fatalf("script must check for a missing window before the action %q:\n%s", action, script)
		}
	}
}

// A window that exists must succeed, and a failure of the action itself must
// stay distinguishable from a missing window.
func TestGnomeActOnWindowByIDSeparatesActionFailureFromMissingWindow(t *testing.T) {
	manager := &GnomeManager{evalJS: func(context.Context, string) (string, error) {
		return "ok", nil
	}}
	if err := manager.actOnWindowByID(context.Background(), "17", `w.activate()`); err != nil {
		t.Fatalf("actOnWindowByID on an existing window = %v, want nil", err)
	}

	failing := &GnomeManager{evalJS: func(context.Context, string) (string, error) {
		return "", errors.New("gnome: eval failed: w is not a function")
	}}
	err := failing.actOnWindowByID(context.Background(), "17", `w.nope()`)
	if err == nil {
		t.Fatal("a failing action reported success")
	}
	if errors.Is(err, ErrWindowNotFound) {
		t.Fatalf("an action failure was reported as a missing window: %v", err)
	}
}

// The marker must not be reachable as a normal result, so an action that
// legitimately returns it cannot be confused with a missing window.
func TestGnomeWindowMissingMarkerIsDistinctFromTheSuccessResult(t *testing.T) {
	if gnomeWindowMissing == "ok" {
		t.Fatal("the missing-window marker collides with the success result")
	}
}
