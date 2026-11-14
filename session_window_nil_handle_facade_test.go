package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/window"
)

// A typed nil handle can be passed through the public window APIs. Its
// metadata accessors must return empty values, and operations must refuse
// the absent owner before touching any desktop backend.
func TestWindowAndApplicationUnownedHandleOperationsFailClosed(t *testing.T) {
	var missing *Window
	if got := missing.ID(); got != (WindowID{}) {
		t.Fatalf("nil window exposed identity: %+v", got)
	}
	if title := missing.Title(); title != "" {
		t.Fatalf("nil window exposed title: %q", title)
	}
	if cached := missing.CachedInfo(); cached != (window.Info{}) {
		t.Fatalf("nil window exposed cached info: %+v", cached)
	}
	if _, err := missing.Info(context.Background()); !errors.Is(err, ErrNilSession) {
		t.Fatalf("nil window Info returned %v", err)
	}
	if _, err := (&Window{}).Info(context.Background()); !errors.Is(err, ErrNilSession) {
		t.Fatalf("unowned window Info returned %v", err)
	}
	var windows *WindowBundle
	if got, err := windows.Wait(context.Background(), WindowMatch{}); got != nil || !errors.Is(err, ErrNilSession) {
		t.Fatalf("nil window bundle wait=%v %v", got, err)
	}
	if got, err := (&WindowBundle{}).Wait(context.Background(), WindowMatch{}); got != nil || !errors.Is(err, ErrNilSession) {
		t.Fatalf("unowned window bundle wait=%v %v", got, err)
	}
	var app *Application
	if got, err := app.WaitForWindow(context.Background(), WindowMatch{}); got != nil || !errors.Is(err, ErrNilSession) {
		t.Fatalf("nil app wait=%v %v", got, err)
	}
	if got, err := (&Application{}).WaitForWindow(context.Background(), WindowMatch{}); got != nil || !errors.Is(err, ErrNilSession) {
		t.Fatalf("unowned app wait=%v %v", got, err)
	}
}
