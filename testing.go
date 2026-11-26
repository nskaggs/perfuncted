package perfuncted

import (
	"context"
	"fmt"
	"io"

	"github.com/nskaggs/perfuncted/v2/accessibility"
	"github.com/nskaggs/perfuncted/v2/clipboard"
	"github.com/nskaggs/perfuncted/v2/input"
	"github.com/nskaggs/perfuncted/v2/internal/env"
	"github.com/nskaggs/perfuncted/v2/internal/util"
	"github.com/nskaggs/perfuncted/v2/output"
	"github.com/nskaggs/perfuncted/v2/screen"
	"github.com/nskaggs/perfuncted/v2/window"
)

// NewSessionForTesting assembles a Session from deterministic backend fakes.
// Production code should use Open.
func newSessionForTesting(
	screenshotter screen.Screenshotter,
	inputter input.Inputter,
	windowManager window.Manager,
	outputLister output.Lister,
	clipboardBackend clipboard.Clipboard,
	accessibilityBackend ...accessibility.Backend,
) *sessionCore {
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

	session := &sessionCore{
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
	// Test sessions retain a bounded action history without writing to the
	// caller's output. This makes opt-in failure bundles useful by default.
	session.tracer = newActionTracer(io.Discard, nil, 0)
	session.screen = newScreenState(session.bundleBase(CapabilityScreen), screenshotter)
	session.inputAdmission = session.bundleBase(CapabilityInput)
	session.input = newInputState(&session.inputAdmission, inputter)
	session.windows = newWindowState(session.bundleBase(CapabilityWindows), windowManager)
	session.outputAdmission = session.bundleBase(CapabilityOutputs)
	session.outputs = newOutputState(&session.outputAdmission, outputLister)
	session.clipboardAdmission = session.bundleBase(CapabilityClipboard)
	session.clipboard = newClipboardState(&session.clipboardAdmission, clipboardBackend)
	session.accessibility = newAccessibilityState(session.bundleBase(CapabilityAccessibility), newAccessibilityBackendOwner(session))
	var accessibilityImpl accessibility.Backend
	if len(accessibilityBackend) > 0 && !util.IsNil(accessibilityBackend[0]) {
		accessibilityImpl = accessibilityBackend[0]
		session.accessibility.installBackend(accessibilityImpl)
	}
	backends := map[Capability]any{
		CapabilityScreen:        screenshotter,
		CapabilityInput:         inputter,
		CapabilityWindows:       windowManager,
		CapabilityOutputs:       outputLister,
		CapabilityClipboard:     clipboardBackend,
		CapabilityAccessibility: accessibilityImpl,
	}
	for _, capability := range allCapabilities {
		backend := backends[capability]
		available := !util.IsNil(backend)
		status := CapabilityStatus{
			Capability: capability,
			Requested:  available,
			Required:   available,
			Available:  available,
		}
		if available {
			status.Backend = fmt.Sprintf("%T", backend)
			status.Operations = supportedOperations(capability, backend)
			status.Diagnostics = backendDiagnostics(backend)
		}
		session.capabilities[capability] = status
	}
	session.cacheResolvedBundleOperations()
	return session
}
