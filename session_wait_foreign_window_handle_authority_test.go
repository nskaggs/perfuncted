package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/window"
)

// Native window identifiers are only meaningful within their originating
// session. An identical ID on a different compositor must never authorize a
// closure proof or trigger even one backend lookup.
func TestWindowClosedRejectsForeignSessionHandleBeforeBackendLookup(t *testing.T) {
	ownerManager := newHandleWindowManager(window.Info{NativeID: "reused-id", Title: "owner window"})
	otherManager := newHandleWindowManager(window.Info{NativeID: "reused-id", Title: "different window"})
	owner := NewSessionForTesting(nil, nil, ownerManager, nil, nil)
	other := NewSessionForTesting(nil, nil, otherManager, nil, nil)
	t.Cleanup(func() { _ = owner.Close(); _ = other.Close() })
	target := &Window{id: WindowID{session: owner, native: "reused-id"}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	evidence, err := other.WaitWithEvidence(ctx, WindowClosed(target), WaitEvery(100*time.Millisecond))
	if !errors.Is(err, ErrInvalidArgument) || evidence.Evaluations != 1 || evidence.PollWakeups != 0 {
		t.Fatalf("foreign session handle not rejected immediately: evidence=%+v err=%v", evidence, err)
	}
	ok, err := WindowClosed(target).evaluate(ctx, owner)
	if err != nil || ok {
		t.Fatalf("same-owner live window incorrectly considered closed: ok=%v err=%v", ok, err)
	}
}
