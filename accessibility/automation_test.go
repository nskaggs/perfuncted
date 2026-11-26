package accessibility

import (
	"context"
	"errors"
	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/v2/internal/dbusutil"
	"math"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestActionInvocationErrorRetainsDispatchCertaintyAndCause(t *testing.T) {
	cause := errors.New("provider disconnected")
	for _, outcome := range []DispatchOutcome{DispatchNotSent, DispatchAccepted, DispatchRejected, DispatchUnknown} {
		t.Run(string(outcome), func(t *testing.T) {
			invocation := &ActionInvocationError{Dispatch: outcome, Err: cause}
			if !errors.Is(invocation, cause) {
				t.Fatal("provider cause lost through error wrapping")
			}
			if !strings.Contains(invocation.Error(), string(outcome)) || !strings.Contains(invocation.Error(), cause.Error()) {
				t.Fatalf("error lost dispatch certainty or cause: %q", invocation.Error())
			}
		})
	}
	var nilInvocation *ActionInvocationError
	if nilInvocation.Unwrap() != nil {
		t.Fatal("nil error unwrap must be nil")
	}
	if got := nilInvocation.Error(); got == "" {
		t.Fatal("nil error must retain useful description")
	}
}

// Action outcomes are not interchangeable: a transport failure or malformed
// provider reply cannot be represented as a known rejection or as success.
func TestAccessibilityActionProviderFailureAndDispatchClassification(t *testing.T) { //nolint:gocyclo // independent adversarial response cases check dispatch certainty and the exact provider error.
	id := NodeID{BusName: "org.example.Widget", ObjectPath: "/action", Generation: 12}
	providerErr := errors.New("synthetic provider went away")
	tests := []struct {
		name     string
		reply    any
		replyErr error
		expected DispatchOutcome
		cause    error
		fragment string
	}{
		{name: "explicit refusal", reply: false, expected: DispatchRejected, cause: ErrMutationRejected},
		{name: "malformed integer reply", reply: int32(1), expected: DispatchUnknown, fragment: "malformed"},
		{name: "missing reply", expected: DispatchUnknown, fragment: "malformed"},
		{name: "transport failure", replyErr: providerErr, expected: DispatchUnknown, cause: providerErr},
		{name: "unknown interface", replyErr: errors.New("org.freedesktop.DBus.Error.UnknownInterface"), expected: DispatchUnknown, cause: ErrUnsupported},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			backend := &dbusBackend{generation: 12, callOverride: func(_ context.Context, _ NodeID, method string, args []any) (any, error) {
				if method != actionIface+".DoAction" || len(args) != 1 || args[0] != int32(5) {
					t.Fatalf("unexpected action request: %s %#v", method, args)
				}
				calls++
				return tc.reply, tc.replyErr
			}}
			err := backend.dispatchAction(context.Background(), id, Action{Index: 5, Name: "save"})
			var invocation *ActionInvocationError
			if !errors.As(err, &invocation) || invocation.Dispatch != tc.expected || invocation.Action.Index != 5 {
				t.Fatalf("dispatch outcome=%v, want %s", err, tc.expected)
			}
			if tc.cause != nil && !errors.Is(err, tc.cause) {
				t.Fatalf("provider cause %v lost: %v", tc.cause, err)
			}
			if tc.fragment != "" && !strings.Contains(err.Error(), tc.fragment) {
				t.Fatalf("provider invalid response not described: %v", err)
			}
			if calls != 1 {
				t.Fatalf("action dispatched %d times", calls)
			}
		})
	}
	t.Run("no dispatch on stale handle", func(t *testing.T) {
		calls := 0
		backend := &dbusBackend{generation: 13, callOverride: func(context.Context, NodeID, string, []any) (any, error) { calls++; return true, nil }}
		err := backend.dispatchAction(context.Background(), id, Action{Index: 5})
		var invocation *ActionInvocationError
		if !errors.As(err, &invocation) || invocation.Dispatch != DispatchNotSent || !errors.Is(err, ErrStaleNode) || calls != 0 {
			t.Fatalf("stale handle dispatched unexpectedly: %v (calls=%d)", err, calls)
		}
	})
	t.Run("no dispatch on canceled context", func(t *testing.T) {
		calls := 0
		backend := &dbusBackend{generation: 12, callOverride: func(context.Context, NodeID, string, []any) (any, error) { calls++; return true, nil }}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := backend.dispatchAction(ctx, id, Action{Index: 5})
		var invocation *ActionInvocationError
		if !errors.As(err, &invocation) || invocation.Dispatch != DispatchNotSent || !errors.Is(err, context.Canceled) || calls != 0 {
			t.Fatalf("canceled handle dispatched unexpectedly: %v (calls=%d)", err, calls)
		}
	})
}

func TestAccessibilityActionMetadataRejectsMalformedProviderPayloads(t *testing.T) {
	id := NodeID{BusName: "org.example.Widget", ObjectPath: "/action", Generation: 12}
	for _, tc := range []struct {
		method string
		result any
		invoke func(*dbusBackend) error
	}{
		{method: actionIface + ".GetActions", result: []string{"save"}, invoke: func(b *dbusBackend) error { _, err := b.actionMetadata(context.Background(), id); return err }},
		{method: actionIface + ".GetName", result: int32(5), invoke: func(b *dbusBackend) error { _, err := b.actionName(context.Background(), id, 0); return err }},
	} {
		t.Run(tc.method, func(t *testing.T) {
			b := &dbusBackend{generation: 12, callOverride: func(_ context.Context, _ NodeID, method string, _ []any) (any, error) {
				if method != tc.method {
					t.Fatalf("unexpected request %s", method)
				}
				return tc.result, nil
			}}
			if err := tc.invoke(b); err == nil || !strings.Contains(err.Error(), "malformed") {
				t.Fatalf("invalid metadata accepted: %v", err)
			}
		})
	}
}

// The selected action's metadata is not a valid result once its invocation
// loses authorization at the dispatch boundary. Preserve the cancellation
// error but never publish the selected private action to an abandoned caller.
func TestCanceledActionDispatchWithholdsSelectedMetadata(t *testing.T) {
	id := NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/dialog/private", Generation: 9}
	for _, tc := range []struct {
		name   string
		invoke func(*dbusBackend, context.Context) (Action, error)
	}{
		{"by name", func(b *dbusBackend, ctx context.Context) (Action, error) {
			return b.InvokeActionByName(ctx, id, "save")
		}},
		{"default", func(b *dbusBackend, ctx context.Context) (Action, error) { return b.InvokeDefaultAction(ctx, id) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			b := &dbusBackend{generation: 9, callOverride: func(_ context.Context, _ NodeID, method string, _ []any) (any, error) {
				switch method {
				case actionIface + ".GetActions":
					return []actionMetadataWire{{LocalizedName: "Private save", Description: "private document", KeyBinding: "Ctrl+S"}}, nil
				case actionIface + ".GetName":
					return "save", nil
				case actionIface + ".DoAction":
					calls++
					cancel()
					return true, nil
				default:
					t.Fatalf("unexpected method %q", method)
					return nil, nil
				}
			}}
			action, err := tc.invoke(b, ctx)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled invocation accepted: action=%+v err=%v", action, err)
			}
			if action != (Action{}) {
				t.Fatalf("canceled invocation exposed selected metadata: %+v", action)
			}
			if calls != 1 {
				t.Fatalf("expected exactly one attempted dispatch, got %d", calls)
			}
		})
	}
}

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

