package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/env"
)

func TestRemoteDesktopInputIsRequiredAndNeverFallsBack(t *testing.T) {
	previousPortal := openRemoteDesktopInput
	previousInput := openInput
	t.Cleanup(func() {
		openRemoteDesktopInput = previousPortal
		openInput = previousInput
	})
	portalCalls, fallbackCalls := 0, 0
	openRemoteDesktopInput = func(_ context.Context, _ env.Runtime, options input.RemoteDesktopOptions) (input.Inputter, error) {
		portalCalls++
		if options.ParentWindow != "xdg:test-parent" || !options.PersistPermission {
			t.Fatalf("portal options = %+v", options)
		}
		return nil, input.ErrRemoteDesktopDenied
	}
	openInput = func(context.Context, env.Runtime, int32, int32) (input.Inputter, error) {
		fallbackCalls++
		return nil, errors.New("fallback must not run")
	}
	_, err := Open(context.Background(),
		WithTarget(EnvironmentTarget(nil)),
		WithRemoteDesktopInput(RemoteDesktopInputOptions{ParentWindow: "xdg:test-parent", PersistPermission: true}),
	)
	if !errors.Is(err, input.ErrRemoteDesktopDenied) {
		t.Fatalf("Open error = %v, want portal denial", err)
	}
	if portalCalls != 1 || fallbackCalls != 0 {
		t.Fatalf("portal calls=%d fallback calls=%d, want one portal attempt and no fallback", portalCalls, fallbackCalls)
	}
}

func TestRemoteDesktopInputRejectsOptionalAndDuplicateConfiguration(t *testing.T) {
	newConfig := func() openConfig {
		return openConfig{required: make(map[Capability]struct{}), optional: make(map[Capability]struct{})}
	}
	options := RemoteDesktopInputOptions{}
	cfg := newConfig()
	if err := Optional(CapabilityInput)(&cfg); err != nil {
		t.Fatalf("mark input optional: %v", err)
	}
	if err := WithRemoteDesktopInput(options)(&cfg); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("optional portal input error = %v, want ErrInvalidArgument", err)
	}
	cfg = newConfig()
	if err := WithRemoteDesktopInput(options)(&cfg); err != nil {
		t.Fatalf("configure portal input: %v", err)
	}
	if _, required := cfg.required[CapabilityInput]; !required {
		t.Fatal("portal input did not become required")
	}
	if err := WithRemoteDesktopInput(options)(&cfg); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("duplicate portal input error = %v, want ErrInvalidArgument", err)
	}
	if err := Optional(CapabilityInput)(&cfg); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("optional capability after portal input error = %v, want ErrInvalidArgument", err)
	}
}
