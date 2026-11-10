package perfuncted

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Stable failure categories must remain separate from genuine execution
// failures, even when callers add wrapping layers around backend causes.
func TestOperationErrorCategoryBoundaries(t *testing.T) {
	backendFailure := errors.New("backend I/O failed")
	for _, tc := range []struct {
		name   string
		cause  error
		failed bool
	}{
		{"backend failure", backendFailure, true},
		{"wrapped backend failure", fmt.Errorf("transport: %w", backendFailure), true},
		{"nil backend cause", nil, true},
		{"unsupported", fmt.Errorf("backend: %w", ErrUnsupported), false},
		{"unavailable", fmt.Errorf("backend: %w", ErrUnavailable), false},
		{"invalid argument", fmt.Errorf("backend: %w", ErrInvalidArgument), false},
		{"nil session", fmt.Errorf("backend: %w", ErrNilSession), false},
		{"closed session", fmt.Errorf("backend: %w", ErrSessionClosed), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := &OperationError{Capability: CapabilityInput, Operation: "Type", Err: tc.cause}
			if got := errors.Is(err, ErrOperationFailed); got != tc.failed {
				t.Fatalf("errors.Is(ErrOperationFailed) = %v, want %v", got, tc.failed)
			}
			if !strings.Contains(err.Error(), "input capability Type:") {
				t.Fatalf("operation context missing: %q", err.Error())
			}
			if tc.cause != nil && !errors.Is(err, tc.cause) {
				t.Fatalf("backend cause lost through Unwrap: %v", err)
			}
		})
	}
	var absent *OperationError
	if absent.Error() != "<nil>" || absent.Unwrap() != nil || errors.Is(absent, ErrOperationFailed) {
		t.Fatal("nil operation error must not imply a failed operation")
	}
}

func TestCapabilityErrorCategoryAndOperationContext(t *testing.T) {
	for _, tc := range []struct {
		cause  error
		target error
	}{
		{fmt.Errorf("backend: %w", ErrUnsupported), ErrUnsupported},
		{fmt.Errorf("backend: %w", ErrUnavailable), ErrUnavailable},
		{errors.New("unexpected transport failure"), nil},
	} {
		err := &CapabilityError{Capability: CapabilityClipboard, Operation: "Set", Err: tc.cause}
		if tc.target != nil && !errors.Is(err, tc.target) {
			t.Fatalf("lost category %v through %v", tc.target, err)
		}
		if tc.target == nil && (errors.Is(err, ErrUnsupported) || errors.Is(err, ErrUnavailable)) {
			t.Fatalf("transport error misclassified: %v", err)
		}
		if !errors.Is(err, tc.cause) {
			t.Fatalf("lost wrapped backend cause: %v", err)
		}
		if !strings.Contains(err.Error(), "clipboard capability Set:") {
			t.Fatalf("missing operation context: %v", err)
		}
		err.Operation = ""
		if err.Error() != "clipboard capability: "+tc.cause.Error() {
			t.Fatalf("incorrect capability-only error: %v", err)
		}
	}
	var absent *CapabilityError
	if absent.Error() != "<nil>" || absent.Unwrap() != nil || errors.Is(absent, ErrUnsupported) {
		t.Fatal("nil capability error must not imply a category")
	}
}
