package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A nil parent context is a supported call path and must be replaced by a
// bounded background context rather than passed to context.WithTimeout.
func TestCapabilityOpeningNilParentStillUsesBoundedContext(t *testing.T) {
	backend := &trackedCapabilityBackend{}
	installed := false
	got, err := openCapabilityBackend[*trackedCapabilityBackend](nil, time.Second, CapabilityScreen,
		func(ctx context.Context) (*trackedCapabilityBackend, error) {
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) <= 0 {
				return nil, errors.New("nil parent did not receive a live startup deadline")
			}
			return backend, nil
		}, nil,
		func(value *trackedCapabilityBackend) { installed = value == backend },
	)
	if err != nil || got != backend || !installed || backend.closes != 0 {
		t.Fatalf("nil-parent capability result %v, err=%v installed=%v closes=%d", got, err, installed, backend.closes)
	}
}

// Capability setup can be called before a screen backend exists. No timeout
// mutation may be attempted on a missing backend.
func TestConfigureScreenTimeoutAcceptsMissingBackend(t *testing.T) {
	configureScreenBackendTimeout(nil, time.Second)
}

// PID 0 and negative IDs must never be used as liveness evidence: kill(0,0)
// targets a whole process group and has different authorization semantics.
func TestSessionPIDLivenessRejectsNonpositiveIdentifiers(t *testing.T) {
	for _, pid := range []int{0, -1, -1024} {
		if pidAlive(pid) {
			t.Fatalf("invalid process identifier %d accepted as live", pid)
		}
	}
}

// A missing clipboard facade must not initiate a desktop write.
func TestNilClipboardFacadeCannotWrite(t *testing.T) {
	var clipboard *ClipboardBundle
	if err := clipboard.Set(context.Background(), "private text"); !errors.Is(err, ErrNilSession) {
		t.Fatalf("nil clipboard Set error %v, want ErrNilSession", err)
	}
}