// Check every public mutation's representability boundary at the provider
// dispatch seam. These invalid requests must be rejected before any AT-SPI
// wire operation, even if the provider would otherwise acknowledge them.
func TestATSPIMutationPreflightRejectsUnrepresentableCommandsWithoutDispatch(t *testing.T) {
	id := NodeID{BusName: "org.test.App", ObjectPath: "/widget", Generation: 3}
	high := int(math.MaxInt32) + 1
	low := int(math.MinInt32) - 1
	cases := []struct {
		name     string
		invoke   func(*dbusBackend) error
		fragment string
	}{
		{"scroll type out of enum", func(b *dbusBackend) error { return b.ScrollTo(context.Background(), id, ScrollType(700)) }, "invalid scroll type"},
		{"scroll point invalid coordinate enum", func(b *dbusBackend) error { return b.ScrollToPoint(context.Background(), id, CoordType(90), 1, 2) }, "invalid coordinate type"},
		{"scroll point x underflow", func(b *dbusBackend) error { return b.ScrollToPoint(context.Background(), id, CoordTypeScreen, low, 3) }, "out of range"},
		{"scroll point y overflow", func(b *dbusBackend) error { return b.ScrollToPoint(context.Background(), id, CoordTypeScreen, 3, high) }, "out of range"},
		{"position coordinate enum", func(b *dbusBackend) error { return b.SetPosition(context.Background(), id, 1, 2, CoordType(100)) }, "invalid coordinate type"},
		{"position negative overflow", func(b *dbusBackend) error { return b.SetPosition(context.Background(), id, low, 1, CoordTypeParent) }, "out of range"},
		{"position positive overflow", func(b *dbusBackend) error { return b.SetPosition(context.Background(), id, 1, high, CoordTypeParent) }, "out of range"},
		{"size negative width", func(b *dbusBackend) error { return b.SetSize(context.Background(), id, -1, 200) }, "size out of range"},
		{"size positive overflow", func(b *dbusBackend) error { return b.SetSize(context.Background(), id, high, 200) }, "size out of range"},
		{"extents invalid enum", func(b *dbusBackend) error {
			return b.SetExtents(context.Background(), id, 1, 2, 300, 200, CoordType(99))
		}, "invalid coordinate type"},
		{"extents negative height", func(b *dbusBackend) error {
			return b.SetExtents(context.Background(), id, 1, 2, 300, -1, CoordTypeScreen)
		}, "extents out of range"},
		{"extents x overflow", func(b *dbusBackend) error {
			return b.SetExtents(context.Background(), id, high, 2, 300, 200, CoordTypeWindow)
		}, "extents out of range"},
		{"paste negative position", func(b *dbusBackend) error { return b.PasteText(context.Background(), id, -1) }, "invalid paste position"},
		{"caret negative offset", func(b *dbusBackend) error { return b.SetCaretOffset(context.Background(), id, -1) }, "invalid caret offset"},
		{"text selection negative id", func(b *dbusBackend) error { return b.SetTextSelection(context.Background(), id, -1, 0, 3) }, "invalid text selection"},
		{"text selection reverse offsets", func(b *dbusBackend) error { return b.SetTextSelection(context.Background(), id, 0, 5, 3) }, "before start"},
		{"add selection negative", func(b *dbusBackend) error { return b.AddTextSelection(context.Background(), id, -1, 3) }, "invalid text selection"},
		{"add selection reverse", func(b *dbusBackend) error { return b.AddTextSelection(context.Background(), id, 5, 3) }, "before start"},
		{"remove selection negative", func(b *dbusBackend) error { return b.RemoveTextSelection(context.Background(), id, -1) }, "invalid selection index"},
		{"delete text reverse", func(b *dbusBackend) error { return b.DeleteText(context.Background(), id, 5, 3) }, "invalid text range"},
		{"copy text reverse", func(b *dbusBackend) error { return b.CopyText(context.Background(), id, 5, 3) }, "invalid text range"},
		{"cut text reverse", func(b *dbusBackend) error { return b.CutText(context.Background(), id, 5, 3) }, "invalid text range"},
		{"set documents empty", func(b *dbusBackend) error { return b.SetTextSelections(context.Background(), id, nil) }, "must not be empty"},
		{"set documents invalid start", func(b *dbusBackend) error {
			return b.SetTextSelections(context.Background(), id, []DocumentTextSelection{{StartObject: NodeID{BusName: id.BusName, ObjectPath: "/bad", Generation: 99}, EndObject: id}})
		}, "start"},
		{"set documents invalid end", func(b *dbusBackend) error {
			return b.SetTextSelections(context.Background(), id, []DocumentTextSelection{{StartObject: id, EndObject: NodeID{BusName: id.BusName, ObjectPath: "/bad", Generation: 99}}})
		}, "end"},
		{"set documents negative offset", func(b *dbusBackend) error {
			return b.SetTextSelections(context.Background(), id, []DocumentTextSelection{{StartObject: id, EndObject: id, StartOffset: -1}})
		}, "negative offset"},
		{"set documents reverse same-node", func(b *dbusBackend) error {
			return b.SetTextSelections(context.Background(), id, []DocumentTextSelection{{StartObject: id, EndObject: id, StartOffset: 7, EndOffset: 2}})
		}, "before start"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			b := &dbusBackend{generation: 3, callOverride: func(context.Context, NodeID, string, []any) (any, error) { calls++; return true, nil }}
			err := tc.invoke(b)
			if err == nil || !strings.Contains(err.Error(), tc.fragment) {
				t.Fatalf("got %v, want error containing %q", err, tc.fragment)
			}
			if calls != 0 {
				t.Fatalf("invalid request dispatched %d provider mutations", calls)
			}
		})
	}
}

// A failure during command processing must not be disguised as a successful
// accepted mutation, and should retain unsupported/error identity.
func TestATSPIMutationProviderFailuresAndCanceledOperationsRemainObservable(t *testing.T) {
	id := NodeID{BusName: "org.test.App", ObjectPath: "/widget", Generation: 3}
	providerErr := errors.New("synthetic AT-SPI provider rejects mutation")
	for _, tc := range []struct {
		name   string
		invoke func(*dbusBackend) error
	}{
		{"grab focus", func(b *dbusBackend) error { return b.GrabFocus(context.Background(), id) }},
		{"set value", func(b *dbusBackend) error { return b.SetValue(context.Background(), id, 1.25) }},
		{"scroll to", func(b *dbusBackend) error { return b.ScrollTo(context.Background(), id, ScrollTopLeft) }},
		{"text insertion", func(b *dbusBackend) error { return b.InsertText(context.Background(), id, 0, "abcd") }},
		{"selection", func(b *dbusBackend) error { return b.SelectChild(context.Background(), id, 0) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			count := 0
			b := &dbusBackend{generation: 3, callOverride: func(context.Context, NodeID, string, []any) (any, error) { count++; return nil, providerErr }}
			if err := tc.invoke(b); !errors.Is(err, providerErr) {
				t.Fatalf("provider error identity was lost: %v", err)
			}
			if count != 1 {
				t.Fatalf("provider dispatched %d times", count)
			}
		})
	}
}

// A D-Bus transport can report a successful mutation after the operation's
// context expires. That result is no longer authoritative for callers.
func TestAccessibilitySetValueRejectsCancellationDuringProviderReply(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	backend := &dbusBackend{generation: 7, callOverride: func(ctx context.Context, id NodeID, method string, args []any) (any, error) {
		if method != propertiesIface+".Set" {
			t.Fatalf("unexpected wire operation %s", method)
		}
		cancel()
		return nil, nil
	}}
	id := NodeID{BusName: "org.example.Widget", ObjectPath: "/entry", Generation: 7}
	err := backend.SetValue(ctx, id, 3.14)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SetValue accepted successful reply after operation canceled: %v", err)
	}
}

func TestAccessibilityActionMetadataCannotOutrunCancellation(t *testing.T) {
	id := NodeID{BusName: "org.example.Widget", ObjectPath: "/entry", Generation: 7}
	for _, tc := range []struct {
		name   string
		action string
		reply  any
		invoke func(*dbusBackend, context.Context) error
	}{
		{"metadata", actionIface + ".GetActions", []actionMetadataWire{{LocalizedName: "Activate"}}, func(b *dbusBackend, ctx context.Context) error { _, err := b.actionMetadata(ctx, id); return err }},
		{"machine name", actionIface + ".GetName", "activate", func(b *dbusBackend, ctx context.Context) error { _, err := b.actionName(ctx, id, 0); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			b := &dbusBackend{generation: 7, callOverride: func(_ context.Context, _ NodeID, method string, _ []any) (any, error) {
				if method != tc.action {
					t.Fatalf("unexpected action read %s", method)
				}
				cancel()
				return tc.reply, nil
			}}
			if err := tc.invoke(b, ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("action read accepted late canceled response: %v", err)
			}
		})
	}
}

