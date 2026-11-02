package accessibility

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/godbus/dbus/v5"
)

// Simulates the remote registry committing a RegisterEvent request at the
// same moment its caller cancels. A successful reply must not authorize
// further registrations or make the whole setup appear successful.
type cancelOnSuccessfulEventRegistry struct {
	cancel  context.CancelFunc
	invoked []string
}

func (r *cancelOnSuccessfulEventRegistry) CallWithContext(_ context.Context, _ string, _ dbus.Flags, args ...any) *dbus.Call {
	family, _ := args[0].(string)
	r.invoked = append(r.invoked, family)
	if len(r.invoked) == 1 {
		r.cancel()
	}
	return &dbus.Call{}
}
func TestEventRegistrationRejectsLateSuccessfulReplyAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	registry := &cancelOnSuccessfulEventRegistry{cancel: cancel}
	candidates := []string{"object:property-change", "window:create", "focus:focus"}
	registered, err := registerEventFamilies(ctx, registry, ":1.synthetic", candidates)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("late canceled provider acknowledgement reported setup success: %v", err)
	}
	if !reflect.DeepEqual(registry.invoked, candidates[:1]) {
		t.Fatalf("canceled setup registered additional provider families: %v", registry.invoked)
	}
	if !reflect.DeepEqual(registered, candidates[:1]) {
		t.Fatalf("ambiguous successful registration not tracked for teardown: %v", registered)
	}
}
