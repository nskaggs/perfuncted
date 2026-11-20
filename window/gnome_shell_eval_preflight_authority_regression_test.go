//go:build linux

package window

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A canceled caller must not open a compositor connection even when a real
// bus address is supplied. A missing address must never silently fall back to
// the ambient desktop's default session bus.
func TestGnomeShellEvalConstructorRejectsCanceledOrUnspecifiedBus(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if manager, err := NewGnomeManagerForBusContext(canceled, "unix:path=/not-present/perfuncted-gnome-bus"); manager != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled caller started GNOME bus discovery: manager=%v err=%v", manager, err)
	}
	if manager, err := NewGnomeManagerForBusContext(context.Background(), ""); manager != nil || err == nil || !strings.Contains(err.Error(), "session bus unset") {
		t.Fatalf("missing private bus fell back to ambient session: manager=%v err=%v", manager, err)
	}
}