// CopyText is a mutation despite its read-like name. A successful provider
// response may not acknowledge a copy after the caller cancels the operation.
func TestATSPICopyTextRejectsLateCanceledProviderAcknowledgment(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	id := NodeID{BusName: "org.example.Editor", ObjectPath: "/text", Generation: 8}
	calls := 0
	b := &dbusBackend{generation: 8, callOverride: func(_ context.Context, got NodeID, method string, args []any) (any, error) {
		calls++
		if got != id || method != editableTextIface+".CopyText" || len(args) != 2 || args[0] != int32(0) || args[1] != int32(9) {
			t.Fatalf("untrusted wire dispatch: id=%+v method=%q args=%v", got, method, args)
		}
		cancel()
		return nil, nil
	}}
	if err := b.CopyText(ctx, id, 0, 9); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled copy acknowledged: %v", err)
	}
	if calls != 1 {
		t.Fatalf("copy dispatch count=%d want exactly one", calls)
	}
}

// A stale generation cannot authorize any private action metadata query or
// actual mutation, even when the provider mock would accept the operation.
func TestATSPIStaleActionHandlesRejectBeforeProviderDispatch(t *testing.T) {
	stale := NodeID{BusName: "org.example.Editor", ObjectPath: "/private", Generation: 6}
	count := 0
	b := &dbusBackend{generation: 7, callOverride: func(context.Context, NodeID, string, []any) (any, error) { count++; return true, nil }}
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		invoke func() error
	}{
		{"copy text", func() error { return b.CopyText(ctx, stale, 0, 1) }},
		{"focus", func() error { return b.GrabFocus(ctx, stale) }},
		{"action metadata", func() error { _, err := b.actionMetadata(ctx, stale); return err }},
		{"action name", func() error { _, err := b.actionName(ctx, stale, 0); return err }},
		{"action dispatch", func() error { return b.dispatchAction(ctx, stale, Action{Index: 0, LocalizedName: "Private"}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.invoke(); !errors.Is(err, ErrStaleNode) {
				t.Fatalf("stale handle returned %v", err)
			}
			if count != 0 {
				t.Fatalf("stale handle dispatched %d requests", count)
			}
		})
	}
}

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

// Internal mutation and metadata helpers must reject a missing operation
// context before consulting the provider or publishing private action data.
func TestATSPIInternalMutationAndActionMetadataRejectNilContextBeforeProvider(t *testing.T) {
	id := NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/editor/private", Generation: 15}
	calls := 0
	b := &dbusBackend{generation: 15, callOverride: func(context.Context, NodeID, string, []any) (any, error) {
		calls++
		return true, nil
	}}
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"mutation", func() error { return b.mutation(nil, id, componentIface, "GrabFocus") }},
		{"boolean mutation", func() error { return b.mutationBool(nil, id, componentIface, "GrabFocus") }},
		{"action metadata", func() error { _, err := b.actionMetadata(nil, id); return err }},
		{"action name", func() error { _, err := b.actionName(nil, id, 0); return err }},
		{"value mutation", func() error { return b.SetValue(nil, id, 2) }},
		{"document selection", func() error { return b.SetTextSelections(nil, id, nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			if err == nil || !strings.Contains(err.Error(), "nil context") {
				t.Fatalf("missing context passed preflight: %v", err)
			}
		})
	}
	if calls != 0 {
		t.Fatalf("missing context dispatched %d provider operations", calls)
	}
}

// A request that already lost its lifetime cannot validate or dispatch
// provider mutations, even when the payload is malformed. Cancellation
// must be the authoritative failure at every public mutation boundary.
func TestATSPIInvalidMutationPayloadCannotOverridePriorCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	id := NodeID{BusName: "org.example.App", ObjectPath: "/widget", Generation: 4}
	calls := 0
	b := &dbusBackend{generation: 4, callOverride: func(context.Context, NodeID, string, []any) (any, error) { calls++; return true, nil }}
	cases := []struct {
		name string
		call func(context.Context) error
	}{
		{"invalid scroll enum", func(ctx context.Context) error { return b.ScrollTo(ctx, id, ScrollType(900)) }},
		{"negative size", func(ctx context.Context) error { return b.SetSize(ctx, id, -5, 10) }},
		{"NUL text", func(ctx context.Context) error { return b.SetTextContents(ctx, id, "hidden\x00text") }},
		{"negative insertion offset", func(ctx context.Context) error { return b.InsertText(ctx, id, -1, "hidden") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("pre-canceled mutation returned %v", err)
			}
			if err := tc.call(nil); err == nil {
				t.Fatal("nil context accepted")
			}
		})
	}
	if calls != 0 {
		t.Fatalf("canceled mutations dispatched %d provider operations", calls)
	}
}

type actionWireFake struct {
	mu             sync.Mutex
	accept         bool
	unknown        bool
	metadataDenied bool
	nameDenied     bool
	actions        []int32
	focusCalls     int
	copied         bool
	values         []float64
}

func (f *actionWireFake) GetActions() ([]actionMetadataWire, *dbus.Error) {
	f.mu.Lock()
	denied := f.metadataDenied
	f.mu.Unlock()
	if denied {
		return nil, dbus.MakeFailedError(errors.New("metadata denied"))
	}
	return []actionMetadataWire{{LocalizedName: "Localized Save", Description: "Save document", KeyBinding: "Ctrl+S"}, {LocalizedName: "Localized Open", Description: "Open", KeyBinding: "Ctrl+O"}}, nil
}
func (f *actionWireFake) GetName(index int32) (string, *dbus.Error) {
	f.mu.Lock()
	denied := f.nameDenied
	f.mu.Unlock()
	if denied {
		return "", dbus.MakeFailedError(errors.New("name denied"))
	}
	if index == 0 {
		return "save", nil
	}
	if index == 1 {
		return "open", nil
	}
	return "", dbus.MakeFailedError(errors.New("unknown action"))
}
func (f *actionWireFake) DoAction(index int32) (bool, *dbus.Error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actions = append(f.actions, index)
	if f.unknown {
		return false, dbus.MakeFailedError(errors.New("remote action rejected"))
	}
	return f.accept, nil
}
func (f *actionWireFake) GrabFocus() (bool, *dbus.Error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.focusCalls++
	if f.unknown {
		return false, dbus.MakeFailedError(errors.New("remote component rejected"))
	}
	return f.accept, nil
}
func (f *actionWireFake) CopyText(start, end int32) *dbus.Error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if start == 0 && end == 2 {
		f.copied = true
	}
	if f.unknown {
		return dbus.MakeFailedError(errors.New("remote clipboard denied"))
	}
	return nil
}
func (f *actionWireFake) Set(iface, name string, value dbus.Variant) *dbus.Error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if iface != valueIface || name != "CurrentValue" {
		return dbus.MakeFailedError(errors.New("wrong property"))
	}
	v, ok := value.Value().(float64)
	if !ok {
		return dbus.MakeFailedError(errors.New("wrong value"))
	}
	f.values = append(f.values, v)
	if f.unknown {
		return dbus.MakeFailedError(errors.New("remote property refused"))
	}
	return nil
}

func newPrivateATSPIActionWireFixture(t *testing.T) (*dbusBackend, *actionWireFake, NodeID) {
	t.Helper()

	service, b, _ := startSyntheticEventBus(t, nil)
	reply, err := service.RequestName("org.test.Application", dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("synthetic application name: reply=%v err=%v", reply, err)
	}
	fixture := &actionWireFake{accept: true}
	path := dbus.ObjectPath("/synthetic/accessibility/node")
	for _, iface := range []string{actionIface, componentIface, editableTextIface, propertiesIface} {
		if exportErr := service.Export(fixture, path, iface); exportErr != nil {
			t.Fatalf("export %s: %v", iface, exportErr)
		}
	}
	id := NodeID{BusName: "org.test.Application", ObjectPath: string(path), Generation: b.Generation(), Incarnation: 1}
	return b, fixture, id
}

