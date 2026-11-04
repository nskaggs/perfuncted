package accessibility

import (
	"context"
	"errors"
	"strings"
	"testing"
)

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
