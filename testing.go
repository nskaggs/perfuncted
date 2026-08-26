package perfuncted

import (
	"context"
	"fmt"

	"github.com/nskaggs/perfuncted/clipboard"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/util"
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
	if util.IsNil(screenshotter) {
		screenshotter = nil
	}
	if util.IsNil(inputter) {
		inputter = nil
	}
	if util.IsNil(windowManager) {
		windowManager = nil
	}
	if util.IsNil(outputLister) {
		outputLister = nil
	}
	if util.IsNil(clipboardBackend) {
		clipboardBackend = nil
	}

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
		backend:    screenshotter,
		bundleBase: session.bundleBase(CapabilityScreen),
	}
	session.Input = &InputBundle{
		backend:    inputter,
		bundleBase: session.bundleBase(CapabilityInput),
	}
	session.Windows = &WindowBundle{
		backend:    windowManager,
		bundleBase: session.bundleBase(CapabilityWindows),
	}
	session.Outputs = &OutputBundle{
		backend:    outputLister,
		bundleBase: session.bundleBase(CapabilityOutputs),
	}
	session.Clipboard = &ClipboardBundle{
		backend: clipboardBackend,
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