func TestATSPIActionComponentAndPropertyMutationsAcrossPrivateDBus(t *testing.T) {
	b, fixture, id := newPrivateATSPIActionWireFixture(t)
	t.Run("success", func(t *testing.T) { assertPrivateATSPIWireSuccess(t, b, fixture, id) })
	t.Run("rejected", func(t *testing.T) { assertPrivateATSPIWireRejected(t, b, fixture, id) })
	t.Run("transport errors", func(t *testing.T) { assertPrivateATSPIWireErrors(t, b, fixture, id) })
	t.Run("metadata errors", func(t *testing.T) { assertPrivateATSPIWireMetadataErrors(t, b, fixture, id) })
}

func assertPrivateATSPIWireSuccess(t *testing.T, b *dbusBackend, fixture *actionWireFake, id NodeID) {
	t.Helper()
	ctx := context.Background()

	chosen, err := b.InvokeDefaultAction(ctx, id)
	if err != nil || chosen.Name != "save" || chosen.Index != 0 || chosen.LocalizedName != "Localized Save" {
		t.Fatalf("first authorized action: %+v err=%v", chosen, err)
	}
	chosen, err = b.InvokeActionByName(ctx, id, "OPEN")
	if err != nil || chosen.Index != 1 || chosen.Name != "open" {
		t.Fatalf("named action: %+v err=%v", chosen, err)
	}
	if err := b.GrabFocus(ctx, id); err != nil {
		t.Fatalf("authorized component focus: %v", err)
	}
	if err := b.CopyText(ctx, id, 0, 2); err != nil {
		t.Fatalf("authorized clipboard copy: %v", err)
	}
	if err := b.SetValue(ctx, id, 1.25); err != nil {
		t.Fatalf("authorized property mutation: %v", err)
	}
	assertPrivateATSPIWireSuccessfulState(t, fixture)
}

func assertPrivateATSPIWireSuccessfulState(t *testing.T, fixture *actionWireFake) {
	t.Helper()
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.actions) != 2 || fixture.actions[0] != 0 || fixture.actions[1] != 1 || fixture.focusCalls != 1 || !fixture.copied || len(fixture.values) != 1 || fixture.values[0] != 1.25 {
		t.Fatalf("wire mutation sequence invalid: actions=%v focus=%d copied=%v values=%v", fixture.actions, fixture.focusCalls, fixture.copied, fixture.values)
	}
}

func assertPrivateATSPIWireRejected(t *testing.T, b *dbusBackend, fixture *actionWireFake, id NodeID) {
	t.Helper()
	ctx := context.Background()
	fixture.mu.Lock()
	fixture.accept = false
	fixture.mu.Unlock()

	if err := b.GrabFocus(ctx, id); !errors.Is(err, ErrMutationRejected) {
		t.Fatalf("component rejected false was accepted: %v", err)
	}
	if err := b.InvokeAction(ctx, id, 0); !errors.Is(err, ErrMutationRejected) {
		t.Fatalf("action rejected false was accepted: %v", err)
	}
}

func assertPrivateATSPIWireErrors(t *testing.T, b *dbusBackend, fixture *actionWireFake, id NodeID) {
	t.Helper()
	ctx := context.Background()
	fixture.mu.Lock()
	fixture.unknown = true
	fixture.mu.Unlock()

	if err := b.GrabFocus(ctx, id); err == nil || !strings.Contains(err.Error(), "remote component rejected") {
		t.Fatalf("remote component error lost: %v", err)
	}
	if err := b.CopyText(ctx, id, 0, 2); err == nil || !strings.Contains(err.Error(), "remote clipboard denied") {
		t.Fatalf("remote clipboard error lost: %v", err)
	}
	if err := b.SetValue(ctx, id, 2); err == nil || !strings.Contains(err.Error(), "remote property refused") {
		t.Fatalf("remote property error lost: %v", err)
	}
	if err := b.InvokeAction(ctx, id, 1); err == nil || !strings.Contains(err.Error(), "remote action rejected") {
		t.Fatalf("remote action failure lost: %v", err)
	}
}

func assertPrivateATSPIWireMetadataErrors(t *testing.T, b *dbusBackend, fixture *actionWireFake, id NodeID) {
	t.Helper()
	ctx := context.Background()
	fixture.mu.Lock()
	fixture.metadataDenied = true
	fixture.unknown = false
	fixture.mu.Unlock()

	if _, err := b.InvokeDefaultAction(ctx, id); err == nil || !strings.Contains(err.Error(), "metadata denied") {
		t.Fatalf("denied metadata read accepted: %v", err)
	}
	fixture.mu.Lock()
	fixture.metadataDenied = false
	fixture.nameDenied = true
	fixture.mu.Unlock()
	if _, err := b.InvokeActionByName(ctx, id, "save"); err == nil || !strings.Contains(err.Error(), "name denied") {
		t.Fatalf("denied name read accepted: %v", err)
	}
}

// A provider may apply an AT-SPI mutation before its D-Bus reply reaches the
// caller. The backend must report cancellation and preserve dispatch uncertainty.
func TestSetValueReportsCancellationAfterRealProviderAppliesValue(t *testing.T) {
	backend, id, provider := newOptionalATSPINode(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	provider.mu.Lock()
	provider.cancelMutation = cancel
	provider.mu.Unlock()

	err := backend.SetValue(ctx, id, 42.75)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SetValue acknowledged late provider reply: %v", err)
	}
	provider.mu.RLock()
	got := provider.values[valueIface+".CurrentValue"]
	lastMutation := provider.lastMutation
	provider.mu.RUnlock()
	if got != 42.75 || lastMutation != "set-value" {
		t.Fatalf("provider mutation was not observed before cancellation: value=%v mutation=%q", got, lastMutation)
	}
}

func TestInvokeActionReportsUnknownAfterRealProviderCancelsReply(t *testing.T) {
	backend, id, provider := newOptionalATSPINode(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	provider.mu.Lock()
	provider.cancelAction = cancel
	provider.mu.Unlock()

	action, err := backend.InvokeActionByName(ctx, id, "press")
	var dispatch *ActionInvocationError
	if !errors.Is(err, context.Canceled) || !errors.As(err, &dispatch) || dispatch.Dispatch != DispatchUnknown {
		t.Fatalf("canceled provider action result=%+v err=%v, want unknown dispatch and context cancellation", action, err)
	}
	if action != (Action{}) {
		t.Fatalf("canceled action exposed provider metadata: %+v", action)
	}
	provider.mu.RLock()
	count, last := provider.actionCount, provider.lastAction
	provider.mu.RUnlock()
	if count != 1 || last != 0 {
		t.Fatalf("provider action was not dispatched exactly once: count=%d index=%d", count, last)
	}
}

// A caller that has already canceled must not run parameter or handle
// validation, nor expose whether a provider has accepted the request.
func TestATSPIRemainingMutationEntryPointsRejectCanceledCallerBeforeValidation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	id := NodeID{BusName: "org.example.App", ObjectPath: "/widget", Generation: 4}
	calls := 0
	b := &dbusBackend{generation: 4, callOverride: func(context.Context, NodeID, string, []any) (any, error) { calls++; return true, nil }}
	cases := []struct {
		name string
		run  func(context.Context) error
	}{
		{"scroll point", func(c context.Context) error { return b.ScrollToPoint(c, id, CoordType(100), 0, 0) }},
		{"position", func(c context.Context) error { return b.SetPosition(c, id, 0, 0, CoordType(100)) }},
		{"extents", func(c context.Context) error { return b.SetExtents(c, id, 0, 0, 10, -1, CoordTypeScreen) }},
		{"delete text", func(c context.Context) error { return b.DeleteText(c, id, 4, 3) }},
		{"copy text", func(c context.Context) error { return b.CopyText(c, id, 4, 3) }},
		{"cut text", func(c context.Context) error { return b.CutText(c, id, 4, 3) }},
		{"paste text", func(c context.Context) error { return b.PasteText(c, id, -1) }},
		{"caret", func(c context.Context) error { return b.SetCaretOffset(c, id, -1) }},
		{"set selection", func(c context.Context) error { return b.SetTextSelection(c, id, -1, 0, 1) }},
		{"add selection", func(c context.Context) error { return b.AddTextSelection(c, id, 4, 3) }},
		{"remove selection", func(c context.Context) error { return b.RemoveTextSelection(c, id, -1) }},
		{"document selections", func(c context.Context) error { return b.SetTextSelections(c, id, nil) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled request returned %v", err)
			}
			if err := tc.run(nil); err == nil {
				t.Fatal("nil context accepted")
			}
		})
	}
	if calls != 0 {
		t.Fatalf("invalid canceled requests dispatched %d provider calls", calls)
	}
}

