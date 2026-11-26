package capability

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// UnsupportedError describes an operation that the selected backend cannot perform.
type UnsupportedError struct {
	Surface string
	Backend string
	Reason  string
}

func (e UnsupportedError) Error() string {
	switch {
	case e.Surface != "" && e.Backend != "" && e.Reason != "":
		return e.Surface + ": unsupported on " + e.Backend + ": " + e.Reason
	case e.Surface != "" && e.Reason != "":
		return e.Surface + ": unsupported: " + e.Reason
	case e.Surface != "":
		return fmt.Sprintf("%s: unsupported", e.Surface)
	default:
		return "unsupported"
	}
}

// Unsupported returns a typed error for a missing capability.
func Unsupported(surface, backend, reason string) error {
	return UnsupportedError{Surface: surface, Backend: backend, Reason: reason}
}

// ErrNilSession identifies a capability with no session owner.
var ErrNilSession = errors.New("session is nil")
var ErrSessionClosed = errors.New("session is closed")
var ErrUnavailable = errors.New("backend unavailable")
var ErrUnsupported = errors.New("operation unsupported")
var ErrInvalidArgument = errors.New("invalid argument")

// Admission is the narrow, module-private boundary for fixed capability
// operations. The session owns the policy and authority; semantic owners
// own the backend and apply the checks around actual dispatch.
type Admission interface {
	BoundedAuthority
	Trace(component, operation string)
}

// Authority checks the session lifetime and translates operation errors.
// It does not grant access to any backend or any capability.
type Authority interface {
	EnsureOpen() error
	OperationError(operation string, err error) error
}

// BackendAuthority adds admission for a particular fixed backend.
type BackendAuthority interface {
	Authority
	CheckBackend(operation string, backend any) error
}

// BoundedAuthority adds the existing medium-timeout operation context.
type BoundedAuthority interface {
	BackendAuthority
	BackendContext(context.Context) (context.Context, context.CancelFunc)
}

// Read performs a bounded read and suppresses results after authority loss.
// Backend operation errors remain distinguishable from cancellation.
func Read[T any](ctx context.Context, gate Admission, component, operation string, read func(context.Context) (T, error)) (T, error) {
	var zero T
	ctx, cancel := gate.BackendContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	gate.Trace(component, operation)
	value, err := read(ctx)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return zero, ctxErr
	}
	if openErr := gate.EnsureOpen(); openErr != nil {
		return zero, openErr
	}
	return value, gate.OperationError(operation, err)
}

// Write preserves the distinction between failed dispatch and late authority
// loss. It does not retry a mutation.
func Write(ctx context.Context, gate Admission, component, operation string, write func(context.Context) error) error {
	ctx, cancel := gate.BackendContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	gate.Trace(component, operation)
	err := write(ctx)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return errors.Join(ctxErr, gate.OperationError(operation, err))
	}
	if openErr := gate.EnsureOpen(); openErr != nil {
		return errors.Join(openErr, gate.OperationError(operation, err))
	}
	return gate.OperationError(operation, err)
}

var operationsBySurface = map[string][]string{
	"screen": {
		"capture",
		"hash",
		"pixel",
		"resolution",
		"wait",
		"wait-change",
		"wait-stable",
	},
	"input-observation": {"listen-hotkey", "listen-button"},
	"input": {
		"keyboard",
		"pointer",
		"click",
		"scroll",
		"drag",
		"pointer-location",
		"pointer-coordinate-space",
		"sync",
	},
	"windows": {
		"discover",
		"active-title",
		"sync",
		"info",
		"activate",
		"move",
		"resize",
		"close",
		"minimize",
		"maximize",
		"fullscreen",
		"restore",
	},
	"outputs": {
		"list",
	},
	"clipboard": {
		"get",
		"set",
	},
	"accessibility": {
		"applications",
		"snapshot",
		"find",
		"find-application",
		"focused",
		"at-point",
		"events",
		"outline",
		"invoke-action",
		"invoke-action-by-name",
		"invoke-default-action",
		"grab-focus",
		"scroll",
		"scroll-to-point",
		"set-position",
		"set-size",
		"set-extents",
		"set-value",
		"set-text-contents",
		"insert-text",
		"delete-text",
		"copy-text",
		"cut-text",
		"paste-text",
		"set-caret",
		"set-text-selection",
		"add-text-selection",
		"remove-text-selection",
		"set-document-text-selections",
		"select-child",
		"deselect-child",
		"select-all",
		"clear-selection",
		"deselect-selected-child",
		"select-row",
		"deselect-row",
		"select-column",
		"deselect-column",
		"window-root",
		"reopen",
	},
}

// Operations returns a defensive copy of the canonical operation names for a
// capability surface, optionally omitting operations unsupported by a backend.
func Operations(surface string, exclude ...string) []string {
	operations := slices.Clone(operationsBySurface[surface])
	for _, name := range exclude {
		operations = slices.DeleteFunc(operations, func(operation string) bool {
			return operation == name
		})
	}
	return operations
}
