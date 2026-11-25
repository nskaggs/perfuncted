package perfuncted

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func newIsolatedOptionConfig() *openConfig {
	return &openConfig{
		required: make(map[Capability]struct{}),
		optional: make(map[Capability]struct{}),
	}
}

// Supplying a second desktop target must never silently replace the caller's
// already-selected target and its authoritative environment snapshot.
func TestExplicitTargetSelectionRejectsReplacement(t *testing.T) {
	original := EnvironmentTarget([]string{"XDG_SESSION_TYPE=wayland", "WAYLAND_DISPLAY=owned-socket"})
	replacement := EnvironmentTarget([]string{"XDG_SESSION_TYPE=x11", "DISPLAY=:999"})
	cfg := newIsolatedOptionConfig()
	if err := WithTarget(original)(cfg); err != nil {
		t.Fatal(err)
	}
	if err := WithTarget(replacement)(cfg); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("replacing selected target: %v, want invalid argument", err)
	}
	if cfg.target.kind != original.Kind() || cfg.target.target.Env()[1] != "WAYLAND_DISPLAY=owned-socket" {
		t.Fatalf("invalid replacement changed authoritative target: %+v", cfg.target)
	}
}

// Capability policy conflicts and unknown capability identifiers must be
// rejected instead of mutating already accepted requirements.
func TestRequiredCapabilityAdmissionRejectsConflictsAndUnknownIdentifiers(t *testing.T) {
	cfg := newIsolatedOptionConfig()
	if err := Optional(CapabilityClipboard)(cfg); err != nil {
		t.Fatal(err)
	}
	if err := Require(CapabilityClipboard)(cfg); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("required capability overwrote optional policy: %v", err)
	}
	if _, ok := cfg.required[CapabilityClipboard]; ok {
		t.Fatal("conflicted capability became required")
	}
	if _, ok := cfg.optional[CapabilityClipboard]; !ok {
		t.Fatal("conflicted capability lost optional policy")
	}
	for _, action := range []Option{Require("unknown-provider-capability"), Optional("unknown-provider-capability")} {
		if err := action(cfg); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("unknown provider capability accepted: %v", err)
		}
	}
	if len(cfg.required) != 0 || len(cfg.optional) != 1 {
		t.Fatalf("rejected capability mutated policy: required=%+v optional=%+v", cfg.required, cfg.optional)
	}
}

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

func TestDesktopTargetKindDefaultsToHostAndPreservesExplicitKind(t *testing.T) {
	if got := (DesktopTarget{}).Kind(); got != TargetHost {
		t.Fatalf("zero target kind = %q, want %q", got, TargetHost)
	}
	for _, kind := range []TargetKind{TargetExplicit, TargetHeadless} {
		if got := (DesktopTarget{kind: kind}).Kind(); got != kind {
			t.Errorf("target kind = %q, want %q", got, kind)
		}
	}
}

func TestDefaultTimeoutPolicyIsShared(t *testing.T) {
	p := DefaultTimeoutPolicy
	if p.Short != 5*time.Second || p.Medium != 30*time.Second ||
		p.Long != 90*time.Second || p.Startup != 10*time.Minute ||
		p.Diagnostic != 5*time.Second || p.Poll != 150*time.Millisecond {
		t.Fatalf("default timeout policy = %#v", p)
	}
}

func TestTimeoutPolicyWithDefaultsFillsOnlyMissingValues(t *testing.T) {
	got := (TimeoutPolicy{Startup: time.Minute}).WithDefaults()
	if got.Startup != time.Minute {
		t.Fatalf("explicit startup timeout = %s, want 1m", got.Startup)
	}
	if got.Short != DefaultTimeoutPolicy.Short || got.Poll != DefaultTimeoutPolicy.Poll {
		t.Fatalf("missing policy values = %#v, want defaults", got)
	}
}

func TestStartupWaitAttemptsUsesConfiguredPolicy(t *testing.T) {
	got := startupWaitAttempts(TimeoutPolicy{
		Startup: 4 * time.Second,
		Poll:    time.Second,
	})
	if got != 4 {
		t.Fatalf("startup wait attempts = %d, want 4", got)
	}
}
