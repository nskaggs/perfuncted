package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/v2/accessibility"
	"github.com/nskaggs/perfuncted/v2/clipboard"
	"github.com/nskaggs/perfuncted/v2/input"
	"github.com/nskaggs/perfuncted/v2/internal/capability"
	"github.com/nskaggs/perfuncted/v2/internal/contextutil"
	"github.com/nskaggs/perfuncted/v2/internal/gnomebridge"
	"github.com/nskaggs/perfuncted/v2/internal/motion"
	"github.com/nskaggs/perfuncted/v2/internal/sessionaccessibility"
	"github.com/nskaggs/perfuncted/v2/internal/sessionclipboard"
	"github.com/nskaggs/perfuncted/v2/internal/sessioninput"
	"github.com/nskaggs/perfuncted/v2/internal/sessionoutput"
	"github.com/nskaggs/perfuncted/v2/internal/sessionscreen"
	"github.com/nskaggs/perfuncted/v2/internal/sessionwindow"
	"github.com/nskaggs/perfuncted/v2/internal/util"
	"github.com/nskaggs/perfuncted/v2/output"
	"github.com/nskaggs/perfuncted/v2/screen"
	"github.com/nskaggs/perfuncted/v2/window"
	"image"
	"slices"
	"time"
)

type bundleBase struct {
	session    *sessionCore
	capability Capability

	// Fixed capabilities are resolved before Open returns and never change.
	// This immutable view avoids locking capability metadata on every input,
	// screen and window call. Accessibility deliberately remains dynamic.
	resolvedOperations []string
	operationsResolved bool
}

