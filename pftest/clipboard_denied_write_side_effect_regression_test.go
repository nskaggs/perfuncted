package pftest_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/pftest"
)

// A failed clipboard write must not silently change the fixture's selection;
// otherwise tests of denied or failed clipboard operations can observe data
// that a real backend did not accept.
func TestFixtureClipboardDeniedSetPreservesPreviousSelection(t *testing.T) {
	denied := errors.New("synthetic clipboard permission denied")
	cb := &pftest.Clipboard{Text: "previous selection", SetErr: denied}
	if err := cb.Set(context.Background(), "secret replacement"); !errors.Is(err, denied) {
		t.Fatalf("denied clipboard write error=%v, want %v", err, denied)
	}
	if got, err := cb.Get(context.Background()); err != nil || got != "previous selection" {
		t.Fatalf("denied clipboard write changed selection to %q (read err=%v)", got, err)
	}
	cb.SetErr = nil
	if err := cb.Set(context.Background(), "authorized replacement"); err != nil {
		t.Fatal(err)
	}
	if got, err := cb.Get(context.Background()); err != nil || got != "authorized replacement" {
		t.Fatalf("authorized clipboard write lost selection: %q, %v", got, err)
	}
}
