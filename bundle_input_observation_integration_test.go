package perfuncted

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/input"
)

// This is an opt-in controlled Xvfb acceptance test, not proof of native
// physical input or cross-focus delivery by human-operated devices.
func TestInputObservationSessionOnControlledX11(t *testing.T) {
	if os.Getenv("PF_X11_OBSERVATION_INTEGRATION") != "1" {
		t.Skip("requires isolated Xvfb")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	session, err := Open(ctx, Require(CapabilityInputObservation))
	if err != nil {
		t.Fatal(err)
	}
	sub, err := session.InputObservation.Listen(ctx, input.ObservationFilter{Combo: "F8", Transition: "both"})
	if err != nil {
		_ = session.Close()
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(sub.Err(), input.ErrObservationRevoked) {
		t.Fatalf("session did not revoke owned listener: %v", sub.Err())
	}
	select {
	case _, ok := <-sub.Events():
		if ok {
			t.Fatal("delivered event after Session.Close")
		}
	case <-time.After(time.Second):
		t.Fatal("listener channel remained open after Session.Close")
	}
}
