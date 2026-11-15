package accessibility

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A current-generation handle is insufficient authorization when the provider
// bus has disconnected. Every mutation/action entrypoint must fail without
// reporting a successful change or dispatching a stale operation.
func TestAutomationRejectsDisconnectedProviderAcrossMutationBoundaries(t *testing.T) {
	const generation = 19
	b := &dbusBackend{generation: generation}
	id := NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/window", Generation: generation}
	action := Action{Index: 0, Name: "save"}
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"mutation", func() error { return b.mutation(context.Background(), id, componentIface, "GrabFocus") }},
		{"boolean mutation", func() error { return b.mutationBool(context.Background(), id, componentIface, "GrabFocus") }},
		{"action metadata", func() error { _, err := b.actionMetadata(context.Background(), id); return err }},
		{"action name", func() error { _, err := b.actionName(context.Background(), id, 0); return err }},
		{"invoke action", func() error { return b.InvokeAction(context.Background(), id, 0) }},
		{"direct dispatch", func() error { return b.dispatchAction(context.Background(), id, action) }},
		{"set value", func() error { return b.SetValue(context.Background(), id, 7) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, ErrDisconnected) {
				t.Fatalf("disconnected provider accepted: %v", err)
			}
		})
	}
	// A connection marked disconnected must reject document selection before
	// interpreting a request payload or encoding any endpoint identities.
	b.disconnected = true
	if err := b.SetTextSelections(context.Background(), id, nil); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("disconnected document selection accepted: %v", err)
	}

}

// Selection metadata is safe to return after an ordinary rejected operation,
// but must not be exposed when the provider never established an action name.
func TestDefaultActionDistinguishesNameReadFailureFromDispatchRejection(t *testing.T) {
	id := NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/actions", Generation: 19}
	for _, tc := range []struct {
		name     string
		denyName bool
	}{
		{"name unavailable", true},
		{"action rejected", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			b := &dbusBackend{generation: 19, callOverride: func(_ context.Context, _ NodeID, method string, _ []any) (any, error) {
				switch method {
				case actionIface + ".GetActions":
					return []actionMetadataWire{{LocalizedName: "Save document", Description: "Write current draft", KeyBinding: "Ctrl+S"}}, nil
				case actionIface + ".GetName":
					if tc.denyName {
						return nil, errors.New("synthetic machine name unavailable")
					}
					return "save", nil
				case actionIface + ".DoAction":
					calls++
					return false, nil
				default:
					t.Fatalf("unexpected provider method %s", method)
					return nil, nil
				}
			}}
			chosen, err := b.InvokeDefaultAction(context.Background(), id)
			if tc.denyName {
				if err == nil || !strings.Contains(err.Error(), "retrieve machine-readable action name") || chosen != (Action{}) || calls != 0 {
					t.Fatalf("failed name exposed action %+v, err=%v, dispatches=%d", chosen, err, calls)
				}
			} else if !errors.Is(err, ErrMutationRejected) || chosen.Name != "save" || chosen.LocalizedName != "Save document" || calls != 1 {
				t.Fatalf("rejection lost selected action %+v, err=%v, dispatches=%d", chosen, err, calls)
			}
		})
	}
}
