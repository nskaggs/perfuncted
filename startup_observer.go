package perfuncted

import (
	"context"
	"errors"
)

// StartupPhase names a bounded boundary inside Open. Values contain no host
// paths, environment values, process identifiers, or application data.
type StartupPhase string

const (
	StartupPhaseStaleSessionCleanup         StartupPhase = "stale_session_cleanup"
	StartupPhaseManagedRuntimeSetup         StartupPhase = "managed_runtime_setup"
	StartupPhaseRuntimeDirectoryPreparation StartupPhase = "runtime_directory_preparation"
	StartupPhaseDBusStartup                 StartupPhase = "dbus_startup"
	StartupPhaseDBusProcessSpawn            StartupPhase = "dbus_process_spawn"
	StartupPhaseDBusSocketReady             StartupPhase = "dbus_socket_ready"
	StartupPhaseAccessibilityLauncher       StartupPhase = "accessibility_launcher"
	StartupPhaseAccessibilityBusAddress     StartupPhase = "accessibility_bus_address"
	StartupPhaseSwayConfiguration           StartupPhase = "sway_configuration"
	StartupPhaseSwayStartup                 StartupPhase = "sway_startup"
	StartupPhaseSwayProcessSpawn            StartupPhase = "sway_process_spawn"
	StartupPhaseSwaySocketsReady            StartupPhase = "sway_sockets_ready"
	StartupPhaseClipboardHelper             StartupPhase = "clipboard_helper"
	StartupPhaseCapabilityScreen            StartupPhase = "capability_screen"
	StartupPhaseCapabilityInput             StartupPhase = "capability_input"
	StartupPhaseCapabilityWindows           StartupPhase = "capability_windows"
	StartupPhaseCapabilityOutputs           StartupPhase = "capability_outputs"
	StartupPhaseCapabilityClipboard         StartupPhase = "capability_clipboard"
	StartupPhaseCapabilityAccessibility     StartupPhase = "capability_accessibility"
)

func capabilityStartupPhase(capability Capability) StartupPhase {
	switch capability {
	case CapabilityScreen:
		return StartupPhaseCapabilityScreen
	case CapabilityInput:
		return StartupPhaseCapabilityInput
	case CapabilityWindows:
		return StartupPhaseCapabilityWindows
	case CapabilityOutputs:
		return StartupPhaseCapabilityOutputs
	case CapabilityClipboard:
		return StartupPhaseCapabilityClipboard
	case CapabilityAccessibility:
		return StartupPhaseCapabilityAccessibility
	default:
		return ""
	}
}

// StartupPhaseState reports only stage start and its terminal result.
type StartupPhaseState string

const (
	StartupPhaseStarted   StartupPhaseState = "started"
	StartupPhaseSucceeded StartupPhaseState = "succeeded"
	StartupPhaseFailed    StartupPhaseState = "failed"
	StartupPhaseCanceled  StartupPhaseState = "canceled"
	StartupPhaseTimedOut  StartupPhaseState = "timed_out"
)

// StartupPhaseObserver receives fixed phase/state values synchronously. It
// should append bounded data and return without I/O.
type StartupPhaseObserver func(StartupPhase, StartupPhaseState)

func observeStartupPhase(observer StartupPhaseObserver, phase StartupPhase, state StartupPhaseState) {
	if observer == nil {
		return
	}
	defer func() { _ = recover() }()
	observer(phase, state)
}

func startupPhaseResult(ctx context.Context, err error) StartupPhaseState {
	if err == nil {
		return StartupPhaseSucceeded
	}
	if ctx != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			return StartupPhaseTimedOut
		}
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
			return StartupPhaseCanceled
		}
	}
	return StartupPhaseFailed
}

func (s *Session) runStartupPhase(ctx context.Context, phase StartupPhase, run func() error) error {
	if s == nil || s.startupObserver == nil {
		return run()
	}
	observeStartupPhase(s.startupObserver, phase, StartupPhaseStarted)
	err := run()
	observeStartupPhase(s.startupObserver, phase, startupPhaseResult(ctx, err))
	return err
}