// A selection mutation with a canceled caller must not evaluate invalid
// indices or dispatch a provider command.
func TestATSPISelectionMutationsHonorPriorCancellationBeforeIndexValidation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	id := NodeID{BusName: "org.example.App", ObjectPath: "/widget", Generation: 4}
	calls := 0
	b := &dbusBackend{generation: 4, callOverride: func(context.Context, NodeID, string, []any) (any, error) { calls++; return true, nil }}
	cases := []struct {
		name string
		run  func(context.Context) error
	}{
		{"select child", func(c context.Context) error { return b.SelectChild(c, id, -1) }},
		{"deselect child", func(c context.Context) error { return b.DeselectChild(c, id, -1) }},
		{"select row", func(c context.Context) error { return b.SelectRow(c, id, -1) }},
		{"deselect row", func(c context.Context) error { return b.DeselectRow(c, id, -1) }},
		{"select column", func(c context.Context) error { return b.SelectColumn(c, id, -1) }},
		{"deselect column", func(c context.Context) error { return b.DeselectColumn(c, id, -1) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled mutation returned %v", err)
			}
		})
	}
	if calls != 0 {
		t.Fatalf("canceled mutations dispatched %d provider commands", calls)
	}
}

// A finite SetValue request is not authority to mutate an obsolete or
// disconnected AT-SPI node. Reject revoked handles before invoking the
// provider, even through the testable mutation transport override.
func TestSetValueRefusesRetiredHandlesBeforeProviderDispatch(t *testing.T) {
	live := NodeID{BusName: "org.example.Widget", ObjectPath: "/entry", Generation: 7}
	stale := live
	stale.Generation = 6
	malformed := live
	malformed.ObjectPath = "not-an-object-path"
	for _, tc := range []struct {
		name                 string
		id                   NodeID
		closed, disconnected bool
		want                 error
	}{
		{name: "stale generation", id: stale, want: ErrStaleNode},
		{name: "malformed path", id: malformed, want: ErrStaleNode},
		{name: "closed backend", id: live, closed: true, want: ErrDisconnected},
		{name: "disconnected backend", id: live, disconnected: true, want: ErrDisconnected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			b := &dbusBackend{
				generation:   7,
				closed:       tc.closed,
				disconnected: tc.disconnected,
				callOverride: func(context.Context, NodeID, string, []any) (any, error) {
					calls++
					return nil, nil
				},
			}
			err := b.SetValue(context.Background(), tc.id, 3.25)
			if !errors.Is(err, tc.want) || calls != 0 {
				t.Fatalf("retired handle mutation result=%v, want %v; provider dispatch count=%d", err, tc.want, calls)
			}
		})
	}
}

type malformedActionReply struct {
	mu      sync.Mutex
	indices []int32
}

type cancelMutationReply struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	calls  int
}

func (p *cancelMutationReply) CopyText(_, _ int32) *dbus.Error {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	p.cancel()
	return nil
}

func (p *cancelMutationReply) GrabFocus() (bool, *dbus.Error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	p.cancel()
	return true, nil
}

func (p *malformedActionReply) DoAction(index int32) (string, *dbus.Error) {
	p.mu.Lock()
	p.indices = append(p.indices, index)
	p.mu.Unlock()
	return "not a boolean", nil
}

func TestMutationCancellationAfterProviderReceivesRequest(t *testing.T) {
	for _, method := range []string{"copy", "focus"} {
		t.Run(method, func(t *testing.T) {
			service, backend, _ := startSyntheticEventBus(t, nil)
			reply, err := service.RequestName("org.test.Application", dbus.NameFlagDoNotQueue)
			if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
				t.Fatalf("own test application name: %v %v", reply, err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			provider := &cancelMutationReply{cancel: cancel}
			path := dbus.ObjectPath("/synthetic/mutation")
			for _, iface := range []string{editableTextIface, componentIface} {
				if err := service.Export(provider, path, iface); err != nil {
					t.Fatal(err)
				}
			}
			id := NodeID{BusName: "org.test.Application", ObjectPath: string(path), Generation: backend.Generation(), Incarnation: 1}
			if method == "copy" {
				err = backend.CopyText(ctx, id, 0, 2)
			} else {
				err = backend.GrabFocus(ctx, id)
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("provider-side cancellation acknowledged as success: %v", err)
			}
			provider.mu.Lock()
			calls := provider.calls
			provider.mu.Unlock()
			if calls != 1 {
				t.Fatalf("provider received %d mutations, want one", calls)
			}
		})
	}
}

func TestActionDispatchReportsUnknownForMalformedOrFailedWireReply(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		name := "provider failure"
		if malformed {
			name = "malformed reply"
		}
		t.Run(name, func(t *testing.T) {
			service, backend, _ := startSyntheticEventBus(t, nil)
			reply, err := service.RequestName("org.test.Application", dbus.NameFlagDoNotQueue)
			if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
				t.Fatalf("own test application name: %v %v", reply, err)
			}
			path := dbus.ObjectPath("/synthetic/action")
			id := NodeID{BusName: "org.test.Application", ObjectPath: string(path), Generation: backend.Generation(), Incarnation: 1}
			var replacement *malformedActionReply
			var provider *actionWireFake
			if malformed {
				replacement = &malformedActionReply{}
				err = service.Export(replacement, path, actionIface)
			} else {
				provider = &actionWireFake{unknown: true}
				err = service.Export(provider, path, actionIface)
			}
			if err != nil {
				t.Fatal(err)
			}
			action := Action{Index: 1, Name: "open"}
			err = backend.dispatchAction(context.Background(), id, action)
			var invocation *ActionInvocationError
			if !errors.As(err, &invocation) || invocation.Dispatch != DispatchUnknown || invocation.Action != action {
				t.Fatalf("wire reply did not preserve dispatch uncertainty: %v", err)
			}
			if malformed {
				replacement.mu.Lock()
				indices := append([]int32(nil), replacement.indices...)
				replacement.mu.Unlock()
				if len(indices) != 1 || indices[0] != action.Index {
					t.Fatalf("unexpected action dispatch: %v", indices)
				}
			} else {
				provider.mu.Lock()
				indices := append([]int32(nil), provider.actions...)
				provider.mu.Unlock()
				if !strings.Contains(err.Error(), "remote action rejected") || len(indices) != 1 || indices[0] != action.Index {
					t.Fatalf("provider failure or dispatch lost: indices=%v err=%v", indices, err)
				}
			}
		})
	}
}

type qtInsertTextCall struct {
	offset int32
	text   string
	length int32
}

type qtInsertTextProvider struct {
	application objectRef
	calls       chan qtInsertTextCall
}

func (p *qtInsertTextProvider) GetApplication() (objectRef, *dbus.Error) {
	return p.application, nil
}

func (p *qtInsertTextProvider) InsertText(offset int32, text string, length int32) (bool, *dbus.Error) {
	p.calls <- qtInsertTextCall{offset: offset, text: text, length: length}
	return true, nil
}

type qtToolkitProperties struct{}

func (qtToolkitProperties) Get(iface, name string) (dbus.Variant, *dbus.Error) {
	if iface != "org.a11y.atspi.Application" || name != "ToolkitName" {
		return dbus.Variant{}, dbus.MakeFailedError(errors.New("unexpected application property read"))
	}
	return dbus.MakeVariant("Qt"), nil
}

