package perfuncted

import (
	"context"
	"fmt"

	"github.com/nskaggs/perfuncted/clipboard"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/screen"
	"github.com/nskaggs/perfuncted/window"
)

// NewSessionForTesting assembles a Session from deterministic backend fakes.
// Production code should use Open.
func NewSessionForTesting(
	screenshotter screen.Screenshotter,
	inputter input.Inputter,
	windowManager window.Manager,
	outputLister output.Lister,
	clipboardBackend clipboard.Clipboard,
) *Session {
	session := &Session{
		capabilities: make(
			map[Capability]CapabilityStatus,
			len(allCapabilities),
		),
		closeDone: make(chan struct{}),
		env:       env.FromEnviron([]string{}),
		target: DesktopTarget{
			kind: TargetExplicit,
			env:  []string{},
		},
	}
	session.ctx, session.cancel = context.WithCancel(context.Background())
	session.Screen = &ScreenBundle{
		Screenshotter: screenshotter,
		bundleBase:    session.bundleBase(CapabilityScreen),
	}
	session.Input = &InputBundle{
		Inputter:   inputter,
		bundleBase: session.bundleBase(CapabilityInput),
	}
	session.Windows = &WindowBundle{
		Manager:    windowManager,
		bundleBase: session.bundleBase(CapabilityWindows),
		session:    session,
	}
	session.Outputs = &OutputBundle{
		Lister:     outputLister,
		bundleBase: session.bundleBase(CapabilityOutputs),
	}
	session.Clipboard = &ClipboardBundle{
		Clipboard: clipboardBackend,
		bundleBase: session.bundleBase(
			CapabilityClipboard,
		),
	}
	backends := map[Capability]any{
		CapabilityScreen:    screenshotter,
		CapabilityInput:     inputter,
		CapabilityWindows:   windowManager,
		CapabilityOutputs:   outputLister,
		CapabilityClipboard: clipboardBackend,
	}
	for _, capability := range allCapabilities {
		backend := backends[capability]
		available := backend != nil
		status := CapabilityStatus{
			Capability: capability,
			Requested:  available,
			Required:   available,
			Available:  available,
		}
		if available {
			status.Backend = fmt.Sprintf("%T", backend)
			status.Operations = supportedOperations(capability, backend)
		}
		session.capabilities[capability] = status
	}
	return session
}