// backendContext gives each bounded backend operation the session's effective
// policy while retaining an earlier caller deadline or cancellation cause.
// Long-lived event streams intentionally do not use this helper: their
// lifetime belongs to the caller's context.
func (b *bundleBase) backendContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx = contextutil.Default(ctx)
	if b == nil || b.session == nil {
		return ctx, func() {}
	}
	if timeout <= 0 {
		timeout = b.session.Timeouts().Medium
	}
	// A caller deadline already earlier than the backend limit needs no
	// child cancellation context. This is especially useful for frequent
	// screen hash polls under one shared page-readiness deadline.
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= timeout {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func (b *bundleBase) traceAction(component, format string, args ...any) {
	if b == nil || b.session == nil || b.session.tracer == nil {
		return
	}
	b.session.tracer.Tracef(component, format, args...)
}

func (b *bundleBase) unavailable(operation string) error {
	if b == nil || b.session == nil {
		return ErrNilSession
	}
	err := ErrUnavailable
	if failure := b.session.Capability(b.capability).Failure; failure != nil {
		err = errors.Join(ErrUnavailable, failure)
	}
	return &CapabilityError{
		Capability: b.capability,
		Operation:  operation,
		Err:        err,
	}
}

// cacheOperations binds the immutable status slice rather than cloning it.
// Session owns the data; public Capability returns independent snapshots.
// It must run before the session is published and never after.
func (b *bundleBase) cacheOperations(operations []string) {
	b.resolvedOperations = operations
	b.operationsResolved = true
}

func (b *bundleBase) checkAvailable(operation string, available bool) error {
	if b == nil {
		return b.unavailable(operation)
	}
	if b.session == nil {
		return ErrNilSession
	}
	if b.session.isClosed() {
		return ErrSessionClosed
	}
	if !available {
		return b.unavailable(operation)
	}
	// Non-accessibility backends have a fixed operation contract once the
	// session opens; check the prevalidated list without locking the session.
	// Accessibility can reopen or disconnect and must use the live status.
	// Manually assembled test bundles without a cached list also use live data.
	var supported bool
	if b.operationsResolved && b.capability != CapabilityAccessibility {
		supported = slices.Contains(b.resolvedOperations, operation)
	} else {
		supported = b.session.supportsCapabilityOperation(b.capability, operation)
	}
	if !supported {
		return &CapabilityError{
			Capability: b.capability,
			Operation:  operation,
			Err:        ErrUnsupported,
		}
	}
	return nil
}

func (b *bundleBase) checkBackend(operation string, backend any) error {
	return b.checkAvailable(operation, !util.IsNil(backend))
}

func closeBackend[T interface{ Close() error }](backend T) error {
	if util.IsNil(backend) {
		return nil
	}
	return backend.Close()
}

func matchesOperationError(err error, targets ...error) bool {
	for _, target := range targets {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

func (b *bundleBase) operationError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if matchesOperationError(err, input.ErrObservationUnsupported, input.ErrNotSupported, window.ErrNotSupported, accessibility.ErrUnsupported, accessibility.ErrUnsupportedCorrelation) {
		err = errors.Join(ErrUnsupported, err)
	}
	if matchesOperationError(err, input.ErrInvalidObservationFilter, input.ErrInvalidStateQuery, window.ErrInvalidArgument) {
		err = errors.Join(ErrInvalidArgument, err)
	}
	var unsupported capability.UnsupportedError
	if errors.As(err, &unsupported) {
		err = errors.Join(ErrUnsupported, err)
	}
	if matchesOperationError(err, gnomebridge.ErrUnavailable, clipboard.ErrNoClipboardTool, util.ErrNotAvailable) {
		err = errors.Join(ErrUnavailable, err)
	}
	return &OperationError{
		Capability: b.capability,
		Operation:  operation,
		Err:        err,
	}
}

// bundleAdmission adapts the existing session admission policy without
// exposing an entire session or mutable authority to semantic owners.
type bundleAdmission struct{ base *bundleBase }

func (a bundleAdmission) CheckBackend(op string, backend any) error {
	return a.base.checkBackend(op, backend)
}
func (a bundleAdmission) BackendContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return a.base.backendContext(ctx, a.base.session.Timeouts().Medium)
}
func (a bundleAdmission) Trace(component, op string) { a.base.traceAction(component, "%s", op) }
func (a bundleAdmission) EnsureOpen() error          { return a.base.session.ensureOpen() }
func (a bundleAdmission) OperationError(op string, err error) error {
	return a.base.operationError(op, err)
}

// These aliases are private implementation references, not additional public contracts.
type clipboardState = sessionclipboard.Owner
type outputState = sessionoutput.Owner

func newClipboardState(base *bundleBase, backend clipboard.Clipboard) *clipboardState {
	return sessionclipboard.New(bundleAdmission{base}, backend)
}
func newOutputState(base *bundleBase, backend output.Lister) *outputState {
	return sessionoutput.New(bundleAdmission{base}, backend)
}

func (s *sessionCore) pasteWithInputContext(
	ctx context.Context,
	text string,
	input *inputState,
) error {
	if input == nil {
		if s.clipboard == nil {
			return (&bundleBase{}).unavailable("paste")
		}
		return s.clipboardAdmission.unavailable("paste")
	}
	if err := input.CheckAvailable("keyboard"); err != nil {
		return err
	}
	if err := s.clipboard.Set(ctx, text); err != nil {
		return err
	}
	// Clipboard ownership can finish after the caller has canceled. Do not
	// dispatch the paste shortcut after authorization has ended.
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return input.Type(ctx, "{ctrl+v}")
}

type inputState = sessioninput.Owner
type inputHandle = sessioninput.Handle

// MotionOptions selects bounded pointer interpolation in backend coordinates.
type MotionOptions = sessioninput.MotionOptions

// InputActionReceipt records a bounded, payload-free input result.
type InputActionReceipt = sessioninput.InputActionReceipt

type motionPoint = motion.Point

const inputReceiptHistoryLimit = sessioninput.ReceiptHistoryLimit

// ValidateMotionOptions checks a motion request before opening an input session.
func ValidateMotionOptions(o MotionOptions) error { return sessioninput.ValidateMotionOptions(o) }

type inputAdmission struct{ base *bundleBase }

func (a inputAdmission) CheckBackend(operation string, backend any) error {
	return a.base.checkBackend(operation, backend)
}
func (a inputAdmission) InputContext(ctx context.Context, extra time.Duration) (context.Context, context.CancelFunc) {
	return a.base.backendContext(ctx, extra+a.base.session.Timeouts().Short)
}
func (a inputAdmission) Tracef(component, format string, args ...any) {
	a.base.traceAction(component, format, args...)
}
func (a inputAdmission) EnsureOpen() error { return a.base.session.ensureOpen() }
func (a inputAdmission) OperationError(operation string, err error) error {
	return a.base.operationError(operation, err)
}
func (a inputAdmission) Mechanism() string {
	if a.base == nil || a.base.session == nil {
		return ""
	}
	return a.base.session.Capability(CapabilityInput).Backend
}
func newInputState(base *bundleBase, backend input.Inputter) *inputState {
	return sessioninput.New(inputAdmission{base}, backend)
}

type inputObservationState = sessioninput.ObservationOwner
type observationHandle = sessioninput.ObservationHandle

func (a inputAdmission) AfterClose(cancel func()) func() bool {
	return context.AfterFunc(a.base.session.ctx, cancel)
}
func newObservationState(base *bundleBase, backend input.Observer) *inputObservationState {
	return sessioninput.NewObservation(inputAdmission{base}, backend)
}

type screenHandle = sessionscreen.Handle
type screenState struct {
	screenHandle
	owner *sessionscreen.Owner
	bundleBase
}

type screenAdmission struct{ base *bundleBase }

func (a screenAdmission) CheckBackend(operation string, backend any) error {
	return a.base.checkBackend(operation, backend)
}
func (a screenAdmission) BackendContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return a.base.backendContext(ctx, a.base.session.Timeouts().Medium)
}
func (a screenAdmission) Tracef(component, format string, args ...any) {
	a.base.traceAction(component, format, args...)
}
func (a screenAdmission) EnsureOpen() error { return a.base.session.ensureOpen() }
func (a screenAdmission) OperationError(operation string, err error) error {
	return a.base.operationError(operation, err)
}
func (a screenAdmission) AfterClose(cancel func()) func() bool {
	return context.AfterFunc(a.base.session.ctx, cancel)
}
func newScreenState(base bundleBase, backend screen.Screenshotter) *screenState {
	s := &screenState{bundleBase: base}
	s.install(backend)
	return s
}
func (s *screenState) install(backend screen.Screenshotter) {
	if s.owner == nil {
		s.owner = sessionscreen.New(screenAdmission{&s.bundleBase}, backend)
		s.screenHandle = s.owner.Handle()
		return
	}
	s.owner.Install(backend)
}
func (s *screenState) backend() screen.Screenshotter {
	if s == nil {
		return nil
	}
	return s.owner.Backend()
}
func (s *screenState) close() error {
	if s == nil {
		return nil
	}
	return s.owner.Close()
}
func (s *screenState) checkAvailable(operation string) error {
	if s == nil {
		return ErrNilSession
	}
	return s.owner.CheckAvailable(operation)
}
func (s *screenState) traceAction(format string, args ...any) {
	if s != nil {
		s.bundleBase.traceAction("screen", format, args...)
	}
}
func (s *screenState) savePNG(ctx context.Context, img image.Image, path string) error {
	return s.owner.SavePNG(ctx, img, path)
}