// Qt's D-Bus InsertText length is UTF-16 units, so verify the toolkit lookup
// and conversion through the real private-bus mutation call.
func TestInsertTextUsesQtWireLengthOnPrivateDBus(t *testing.T) {
	address := startTestDBus(t)
	service, err := dbusutil.ConnectContext(context.Background(), address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	reply, err := service.RequestName(registryName, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("register private AT-SPI provider: %v %v", reply, err)
	}

	const (
		applicationPath = dbus.ObjectPath("/org/a11y/atspi/accessible/qt_application")
		nodePath        = dbus.ObjectPath("/org/a11y/atspi/accessible/qt_editor")
	)
	application := objectRef{BusName: registryName, ObjectPath: applicationPath}
	provider := &qtInsertTextProvider{application: application, calls: make(chan qtInsertTextCall, 1)}
	for _, iface := range []string{accessibleIface, editableTextIface} {
		if err := service.Export(provider, nodePath, iface); err != nil {
			t.Fatalf("export %s: %v", iface, err)
		}
	}
	if err := service.Export(qtToolkitProperties{}, applicationPath, propertiesIface); err != nil {
		t.Fatalf("export application properties: %v", err)
	}
	client, err := dbusutil.ConnectContext(context.Background(), address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	backend := &dbusBackend{generation: 1, access: client, toolkits: make(map[string]string)}
	id := backend.refID(objectRef{BusName: registryName, ObjectPath: nodePath})

	text := "é😀"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := backend.InsertText(ctx, id, 7, text); err != nil {
		t.Fatalf("insert through private AT-SPI provider: %v", err)
	}
	select {
	case call := <-provider.calls:
		if call.offset != 7 || call.text != text || call.length != 3 {
			t.Fatalf("provider received InsertText(%d, %q, %d), want offset 7 and UTF-16 length 3", call.offset, call.text, call.length)
		}
	case <-ctx.Done():
		t.Fatalf("provider did not receive InsertText: %v", ctx.Err())
	}
}

func TestMutationsCannotReportSuccessAfterCallerCancelsWhileProviderReplies(t *testing.T) {
	id := NodeID{BusName: "org.test", ObjectPath: "/node", Generation: 1}
	for _, tc := range []struct {
		name   string
		run    func(*dbusBackend, context.Context) error
		method string
	}{
		{"focus", func(b *dbusBackend, ctx context.Context) error { return b.GrabFocus(ctx, id) }, componentIface + ".GrabFocus"},
		{"set text", func(b *dbusBackend, ctx context.Context) error { return b.SetTextContents(ctx, id, "hello") }, editableTextIface + ".SetTextContents"},
		{"action", func(b *dbusBackend, ctx context.Context) error { return b.InvokeAction(ctx, id, 0) }, actionIface + ".DoAction"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			mutations := 0
			b := &dbusBackend{generation: 1, callOverride: func(_ context.Context, _ NodeID, method string, _ []any) (any, error) {
				if method == actionIface+".GetActions" {
					return []actionMetadataWire{{LocalizedName: "activate"}}, nil
				}
				if method != tc.method {
					t.Fatalf("unexpected method %q", method)
				}
				mutations++
				cancel()
				return true, nil
			}}
			err := tc.run(b, ctx)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("operation reported success or wrong error after cancellation: %v", err)
			}
			if mutations != 1 {
				t.Fatalf("mutation calls=%d, want one", mutations)
			}
			if tc.name == "action" {
				var dispatch *ActionInvocationError
				if !errors.As(err, &dispatch) || dispatch.Dispatch != DispatchUnknown {
					t.Fatalf("canceled action should preserve uncertain dispatch: %v", err)
				}
			}
		})
	}
}

// Provider mutation validation precedes dispatch. Non-finite values and
// malformed strings cannot be represented as meaningful AT-SPI state and
// must not reach D-Bus even when a faulty provider would acknowledge them.
func TestAccessibilityMutationRejectsNonFiniteValuesAndMalformedTextBeforeWire(t *testing.T) {
	id := NodeID{BusName: "org.example.Widget", ObjectPath: "/entry", Generation: 7}
	for _, tc := range []struct {
		name string
		run  func(*dbusBackend) error
	}{
		{"NaN value", func(b *dbusBackend) error { return b.SetValue(context.Background(), id, math.NaN()) }},
		{"positive infinite value", func(b *dbusBackend) error { return b.SetValue(context.Background(), id, math.Inf(1)) }},
		{"negative infinite value", func(b *dbusBackend) error { return b.SetValue(context.Background(), id, math.Inf(-1)) }},
		{"NUL in full text", func(b *dbusBackend) error { return b.SetTextContents(context.Background(), id, "safe\x00hidden") }},
		{"invalid UTF8 in full text", func(b *dbusBackend) error { return b.SetTextContents(context.Background(), id, "\xff") }},
		{"NUL in inserted text", func(b *dbusBackend) error { return b.InsertText(context.Background(), id, 0, "safe\x00hidden") }},
		{"invalid UTF8 in inserted text", func(b *dbusBackend) error { return b.InsertText(context.Background(), id, 0, "\xff") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			b := &dbusBackend{generation: 7, callOverride: func(context.Context, NodeID, string, []any) (any, error) { calls++; return true, nil }}
			if err := tc.run(b); err == nil {
				t.Error("malformed mutation accepted")
			}
			if calls != 0 {
				t.Errorf("malformed mutation reached provider %d times", calls)
			}
		})
	}
}

func TestAccessibilityMutationAcceptsFiniteValuesAndUnicodeText(t *testing.T) {
	id := NodeID{BusName: "org.example.Widget", ObjectPath: "/entry", Generation: 7}
	calls := 0
	b := &dbusBackend{generation: 7, callOverride: func(context.Context, NodeID, string, []any) (any, error) { calls++; return true, nil }}
	if err := b.SetValue(context.Background(), id, -123.456); err != nil {
		t.Fatal(err)
	}
	if err := b.SetTextContents(context.Background(), id, "Café 日本"); err != nil {
		t.Fatal(err)
	}
	if err := b.InsertText(context.Background(), id, 0, "Café"); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 permitted mutations, got %d", calls)
	}
}

// A provider's malformed self-referential relations cannot substitute a
// target's own text for the independent label evidence requested by a caller.
func TestSemanticSelectorRequiresDistinctLabelNode(t *testing.T) {
	targetID := NodeID{BusName: "org.synthetic.App", ObjectPath: "/input", Generation: 8}
	labelID := NodeID{BusName: targetID.BusName, ObjectPath: "/label", Generation: 8}
	target := Node{ID: targetID, Role: "entry", Name: "Account password", Relations: map[string][]NodeID{"labelled-by": {targetID}, "label-for": {targetID}}}
	selector := Selector{Role: "entry", Label: "password"}
	if got := FilterSnapshotSelector(Snapshot{Nodes: []Node{target}}, selector); len(got) != 0 {
		t.Fatalf("self-referential label accepted target name as label evidence: %+v", got)
	}
	// A label-for edge on the target itself must not count as a reverse label.
	target.Relations = map[string][]NodeID{"label-for": {targetID}}
	if got := FilterSnapshotSelector(Snapshot{Nodes: []Node{target}}, selector); len(got) != 0 {
		t.Fatalf("self-referential label-for edge accepted target name: %+v", got)
	}
	// A separate accessible label remains sufficient in either direction.
	target.Relations = map[string][]NodeID{"labelled-by": {labelID}}
	label := Node{ID: labelID, Role: "label", Text: "Password field"}
	if got := FilterSnapshotSelector(Snapshot{Nodes: []Node{target, label}}, selector); len(got) != 1 || got[0].ID != targetID {
		t.Fatalf("valid external labelled-by evidence rejected: %+v", got)
	}
	target.Relations = nil
	label.Relations = map[string][]NodeID{"label-for": {targetID}}
	if got := FilterSnapshotSelector(Snapshot{Nodes: []Node{target, label}}, selector); len(got) != 1 || got[0].ID != targetID {
		t.Fatalf("valid external label-for evidence rejected: %+v", got)
	}
}

