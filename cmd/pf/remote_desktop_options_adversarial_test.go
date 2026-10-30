package main

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted"
	"strings"
	"testing"
)

func TestRemoteDesktopCLIOptionalInfoCanBuildOptions(t *testing.T) {
	for _, tt := range []struct {
		name               string
		required, optional []perfuncted.Capability
		remember           bool
	}{
		{name: "required input", required: []perfuncted.Capability{perfuncted.CapabilityInput}},
		{name: "optional info includes input", optional: []perfuncted.Capability{perfuncted.CapabilityInput, perfuncted.CapabilityWindows, perfuncted.CapabilityScreen}},
		{name: "remember user consent", optional: []perfuncted.Capability{perfuncted.CapabilityInput}, remember: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel() // options must validate without touching host
			_, err := defaultOpenPFFactory(&cliConfig{inputBackend: "remote-desktop", required: tt.required, optional: tt.optional, rememberInputPermission: tt.remember})(ctx)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("options failed before canceled open: %v", err)
			}
		})
	}
}
func TestRemoteDesktopCLIRejectsInvalidConfigurations(t *testing.T) {
	for _, cfg := range []cliConfig{{inputBackend: "missing"}, {inputBackend: "auto", rememberInputPermission: true}} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := defaultOpenPFFactory(&cfg)(ctx)
		if err == nil || !strings.Contains(err.Error(), "--") {
			t.Errorf("config %+v expected argument error, got %v", cfg, err)
		}
	}
}
