package accessibility

import (
	"errors"
	"strings"
	"testing"
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