func TestFilterSnapshotSelectorResolvesLabelAndOrderedAncestors(t *testing.T) {
	root := NodeID{BusName: "org.test", ObjectPath: "/root", Generation: 4}
	form := NodeID{BusName: "org.test", ObjectPath: "/form", Generation: 4}
	label := NodeID{BusName: "org.test", ObjectPath: "/label", Generation: 4}
	field := NodeID{BusName: "org.test", ObjectPath: "/field", Generation: 4}
	snapshot := Snapshot{Root: Node{ID: root}, Nodes: []Node{
		{ID: root, Role: "application"},
		{ID: form, Parent: root, Role: "group", Name: "Account"},
		{ID: label, Parent: form, Role: "label", Name: "Email address", Relations: map[string][]NodeID{"label-for": {field}}},
		{ID: field, Parent: form, Role: "text input", States: []string{"enabled", "editable"}},
	}}
	got := FilterSnapshotSelector(snapshot, Selector{
		Role:   "input",
		Label:  "email",
		States: []string{"enabled", "editable"},
		Ancestors: []AncestorSelector{
			{Role: "group", Name: "account"},
			{Role: "application"},
		},
	})
	if len(got) != 1 || got[0].ID != field {
		t.Fatalf("selector matches = %+v, want field %v", got, field)
	}
}

func TestValidateSemanticSnapshotRejectsIncompleteTargetEvidence(t *testing.T) {
	tests := []struct {
		name     string
		snapshot Snapshot
		selector Selector
	}{
		{name: "truncated", snapshot: Snapshot{Truncated: true}},
		{name: "provider error", snapshot: Snapshot{ProviderErrors: 1}},
		{name: "tree warning", snapshot: Snapshot{ProviderErrors: 1, Warnings: []string{"/child: child disappeared"}}},
		{
			name:     "selector name warning",
			snapshot: snapshotWithNodeWarning(accessibleIface + ".Name: name read failed"),
			selector: Selector{Name: "save"},
		},
		{
			name:     "selector state warning",
			snapshot: snapshotWithNodeWarning(accessibleIface + ".GetState: state read failed"),
			selector: Selector{States: []string{"enabled"}},
		},
		{
			name:     "selector text warning",
			snapshot: snapshotWithNodeWarning(textIface + ": text read failed"),
			selector: Selector{Text: "save"},
		},
		{
			name:     "selector attributes warning",
			snapshot: snapshotWithNodeWarning(accessibleIface + ".GetAttributes: attributes read failed"),
			selector: Selector{Attributes: map[string]string{"kind": "primary"}},
		},
		{
			name:     "label relation warning",
			snapshot: snapshotWithNodeWarning(accessibleIface + ".GetRelationSet: relation read failed"),
			selector: Selector{Label: "email"},
		},
		{
			name:     "label text warning",
			snapshot: snapshotWithNodeWarning(textIface + ": text read failed"),
			selector: Selector{Label: "email"},
		},
		{
			name:     "label interfaces warning",
			snapshot: snapshotWithNodeWarning(accessibleIface + ".GetInterfaces: interfaces read failed"),
			selector: Selector{Label: "email"},
		},
		{
			name:     "ancestor name warning",
			snapshot: snapshotWithNodeWarning(accessibleIface + ".Name: name read failed"),
			selector: Selector{Ancestors: []AncestorSelector{{Name: "dialog"}}},
		},
		{
			name:     "child count warning",
			snapshot: snapshotWithNodeWarning(accessibleIface + ".ChildCount: child count read failed"),
			selector: Selector{Role: "button"},
		},
		{
			name:     "inconsistent diagnostics",
			snapshot: Snapshot{ProviderErrors: 2, Warnings: []string{"/child: child disappeared"}},
		},
		{name: "text truncation", snapshot: Snapshot{Nodes: []Node{{TextTruncated: true}}}, selector: Selector{Text: "hidden suffix"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateSemanticSnapshot(test.snapshot, test.selector); !errors.Is(err, ErrIncompleteSnapshot) {
				t.Fatalf("ValidateSemanticSnapshot error = %v, want ErrIncompleteSnapshot", err)
			}
		})
	}
}

func TestValidateSemanticSnapshotIgnoresWarningsOutsideSelectorEvidence(t *testing.T) {
	tests := []struct {
		name     string
		warning  string
		selector Selector
	}{
		{name: "optional value", warning: valueIface + ".MinimumValue: unsupported", selector: Selector{Role: "button"}},
		{name: "optional action", warning: actionIface + ".GetActions: unsupported", selector: Selector{Role: "button"}},
		{name: "optional selection", warning: selectionIface + ".GetSelectedChildCount: unsupported", selector: Selector{Role: "button"}},
		{name: "optional table", warning: tableIface + ".NRows: unsupported", selector: Selector{Role: "button"}},
		{name: "optional document", warning: documentIface + ": no readable document properties", selector: Selector{Role: "button"}},
		{name: "unused name", warning: accessibleIface + ".Name: unsupported", selector: Selector{Role: "button"}},
		{name: "unused state", warning: accessibleIface + ".GetState: unsupported", selector: Selector{Role: "button"}},
		{name: "unused attributes", warning: accessibleIface + ".GetAttributes: unsupported", selector: Selector{Role: "button"}},
		{name: "unused interfaces", warning: accessibleIface + ".GetInterfaces: unsupported", selector: Selector{Role: "button"}},
		{name: "text interface for role", warning: textIface + ": unsupported", selector: Selector{Role: "button"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateSemanticSnapshot(snapshotWithNodeWarning(test.warning), test.selector); err != nil {
				t.Fatalf("ValidateSemanticSnapshot = %v, want complete selector evidence", err)
			}
		})
	}
}

func TestValidateSemanticSnapshotRejectsLabelTextTruncation(t *testing.T) {
	snapshot := Snapshot{Nodes: []Node{{TextTruncated: true}}}
	if err := ValidateSemanticSnapshot(snapshot, Selector{Label: "email"}); !errors.Is(err, ErrIncompleteSnapshot) {
		t.Fatalf("ValidateSemanticSnapshot error = %v, want ErrIncompleteSnapshot", err)
	}
}

func snapshotWithNodeWarning(warning string) Snapshot {
	return Snapshot{
		Nodes:          []Node{{Role: "button", Warnings: []string{warning}}},
		ProviderErrors: 1,
		Warnings:       []string{warning},
	}
}

func TestValidateSemanticSnapshotAllowsTextTruncationWithoutTextSelector(t *testing.T) {
	if err := ValidateSemanticSnapshot(Snapshot{Nodes: []Node{{TextTruncated: true}}}, Selector{Role: "button"}); err != nil {
		t.Fatalf("ValidateSemanticSnapshot = %v, want complete role observation", err)
	}
}

