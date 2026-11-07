package input

import (
	"context"
	"errors"
	"testing"
)

// Cancellation is authoritative before parsing or validating a command.
// The rejected command must neither dispatch input nor disclose whether an
// otherwise unauthorised payload was syntactically valid.
func TestRemoteDesktopTypePreCanceledRejectsBeforeParsingAndValidation(t *testing.T) {
	b, _, _ := newSyntheticRevocableRemoteDesktop(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, payload := range []string{"{", "{not-a-real-key}", "private text"} {
		err := b.Type(ctx, payload)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled Type(%q) returned %v instead of cancellation", payload, err)
		}
	}
}

func TestRemoteDesktopTypeNilContextRejectsBeforeParsing(t *testing.T) {
	b, _, _ := newSyntheticRevocableRemoteDesktop(t)
	if err := b.Type(nil, "{"); err == nil || err.Error() != "input/remote-desktop: nil operation context" {
		t.Fatalf("nil-context Type returned %v", err)
	}
}
