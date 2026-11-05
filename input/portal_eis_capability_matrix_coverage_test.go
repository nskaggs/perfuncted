package input

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestRemoteDesktopEISCapabilitiesRequireBoundDevicesAndRegions(t *testing.T) {
	tests := []struct {
		name    string
		sender  eiSender
		want    []string
		pointer bool
	}{
		{"no bound devices", eiSender{}, []string{"keyboard: unavailable", "pointer: unavailable", "button: unavailable", "scroll: unavailable", "text: unavailable", "absolute pointer regions: 0"}, false},
		{"object IDs without device bindings", eiSender{keyboard: 1, pointer: 2, button: 3, scroll: 4, text: 5, pointers: []eiPointerDevice{{device: 8, pointer: 9, regions: []eiRegion{{width: 0, height: 10}}}}}, []string{"keyboard: unavailable", "pointer: unavailable", "button: unavailable", "scroll: unavailable", "text: unavailable", "absolute pointer regions: 0"}, false},
		{"fully authorized", eiSender{keyboard: 1, keyboardDev: 2, button: 3, buttonDev: 4, scroll: 5, scrollDev: 6, text: 7, textDev: 8, pointers: []eiPointerDevice{{device: 9, pointer: 10, regions: []eiRegion{{width: 100, height: 100}, {width: 0, height: 50}, {width: 50, height: 50}}}}}, []string{"keyboard: available", "pointer: available", "button: available", "scroll: available", "text: available", "absolute pointer regions: 2"}, true},
		{"unbound pointer regions do not authorize", eiSender{pointers: []eiPointerDevice{{device: 11, regions: []eiRegion{{width: 100, height: 100}}}, {pointer: 12, regions: []eiRegion{{width: 50, height: 50}}}}}, []string{"pointer: unavailable", "absolute pointer regions: 0"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := &RemoteDesktopBackend{sender: &tt.sender, portal: &portalSession{}, commands: make(chan remoteDesktopCommand), stop: make(chan struct{}), revoked: make(chan struct{})}
			diagnostics := strings.Join(backend.Diagnostics(), "\n")
			for _, want := range tt.want {
				if !strings.Contains(diagnostics, want) {
					t.Errorf("diagnostics %q missing %q", diagnostics, want)
				}
			}
			info, err := backend.PointerCoordinateSpace(context.Background())
			if tt.pointer {
				if err != nil || info.Kind != CoordinateSpaceLogical || info.ScaleX != 1 || info.ScaleY != 1 {
					t.Fatalf("authorized coordinate space = %+v, %v", info, err)
				}
			} else if !errors.Is(err, ErrNotSupported) {
				t.Fatalf("unauthorized coordinate space = %+v, %v", info, err)
			}
			if got := backend.SupportedOperations(); (len(got) > 0) != tt.pointer {
				t.Fatalf("supported operations = %v, pointer authorized = %v", got, tt.pointer)
			}
		})
	}
}

func TestRemoteDesktopEISPointerCoordinateSpaceRejectsInvalidContextsAndState(t *testing.T) {
	sender := &eiSender{pointers: []eiPointerDevice{{device: 1, pointer: 2, regions: []eiRegion{{width: 100, height: 100}}}}}
	backend := &RemoteDesktopBackend{sender: sender, portal: &portalSession{}, commands: make(chan remoteDesktopCommand), stop: make(chan struct{}), revoked: make(chan struct{})}
	if _, err := backend.PointerCoordinateSpace(nil); err == nil || !strings.Contains(err.Error(), "nil context") { //nolint:staticcheck // Explicitly verify rejection of nil contexts.
		t.Fatalf("nil context was not rejected: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := backend.PointerCoordinateSpace(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context returned %v", err)
	}
	close(backend.stop)
	if _, err := backend.PointerCoordinateSpace(context.Background()); !errors.Is(err, ErrRemoteDesktopUnavailable) {
		t.Fatalf("closed session coordinate space returned %v", err)
	}
	if got := backend.SupportedOperations(); len(got) != 0 {
		t.Fatalf("closed session advertises operations: %v", got)
	}
	if got := fmt.Sprint(backend.Diagnostics()); !strings.Contains(got, "unavailable") || strings.Contains(got, "session established") {
		t.Fatalf("closed session diagnostics: %q", got)
	}
}