func TestFilterSnapshotAdversarialSearchDoesNotOvermatch(t *testing.T) {
	root := NodeID{BusName: "org.example.App", ObjectPath: "/app", Generation: 7}
	form := NodeID{BusName: root.BusName, ObjectPath: "/form", Generation: 7}
	field := NodeID{BusName: root.BusName, ObjectPath: "/email", Generation: 7}
	label := NodeID{BusName: root.BusName, ObjectPath: "/emailLabel", Generation: 7}
	secret := NodeID{BusName: root.BusName, ObjectPath: "/secret", Generation: 7}
	snap := Snapshot{Nodes: []Node{
		{ID: root, Role: "application", Name: "Mail"},
		{ID: form, Parent: root, Role: "group", Name: "New message"},
		{ID: label, Parent: form, Role: "label", Name: "Recipient address", Relations: map[string][]NodeID{"label-for": {field}}},
		{ID: field, Parent: form, Role: "text input", Name: "Recipient", Text: "recipient@example.com", States: []string{"editable", "enabled"}, Attributes: map[string]string{"autocomplete": "EMAIL"}},
		{ID: secret, Parent: form, Role: "text input", Name: "Password", Redacted: true, Text: "", States: []string{"protected"}},
	}}
	cases := []struct {
		name  string
		query Query
		count int
		first NodeID
	}{
		{"exact case-insensitive role", Query{Role: "APPLICATION"}, 1, root},
		{"name case-insensitive substring", Query{Name: "RECIP", Role: "text input"}, 1, field},
		{"text substring", Query{Text: "@EXAMPLE."}, 1, field},
		{"state and attribute", Query{States: []string{"EDITABLE", "enabled"}, Attributes: map[string]string{"AUTOComplete": "email"}}, 1, field},
		{"missing required state", Query{States: []string{"editable", "focused"}}, 0, NodeID{}},
		{"missing attribute", Query{Attributes: map[string]string{"autocomplete": "telephone"}}, 0, NodeID{}},
		{"redacted contents not searchable", Query{Text: "password secret"}, 0, NodeID{}},
		{"no implicit partial attribute matching", Query{Attributes: map[string]string{"autocomplete": "em"}}, 0, NodeID{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			found := FilterSnapshot(snap, tc.query)
			if len(found) != tc.count {
				t.Fatalf("query %+v matched %d nodes (%+v), want %d", tc.query, len(found), found, tc.count)
			}
			if tc.count == 1 && found[0].ID != tc.first {
				t.Fatalf("query %+v selected wrong target %v", tc.query, found[0].ID)
			}
		})
	}
	if got := FilterSnapshotSelector(snap, Selector{Role: "input", Label: "recipient", Ancestors: []AncestorSelector{{Role: "group", Name: "new message"}, {Role: "application", Name: "mail"}}}); len(got) != 1 || got[0].ID != field {
		t.Fatalf("label plus ordered ancestors resolved wrong target: %+v", got)
	}
	if got := FilterSnapshotSelector(snap, Selector{Role: "input", Label: "recipient", Ancestors: []AncestorSelector{{Role: "application"}, {Role: "group"}}}); len(got) != 0 {
		t.Fatalf("reversed ancestry unexpectedly matched: %+v", got)
	}
	if got := FilterSnapshotSelector(snap, Selector{Label: "not a label"}); len(got) != 0 {
		t.Fatalf("unknown label unexpectedly matched: %+v", got)
	}
}

func TestAccessibilityToolkitCacheEnforcesHandleEpochAndIsolation(t *testing.T) {
	b := &dbusBackend{generation: 11}
	node := NodeID{BusName: "org.example.App", ObjectPath: "/node", Generation: 11, Incarnation: 1}
	if got, ok, err := b.cachedToolkit(node); err != nil || ok || got != "" {
		t.Fatalf("uncached toolkit = %q, %t, %v", got, ok, err)
	}
	if err := b.cacheToolkit(node, "GTK"); err != nil {
		t.Fatalf("cacheToolkit: %v", err)
	}
	if got, ok, err := b.cachedToolkit(node); err != nil || !ok || got != "GTK" {
		t.Fatalf("cached toolkit = %q, %t, %v", got, ok, err)
	}
	alias := NodeID{BusName: "org.example.Other", ObjectPath: "/node", Generation: 11, Incarnation: 1}
	if got, ok, err := b.cachedToolkit(alias); err != nil || ok || got != "" {
		t.Fatalf("toolkit leaked across application buses: %q %t %v", got, ok, err)
	}
	b.Invalidate(node)
	if _, ok, err := b.cachedToolkit(node); !errors.Is(err, ErrStaleNode) || ok {
		t.Fatalf("stale generation reused toolkit after invalidation: %v", err)
	}
	if err := b.cacheToolkit(node, "stale"); !errors.Is(err, ErrStaleNode) {
		t.Fatalf("stale generation accepted cache write: %v", err)
	}
	fresh := NodeID{BusName: node.BusName, ObjectPath: node.ObjectPath, Generation: 12, Incarnation: 1}
	if got, ok, err := b.cachedToolkit(fresh); err != nil || ok || got != "" {
		t.Fatalf("invalidation failed to clear toolkit: %q %t %v", got, ok, err)
	}
	if err := b.cacheToolkit(fresh, "Qt"); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := b.cachedToolkit(fresh); err != nil || !ok || got != "Qt" {
		t.Fatalf("new generation toolkit not loaded: %q %t %v", got, ok, err)
	}
}

func TestAccessibilityParentCacheRejectsStaleAndForeignHandles(t *testing.T) {
	b := &dbusBackend{generation: 4}
	parent := NodeID{BusName: "org.example.App", ObjectPath: "/parent", Generation: 4}
	child := NodeID{BusName: "org.example.App", ObjectPath: "/parent/child", Generation: 4}
	b.recordObjectParent(child, parent)
	identity := objectIdentity{busName: child.BusName, objectPath: child.ObjectPath}
	if p, ok := b.parents[identity]; !ok || p.objectPath != parent.ObjectPath {
		t.Fatalf("parent relation not cached: %+v", b.parents)
	}
	b.recordObjectParent(NodeID{BusName: child.BusName, ObjectPath: "/invalid", Generation: 3}, parent)
	b.recordObjectParent(child, NodeID{})
	if len(b.parents) != 1 {
		t.Fatalf("stale/invalid node altered ancestry tracking: %+v", b.parents)
	}
	b.Invalidate(parent)
	if len(b.parents) != 0 {
		t.Fatalf("cached parents survived epoch change: %+v", b.parents)
	}
	b.recordObjectParent(child, parent)
	if len(b.parents) != 0 {
		t.Fatalf("stale parent handles restored old ancestry: %+v", b.parents)
	}
}

// When AT-SPI reports more characters than the client's read budget, that
// prefix is incomplete evidence. A matching text selector must not treat an
// omitted suffix as a verified negative result.
func TestTextProviderCharacterLimitMarksSemanticEvidenceTruncated(t *testing.T) {
	const original = "start of document with a hidden important suffix"
	backend, root, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(_ *syntheticATSPINode, frame *syntheticATSPINode) {
		frame.text = original
	})
	node, err := backend.readNodeFresh(context.Background(), root, NodeID{}, 8, true)
	if err != nil {
		t.Fatalf("read node: %v", err)
	}
	if node.Text != original[:8] || !node.TextTruncated {
		t.Fatalf("provider text prefix=%q truncated=%t, want bounded incomplete evidence", node.Text, node.TextTruncated)
	}
	snap := Snapshot{Nodes: []Node{node}, Root: node, Generation: root.Generation}
	if err := ValidateSemanticSnapshot(snap, Selector{Text: "hidden important suffix"}); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("text selector silently accepted truncated evidence: %v", err)
	}
}

// Ordinary complete text must not be labeled incomplete merely because it
// exactly matches the configured read limit; empty providers are complete too.
func TestTextProviderCompleteCharacterCountRemainsAuthoritative(t *testing.T) {
	for _, tc := range []struct {
		name, text    string
		limit         int
		wantTruncated bool
	}{
		{name: "text exactly at budget", text: "eight123", limit: 8},
		{name: "short text", text: "okay", limit: 8},
		{name: "empty text", text: "", limit: 8},
		{name: "prefix exceeds budget", text: "longer than max", limit: 5, wantTruncated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, root, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(_ *syntheticATSPINode, frame *syntheticATSPINode) { frame.text = tc.text })
			node, err := backend.readNodeFresh(context.Background(), root, NodeID{}, tc.limit, true)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			expected := tc.text
			if len(expected) > tc.limit {
				expected = expected[:tc.limit]
			}
			if node.Text != expected || node.TextTruncated != tc.wantTruncated {
				t.Fatalf("got text=%q truncated=%t, want %q %t", node.Text, node.TextTruncated, expected, tc.wantTruncated)
			}
		})
	}
}

func TestTextReadReportsMissingProviderMethodWithDefaultLimit(t *testing.T) {
	backend, id, provider := newOptionalATSPINode(t)
	provider.mu.Lock()
	provider.values[textIface+".CharacterCount"] = int32(7)
	provider.mu.Unlock()
	node := Node{}
	err := backend.readNodeText(context.Background(), id, []string{textIface}, 0, &node)
	if err == nil || !strings.Contains(err.Error(), textIface) || node.Text != "" {
		t.Fatalf("missing text method returned text=%q err=%v", node.Text, err)
	}
}
