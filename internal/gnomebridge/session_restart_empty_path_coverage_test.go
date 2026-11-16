package gnomebridge

import (
	"errors"
	"testing"
)

// A restart condition without an install path still communicates the actionable
// restart sentinel rather than fabricating a filesystem location.
func TestSessionRestartWithoutInstallPathPreservesActionableSentinel(t *testing.T) {
	err := &SessionRestartRequiredError{}
	if got := err.Error(); got != ErrSessionRestartRequired.Error() {
		t.Fatalf("restart without path = %q, want %q", got, ErrSessionRestartRequired.Error())
	}
	if !errors.Is(err, ErrSessionRestartRequired) || errors.Is(err, ErrProtocolMismatch) {
		t.Fatalf("restart without path lost sentinel identity: %v", err)
	}
}