type windowHandle = sessionwindow.Handle
type windowBundleState struct {
	windowHandle
	owner *sessionwindow.Owner
	bundleBase
}

// WindowEventKind identifies a lifecycle or focus notification.
type WindowEventKind = window.EventKind

// WindowEvent reports a window lifecycle or focus change.
type WindowEvent = window.Event

// Window event kinds reported by the active backend.
const (
	WindowAddedEvent   = window.WindowAddedEvent
	WindowRemovedEvent = window.WindowRemovedEvent
	WindowChangedEvent = window.WindowChangedEvent
	FocusChangedEvent  = window.FocusChangedEvent
)

func newWindowState(base bundleBase, backend window.Manager) *windowBundleState {
	b := &windowBundleState{bundleBase: base}
	b.owner = sessionwindow.New(bundleAdmission{&b.bundleBase}, backend)
	b.windowHandle = b.owner.Handle()
	return b
}
func (b *windowBundleState) backend() window.Manager {
	if b == nil {
		return nil
	}
	return b.owner.Backend()
}
func (b *windowBundleState) install(backend window.Manager) { b.owner.Install(backend) }
func (b *windowBundleState) close() error {
	if b == nil {
		return nil
	}
	return b.owner.Close()
}
func (b *windowBundleState) checkAvailable(operation string) error {
	if b == nil {
		return ErrNilSession
	}
	return b.owner.CheckAvailable(operation)
}

type accessibilityBackendOwner = sessionaccessibility.Owner

// The adapter participates in the real session lifecycle lock. No public
// handle exposes it or can manufacture a generation publication.
type accessibilityAdmission struct{ session *sessionCore }

func newAccessibilityBackendOwner(s *sessionCore) *accessibilityBackendOwner {
	if s == nil {
		return sessionaccessibility.New(nil)
	}
	return sessionaccessibility.New(accessibilityAdmission{s})
}
func (a accessibilityAdmission) Context() context.Context { return a.session.ctx }
func (a accessibilityAdmission) IsClosed() bool           { return a.session.isClosed() }
func (a accessibilityAdmission) LockPublication() (func(), error) {
	s := a.session
	s.lifecycleMu.Lock()
	if s.closed || s.ctx == nil {
		s.lifecycleMu.Unlock()
		return nil, ErrSessionClosed
	}
	return s.lifecycleMu.Unlock, nil
}
func (a accessibilityAdmission) NotifyWaiters() { a.session.notifyWaiters() }
func (a accessibilityAdmission) MarkUnavailable(cause error) {
	s := a.session
	s.capabilitiesMu.Lock()
	defer s.capabilitiesMu.Unlock()
	status := s.capabilities[CapabilityAccessibility]
	status.Available = false
	status.Failure = errors.Join(ErrUnavailable, cause)
	s.capabilities[CapabilityAccessibility] = status
}
func (a accessibilityAdmission) DescribeBackend(backend accessibility.Backend) sessionaccessibility.Status {
	return sessionaccessibility.Status{Backend: fmt.Sprintf("%T", backend), Operations: slices.Clone(supportedOperations(CapabilityAccessibility, backend)), Diagnostics: backendDiagnostics(backend)}
}
func (a accessibilityAdmission) PublishStatus(dynamic sessionaccessibility.Status) {
	s := a.session
	s.capabilitiesMu.Lock()
	defer s.capabilitiesMu.Unlock()
	status := s.capabilities[CapabilityAccessibility]
	status.Available = true
	status.Failure = nil
	status.Backend = dynamic.Backend
	status.Operations = dynamic.Operations
	status.Diagnostics = dynamic.Diagnostics
	s.capabilities[CapabilityAccessibility] = status
}
