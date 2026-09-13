package window

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
				_, err := NewGnomeNativeManagerForRuntime(env.FromEnviron(nil))
				return err
			},
		},
		{
			name: "KWin scripting",
			open: func() error {
				_, err := NewKWinScriptManagerForBus("")
				return err
			},
		},
		{
			name: "Sway IPC",
			open: func() error {
				_, err := NewSwayManagerRuntime(env.FromEnviron(nil))
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
