package perfuncted

import (
	"context"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/window"
)

// Successful backend selection binds only the matching facade. In particular,
// opening windows must not implicitly initialize output enumeration or vice versa.
func TestCapabilityOpenWindowAndOutputBindOnlyOwnedFacades(t *testing.T) {
	preserveOpeners(t)
	windows := &listOnlyWindowManager{}
	outputs := &closeCountingOutput{}
	var windowsOpened, outputsOpened int
	openWindow = func(context.Context, env.Runtime) (window.Manager, error) {
		windowsOpened++
		return windows, nil
	}
	openOutput = func(context.Context, env.Runtime) (output.Lister, error) {
		outputsOpened++
		return outputs, nil
	}
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()

	gotWindow, err := session.openCapabilityContext(context.Background(), CapabilityWindows)
	if err != nil || gotWindow != windows || session.Windows.backend != windows {
		t.Fatalf("window opener did not install exact backend: backend=%v err=%v", gotWindow, err)
	}
	if outputsOpened != 0 || session.Outputs.backend != nil {
		t.Fatal("opening windows unexpectedly initialized the output backend")
	}
	gotOutput, err := session.openCapabilityContext(context.Background(), CapabilityOutputs)
	if err != nil || gotOutput != outputs || session.Outputs.backend != outputs {
		t.Fatalf("output opener did not install exact backend: backend=%v err=%v", gotOutput, err)
	}
	if windowsOpened != 1 || outputsOpened != 1 {
		t.Fatalf("unexpected opener calls windows=%d outputs=%d", windowsOpened, outputsOpened)
	}
}
