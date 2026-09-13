package screen

import (
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestPublicContextlessConstructorsValidateMissingEndpoints(t *testing.T) {
	constructors := []struct {
		name string
		open func() error
	}{
		{
			name: "GNOME native",
			open: func() error {
				_, err := NewGnomeNativeScreenBackendForRuntime(env.FromEnviron(nil))
				return err
			},
		},
		{
			name: "extension capture",
			open: func() error {
				_, err := NewExtCaptureBackendForSocket("")
				return err
			},
		},
		{
			name: "portal D-Bus",
			open: func() error {
				_, err := NewPortalDBusBackendForBus("")
				return err
			},
		},
		{
			name: "wlroots screencopy",
			open: func() error {
				_, err := NewWlrScreencopyBackendForSocket("")
				return err
			},
		},
	}
	for _, constructor := range constructors {
		t.Run(constructor.name, func(t *testing.T) {
			if err := constructor.open(); err == nil {
				t.Fatal("constructor succeeded without its required endpoint")
			}
		})
	}
}
