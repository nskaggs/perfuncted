package perfuncted

import (
	"context"

	"github.com/nskaggs/perfuncted/internal/util"
	"github.com/nskaggs/perfuncted/window"
)

// WindowBundle is the session-bound window discovery and control facade.
type WindowBundle struct {
	backend window.Manager
	bundleBase
}

// WindowEventKind identifies a window lifecycle or focus notification.
type WindowEventKind = window.EventKind

const (
	// WindowAddedEvent reports a newly visible window.
	WindowAddedEvent = window.WindowAddedEvent
	// WindowRemovedEvent reports a window that is no longer managed.
	WindowRemovedEvent = window.WindowRemovedEvent
	// WindowChangedEvent reports changed window metadata or state.
	WindowChangedEvent = window.WindowChangedEvent
	// FocusChangedEvent reports a changed focus target.
	FocusChangedEvent = window.FocusChangedEvent
)

// WindowEvent is a window lifecycle or focus notification from a backend.
type WindowEvent = window.Event

func (b *WindowBundle) checkAvailable(operation string) error {
	if b == nil {
		return (&bundleBase{}).unavailable(operation)
	}
	return b.bundleBase.checkAvailable(operation, !util.IsNil(b.backend))
}

func (b *WindowBundle) close() error {
	if b == nil || util.IsNil(b.backend) {
		return nil
	}
	return b.backend.Close()
}

// Sync refreshes pending window-backend state when supported.
func (b *WindowBundle) Sync(ctx context.Context) error {
	if err := b.checkAvailable("sync"); err != nil {
		return err
	}
	type syncer interface {
		Sync(context.Context) error
	}
	if backend, ok := b.backend.(syncer); ok {
		return b.operationError("sync", backend.Sync(ctx))
	}
	return b.operationError("sync", ErrUnsupported)
}

// ActiveTitle returns the title of the focused window.
func (b *WindowBundle) ActiveTitle(ctx context.Context) (string, error) {
	if err := b.checkAvailable("active-title"); err != nil {
		return "", err
	}
	title, err := b.backend.ActiveTitle(ctx)
	return title, b.operationError("active-title", err)
}

// Events returns the bounded, lossy stream of window lifecycle and focus
// notifications when the selected backend provides one. Events may be
// dropped, including lifecycle and focus events, if the consumer is slow.
// Refresh authoritative state with List or Window.Info after receiving an
// event. The channel closes when the session closes or the backend stops.
func (b *WindowBundle) Events() (<-chan WindowEvent, error) {
	if err := b.checkAvailable("events"); err != nil {
		return nil, err
	}
	source, ok := b.backend.(window.EventSource)
	if !ok {
		return nil, b.operationError("events", window.ErrNotSupported)
	}
	return source.WindowEvents(), nil
}
