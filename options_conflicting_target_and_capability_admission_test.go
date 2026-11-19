package perfuncted

import (
	"errors"
	"testing"
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
