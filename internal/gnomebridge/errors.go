package gnomebridge

import (
	"errors"
	"fmt"
)

var (
	// ErrUnavailable means that the bridge service is not currently reachable.
	ErrUnavailable = errors.New("gnome bridge unavailable")
	// ErrProtocolMismatch means that the running extension cannot speak the
	// protocol understood by this client.
	ErrProtocolMismatch = errors.New("gnome bridge protocol mismatch")
	// ErrSessionRestartRequired reports that installation or replacement of the
	// extension succeeded but GNOME Shell must load it at the next session.
	ErrSessionRestartRequired = errors.New("GNOME Shell session restart required")
	// ErrUnixFDUnsupported means that the active D-Bus transport cannot pass
	// the descriptor required by the screen interface.
	ErrUnixFDUnsupported = errors.New("gnome bridge D-Bus transport does not support Unix file descriptors")
	// ErrObjectNotFound is translated by capability adapters into their public
	// package-specific not-found error.
	ErrObjectNotFound = errors.New("gnome bridge object not found")
)

// ProtocolError describes the protocol values observed during negotiation.
type ProtocolError struct {
	Expected uint32
	Actual   uint32
}

func (e *ProtocolError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("gnome bridge protocol %d is incompatible with client protocol %d", e.Actual, e.Expected)
}

func (e *ProtocolError) Is(target error) bool { return target == ErrProtocolMismatch }

// SessionRestartRequiredError includes the installed extension directory so a
// CLI can give a useful diagnostic while still supporting errors.Is.
type SessionRestartRequiredError struct {
	Path string
}

func (e *SessionRestartRequiredError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Path == "" {
		return ErrSessionRestartRequired.Error()
	}
	return fmt.Sprintf("%s: log out and back in once to activate the perfuncted GNOME integration (installed at %s)", ErrSessionRestartRequired, e.Path)
}

func (e *SessionRestartRequiredError) Is(target error) bool {
	return target == ErrSessionRestartRequired
}
