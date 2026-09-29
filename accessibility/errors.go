// Package accessibility exposes the AT-SPI accessibility tree and typed
// automation operations when
// an accessibility bus is available. It deliberately uses the same godbus
// dependency as the rest of perfuncted so callers do not need CGO or libatspi.
package accessibility

import (
	"errors"
	"fmt"
)

var (
	// ErrUnsupported indicates that an optional AT-SPI operation is not
	// implemented by this backend or object.
	ErrUnsupported = errors.New("accessibility: operation unsupported")
	// ErrNotFound indicates that a query found no matching accessible object.
	ErrNotFound = errors.New("accessibility: object not found")
	// ErrAmbiguous indicates that a selector matched more than one application,
	// window, action, or other target.
	ErrAmbiguous = errors.New("accessibility: ambiguous application")
	// ErrStaleNode indicates that a node handle belongs to an older
	// accessibility generation and must not be reused.
	ErrStaleNode = errors.New("accessibility: stale node")
	// ErrDisconnected indicates that the target accessibility bus disconnected.
	ErrDisconnected = errors.New("accessibility: disconnected")
	// ErrScope indicates that a tree operation was requested without an
	// explicit application/window/root scope.
	ErrScope = errors.New("accessibility: explicit scope required")
	// ErrInvalidAction indicates that an action index is not exposed by a node.
	ErrInvalidAction = errors.New("accessibility: invalid action index")
	// ErrMutationRejected indicates that a provider declined a valid mutation.
	ErrMutationRejected = errors.New("accessibility: mutation rejected")
	// ErrStaleGeneration indicates that a remote read crossed an object-handle
	// invalidation boundary and its result was discarded.
	ErrStaleGeneration = errors.New("accessibility: generation changed during read")
	// ErrObservationChanged indicates provider state changed during a multi-call
	// observation, so the result cannot prove one consistent view.
	ErrObservationChanged = errors.New("accessibility: observation changed during read")
	// ErrUnsupportedCorrelation indicates that compositor and accessibility
	// identity could not be correlated from authoritative evidence.
	ErrUnsupportedCorrelation = errors.New("accessibility: window correlation unsupported")
	// ErrResponseBudget indicates that even the minimum safe snapshot envelope
	// cannot fit within the caller's hard response limit.
	ErrResponseBudget = errors.New("accessibility: snapshot response budget exceeded")
	// ErrIncompleteSnapshot indicates that bounded observation did not cover
	// enough provider state to establish a unique semantic target.
	ErrIncompleteSnapshot = errors.New("accessibility: incomplete snapshot")
)

// DispatchOutcome describes what is known about an AT-SPI action dispatch.
type DispatchOutcome string

const (
	// DispatchNotSent means validation failed before the provider mutation call.
	DispatchNotSent DispatchOutcome = "not-sent"
	// DispatchAccepted means the provider returned a successful action response.
	DispatchAccepted DispatchOutcome = "accepted"
	// DispatchRejected means the provider explicitly returned false.
	DispatchRejected DispatchOutcome = "rejected"
	// DispatchUnknown means the provider call may have executed but its result
	// could not be established. Callers must not replay the action.
	DispatchUnknown DispatchOutcome = "unknown"
)

// ActionInvocationError retains dispatch certainty and the selected action
// when an invocation fails after action metadata has been read.
type ActionInvocationError struct {
	Action   Action          `json:"action"`
	Dispatch DispatchOutcome `json:"dispatch"`
	Err      error           `json:"-"`
}

func (e *ActionInvocationError) Error() string {
	if e == nil {
		return "accessibility: action invocation failed"
	}
	return fmt.Sprintf("accessibility: action dispatch %s: %v", e.Dispatch, e.Err)
}

func (e *ActionInvocationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}
