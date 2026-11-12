package accessibility

import (
	"context"
	"errors"
	"testing"
)

// An action's localized label is not its machine-readable operation name.
// An empty, absent or ambiguous name must never dispatch a different action.
func TestActionNameSelectionRejectsEmptyMissingAndAmbiguousNamesWithoutDispatch(t *testing.T) {
	id := NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/dialog/actions", Generation: 17}
	for _, tc := range []struct {
		name    string
		query   string
		machine []string
		want    error
	}{
		{"blank request", "   ", []string{"save"}, ErrNotFound},
		{"missing action", "delete", []string{"save", "cancel"}, ErrNotFound},
		{"ambiguous casefold", " SAVE ", []string{"save", "Save"}, ErrAmbiguous},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			metadataReads := 0
			nameReads := 0
			b := &dbusBackend{generation: 17, callOverride: func(_ context.Context, got NodeID, method string, args []any) (any, error) {
				if got != id {
					t.Fatalf("unexpected object: %+v", got)
				}
				switch method {
				case actionIface + ".GetActions":
					metadataReads++
					items := make([]actionMetadataWire, len(tc.machine))
					for i := range items {
						items[i] = actionMetadataWire{LocalizedName: "visible label", Description: "private metadata"}
					}
					return items, nil
				case actionIface + ".GetName":
					nameReads++
					if len(args) != 1 {
						t.Fatalf("invalid GetName args: %#v", args)
					}
					index, ok := args[0].(int32)
					if !ok || int(index) < 0 || int(index) >= len(tc.machine) {
						t.Fatalf("invalid GetName index: %#v", args)
					}
					return tc.machine[index], nil
				case actionIface + ".DoAction":
					calls++
					return true, nil
				default:
					t.Fatalf("unexpected action provider method %q", method)
					return nil, nil
				}
			}}
			chosen, err := b.InvokeActionByName(context.Background(), id, tc.query)
			if !errors.Is(err, tc.want) || chosen != (Action{}) {
				t.Fatalf("invalid action selection returned %+v %v, want %v", chosen, err, tc.want)
			}
			if calls != 0 || metadataReads != 1 {
				t.Fatalf("invalid action selection dispatched %d times, metadata reads=%d", calls, metadataReads)
			}
			if tc.name == "blank request" && nameReads != 0 {
				t.Fatalf("blank name read %d private action names", nameReads)
			}
		})
	}
}

func TestDefaultActionSelectionRejectsEmptyProviderListWithoutDispatch(t *testing.T) {
	id := NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/dialog/actions", Generation: 17}
	calls := 0
	b := &dbusBackend{generation: 17, callOverride: func(_ context.Context, _ NodeID, method string, _ []any) (any, error) {
		if method != actionIface+".GetActions" {
			calls++
			t.Fatalf("empty provider list invoked %q", method)
		}
		return []actionMetadataWire{}, nil
	}}
	chosen, err := b.InvokeDefaultAction(context.Background(), id)
	if !errors.Is(err, ErrNotFound) || chosen != (Action{}) || calls != 0 {
		t.Fatalf("empty provider authorized action %+v %v calls=%d", chosen, err, calls)
	}
}
