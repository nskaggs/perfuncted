package perfuncted

import (
	"errors"
	"testing"
)

// A missing caller context must be rejected before any clipboard or keyboard
// backend can dispatch a paste. Context.Background is not an implicit grant.
func TestSessionPasteRejectsNilContextBeforeDispatch(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()
	if err := session.Paste(nil, "private selection"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("paste without explicit context = %v, want ErrInvalidArgument", err)
	}
}

// An absent or only partially initialized owner never authorizes operations.
func TestSessionAbsentReceiverAndOwnerlessTraceFailClosed(t *testing.T) {
	var session *Session
	if !session.isClosed() {
		t.Fatal("nil session must be treated as closed")
	}
	var base *bundleBase
	base.traceAction("clipboard", "set") // Must not dereference a missing owner.
	orphan := &bundleBase{}
	orphan.traceAction("clipboard", "set")
}
