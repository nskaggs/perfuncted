package perfuncted

import (
	"context"
	"errors"
	"time"

	"github.com/nskaggs/perfuncted/v2/input"
	"github.com/nskaggs/perfuncted/v2/internal/contextutil"
)

// operationScope is immutable policy associated with a session-bound handle.
// It never owns the underlying session or backend.
type operationScope struct {
	core     *sessionCore
	ctx      context.Context
	explicit bool
	timeout  time.Duration
}
type scopeDeadlineKey struct{}
type operationClass uint8

const (
	scopeShort operationClass = iota
	scopeMedium
	scopeLong
	scopeWait
	scopeStartup
	scopeDiagnostic
	scopeCleanup
	scopeStream
	scopeMotion
	scopeBatch
)

func (s operationScope) withContext(ctx context.Context) operationScope {
	s.ctx = ctx
	s.explicit = true
	return s
}
func (s operationScope) withTimeout(d time.Duration) operationScope { s.timeout = d; return s }
func (s operationScope) begin(class operationClass, extra time.Duration) (context.Context, func(*error), error) {
	if s.explicit && s.ctx == nil || s.timeout < 0 {
		return nil, nil, ErrInvalidArgument
	}
	parent := contextutil.Default(s.ctx)
	if err := contextutil.ErrorWithCause(parent); err != nil {
		return nil, nil, err
	}
	policy := DefaultTimeoutPolicy
	if s.core != nil {
		policy = s.core.Timeouts()
	}
	duration := policy.Medium
	switch class {
	case scopeShort:
		duration = policy.Short
	case scopeLong, scopeWait, scopeCleanup, scopeBatch:
		duration = policy.Long
	case scopeStartup:
		duration = policy.Startup
	case scopeDiagnostic:
		duration = policy.Long
	case scopeMotion:
		duration = policy.Short + extra
	}
	if s.timeout > 0 {
		duration = s.timeout
	}
	if class == scopeStream {
		return parent, func(*error) {}, nil
	}
	ctx, cancel := context.WithTimeout(parent, duration)
	// Composite waits retain bounded individual backend attempts.
	if class != scopeWait && class != scopeDiagnostic && class != scopeCleanup && (class != scopeBatch || s.timeout > 0) {
		ctx = context.WithValue(ctx, scopeDeadlineKey{}, true)
	}
	stop := func() bool { return false }
	if s.core != nil && s.core.ctx != nil && class != scopeCleanup && class != scopeDiagnostic {
		var revoke context.CancelCauseFunc
		ctx, revoke = context.WithCancelCause(ctx)
		stop = context.AfterFunc(s.core.ctx, func() { revoke(ErrSessionClosed) })
		if s.core.ctx.Err() != nil {
			revoke(ErrSessionClosed)
		}
		old := cancel
		cancel = func() { revoke(context.Canceled); old() }
	}
	return ctx, func(err *error) {
		if *err != nil {
			if cause := contextutil.ErrorWithCause(ctx); cause != nil && !errors.Is(*err, cause) {
				*err = errors.Join(*err, cause)
			}
		}
		stop()
		cancel()
	}, nil
}
func (s *Session) operationScope() operationScope {
	if s == nil {
		return operationScope{}
	}
	p := s.scope
	p.core = s.core
	return p
}
func (h InputBundle) operationScope() operationScope            { return h.scope }
func (h InputObservationBundle) operationScope() operationScope { return h.scope }
func (h ScreenBundle) operationScope() operationScope           { return h.scope }
func (h WindowBundle) operationScope() operationScope           { return h.scope }
func (h OutputBundle) operationScope() operationScope           { return h.scope }
func (h ClipboardBundle) operationScope() operationScope        { return h.scope }
func (h AccessibilityBundle) operationScope() operationScope    { return h.scope }
func (h *Window) operationScope() operationScope {
	if h == nil {
		return operationScope{}
	}
	return h.scope
}
func (h *Application) operationScope() operationScope {
	if h == nil {
		return operationScope{}
	}
	return h.scope
}
func (h *AccessibilityLocator) operationScope() operationScope {
	if h == nil {
		return operationScope{}
	}
	p := h.operation
	if p.core == nil && h.bundle != nil {
		p.core = h.bundle.session
	}
	return p
}

func (h ScreenBundle) backendHandle() screenHandle {
	if h.state == nil {
		return screenHandle{}
	}
	return h.state.screenHandle
}
func (h WindowBundle) backendHandle() windowHandle {
	if h.state == nil {
		return windowHandle{}
	}
	return h.state.windowHandle
}
func (h AccessibilityBundle) backendHandle() accessibilityHandle {
	if h.state == nil {
		return accessibilityHandle{}
	}
	return h.state.accessibilityHandle
}

// streamSetup keeps a caller-owned stream alive after the setup deadline has
// been disarmed. The session and caller can still revoke it.
type streamSetup struct {
	ctx     context.Context
	release func()
	finish  func() error
}

func (s operationScope) startStream() (streamSetup, error) {
	parent, _, err := s.begin(scopeStream, 0)
	if err != nil {
		return streamSetup{}, err
	}
	// Existing provider lifetime management suffices when no additional setup
	// bound was requested. In particular, avoid a retained child context.
	if s.timeout == 0 {
		return streamSetup{ctx: parent, release: func() {}, finish: func() error { return contextutil.ErrorWithCause(parent) }}, nil
	}
	ctx, cancel := context.WithCancelCause(parent)
	stopSession := func() bool { return false }
	if s.core != nil && s.core.ctx != nil {
		stopSession = context.AfterFunc(s.core.ctx, func() { cancel(ErrSessionClosed) })
		if s.core.ctx.Err() != nil {
			cancel(ErrSessionClosed)
		}
	}
	// Detach the session callback when the caller cancels independently.
	context.AfterFunc(ctx, func() { stopSession() })
	fired := make(chan struct{})
	timer := time.AfterFunc(s.timeout, func() {
		cancel(context.DeadlineExceeded)
		close(fired)
	})
	return streamSetup{
		ctx:     ctx,
		release: func() { stopSession(); cancel(context.Canceled) },
		finish: func() error {
			if !timer.Stop() {
				<-fired
			}
			return contextutil.ErrorWithCause(ctx)
		},
	}, nil
}

// scopedWindowEvents adapts a legacy session-owned stream to a caller's
// cancellation without closing the underlying provider channel.
func scopedEvents[T any](ctx context.Context, source <-chan T, release func()) <-chan T {
	out := make(chan T)
	go func() {
		defer close(out)
		defer release()
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-source:
				if !ok || ctx.Err() != nil {
					return
				}
				select {
				case <-ctx.Done():
					return
				case out <- event:
				}
			}
		}
	}()
	return out
}

// scopedObservation owns only a cancellation callback, not the observer.
// Close retains the provider's terminal error and releases the setup scope.
type scopedObservation struct {
	input.ObservationSubscription
	release func()
}

func (s scopedObservation) Close() error {
	defer s.release()
	return s.ObservationSubscription.Close()
}
