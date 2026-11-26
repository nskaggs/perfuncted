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
	"github.com/nskaggs/perfuncted/v2/internal/diagnostic"
	"github.com/nskaggs/perfuncted/v2/internal/env"
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
	"sync"
	"time"
)

// Session is the central orchestrator of perfuncted. It owns all backends and
// manages the desktop session lifecycle.
type sessionCore struct {
	// Screen exposes screen-capture operations for this session.
	screen *screenState
	// Input exposes keyboard and pointer operations for this session.
	input          *inputState
	inputAdmission bundleBase
	// InputObservation observes explicitly requested global shortcuts or buttons.
	inputObservation     *inputObservationState
	observationAdmission bundleBase
	// Windows exposes window discovery and control for this session.
	windows *windowBundleState
	// Outputs exposes display-output discovery for this session.
	outputs         *outputState
	outputAdmission bundleBase
	// Clipboard exposes clipboard access for this session.
	clipboard          *clipboardState
	clipboardAdmission bundleBase
	// Accessibility exposes AT-SPI semantic queries and typed automation for this session.
	accessibility *accessibilityState

	config          SessionConfig
	startupObserver StartupPhaseObserver
	portalInput     *input.RemoteDesktopOptions
	timeoutInput    TimeoutPolicy
	hasTimeoutInput bool
	target          DesktopTarget
	env             env.Runtime
	tracer          *diagnostic.Tracer
	infra           *sessionInfra
	capabilities    map[Capability]CapabilityStatus
	capabilitiesMu  sync.RWMutex

	ctx    context.Context //nolint:containedctx // session owns this context
	cancel context.CancelFunc

	lifecycleMu sync.Mutex
	closed      bool
	apps        []*applicationState

	closeOnce sync.Once
	closeDone chan struct{}
	closeErr  error

	hubOnce sync.Once
	hubMu   sync.RWMutex
	hub     *invalidationHub
}

// Open creates a new Session, resolving backends and optionally starting
// an isolated desktop session.
func openSession(ctx context.Context, opts ...Option) (*sessionCore, error) {
	if ctx == nil {
		return nil, fmt.Errorf("perfuncted: open: %w: nil context", ErrInvalidArgument)
	}

	cfg := openConfig{
		required: make(map[Capability]struct{}),
		optional: make(map[Capability]struct{}),
		target: targetSelection{
			kind: TargetHost,
		},
	}
	for _, option := range opts {
		if option == nil {
			continue
		}
		if err := option(&cfg); err != nil {
			return nil, err
		}
	}
	if cfg.target.kind == TargetHeadless && cfg.target.config.Resolution == (image.Point{}) {
		cfg.target.config.Resolution = image.Pt(1024, 768)
	}
	timeoutInput := cfg.target.config.Timeouts
	cfg.target.config.Timeouts = cfg.target.config.Timeouts.WithDefaults()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s := &sessionCore{
		config:          cfg.target.config,
		startupObserver: cfg.startupObserver,
		portalInput:     cfg.portalInput,
		timeoutInput:    timeoutInput,
		hasTimeoutInput: true,
		capabilities:    make(map[Capability]CapabilityStatus, len(allCapabilities)),
		closeDone:       make(chan struct{}),
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())

	if cfg.trace {
		s.tracer = diagnostic.NewTracer(cfg.traceOut, cfg.logger, cfg.traceDelay)
	}

	wantAccessibility := false
	_, wantAccessibilityOptional := cfg.optional[CapabilityAccessibility]
	_, wantAccessibilityRequired := cfg.required[CapabilityAccessibility]
	wantAccessibility = wantAccessibilityOptional || wantAccessibilityRequired
	if err := s.resolveRuntime(ctx, cfg.target, wantAccessibility); err != nil {
		s.cancel()
		return nil, err
	}

	if err := s.initializeCapabilities(ctx, cfg); err != nil {
		closeErr := s.Close() //nolint:contextcheck // Close owns its shutdown contexts
		return nil, errors.Join(err, closeErr)
	}
	if err := ctx.Err(); err != nil {
		closeErr := s.Close() //nolint:contextcheck // Close owns its shutdown contexts
		return nil, errors.Join(err, closeErr)
	}

	// Backend support for these five facades is fixed during initialization.
	// The accessibility backend can reopen and is intentionally excluded.
	s.cacheResolvedBundleOperations()
	return s, nil
}

func (s *sessionCore) resolveRuntime(ctx context.Context, target targetSelection, wantAccessibility bool) error {
	runStaleCleanup := func() error {
		cleanupCtx, cancel := context.WithTimeout(
			ctx,
			target.config.Timeouts.WithDefaults().Startup,
		)
		defer cancel()
		return s.runStartupPhase(cleanupCtx, StartupPhaseStaleSessionCleanup, func() error {
			return CleanupStaleSessionsContext(cleanupCtx, 24*time.Hour)
		})
	}

	switch target.kind {
	case TargetHost:
		s.env = env.Current()
	case TargetExplicit:
		s.env = env.FromEnviron(target.target.Env())
	case TargetHeadless:
		if err := runStaleCleanup(); err != nil {
			return err
		}
		var infra *sessionInfra
		err := s.runStartupPhase(ctx, StartupPhaseManagedRuntimeSetup, func() error {
			var startErr error
			infra, startErr = s.startSession(ctx, sessionModeHeadless, target.config, wantAccessibility)
			return startErr
		})
		if err != nil {
			return err
		}
		s.infra = infra
		s.env = env.Current().WithSession(infra.xdgDir, infra.wlDisplay, infra.dbusAddr).WithAccessibilityBus(infra.atspiAddr)
	case TargetNested:
		if err := runStaleCleanup(); err != nil {
			return err
		}
		var infra *sessionInfra
		err := s.runStartupPhase(ctx, StartupPhaseManagedRuntimeSetup, func() error {
			var startErr error
			infra, startErr = s.startSession(ctx, sessionModeNested, target.config, wantAccessibility)
			return startErr
		})
		if err != nil {
			return err
		}
		s.infra = infra
		s.env = env.Current().WithSession(infra.xdgDir, infra.wlDisplay, infra.dbusAddr).WithAccessibilityBus(infra.atspiAddr)
	default:
		return fmt.Errorf("perfuncted: unknown target kind %q", target.kind)
	}
	s.target = DesktopTarget{
		kind: target.kind,
		env:  s.env.EnvList(),
	}
	return nil
}

func (s *sessionCore) initializeCapabilities(ctx context.Context, cfg openConfig) error {
	s.screen = newScreenState(s.bundleBase(CapabilityScreen), nil)
	s.inputAdmission = s.bundleBase(CapabilityInput)
	s.input = newInputState(&s.inputAdmission, nil)
	s.observationAdmission = s.bundleBase(CapabilityInputObservation)
	s.inputObservation = newObservationState(&s.observationAdmission, nil)
	s.windows = newWindowState(s.bundleBase(CapabilityWindows), nil)
	s.outputAdmission = s.bundleBase(CapabilityOutputs)
	s.outputs = newOutputState(&s.outputAdmission, nil)
	s.clipboardAdmission = s.bundleBase(CapabilityClipboard)
	s.clipboard = newClipboardState(&s.clipboardAdmission, nil)
	s.accessibility = newAccessibilityState(s.bundleBase(CapabilityAccessibility), newAccessibilityBackendOwner(s))

	for _, capability := range allCapabilities {
		_, required := cfg.required[capability]
		_, optional := cfg.optional[capability]
		s.capabilities[capability] = CapabilityStatus{
			Capability: capability,
			Requested:  required || optional,
			Required:   required,
		}
	}

	for _, capability := range allCapabilities {
		if err := ctx.Err(); err != nil {
			return err
		}
		status := s.capabilities[capability]
		if !status.Requested {
			continue
		}
		phase := capabilityStartupPhase(capability)
		var backend any
		err := s.runStartupPhase(ctx, phase, func() error {
			var openErr error
			backend, openErr = s.openCapabilityContext(ctx, capability)
			return openErr
		})
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			status.Failure = errors.Join(ErrUnavailable, err)
		}
		status.Available = err == nil
		if err == nil {
			status.Backend = fmt.Sprintf("%T", backend)
			status.Operations = slices.Clone(supportedOperations(capability, backend))
			status.Diagnostics = backendDiagnostics(backend)
		}
		s.capabilities[capability] = status
		if err != nil && status.Required {
			return &CapabilityError{
				Capability: capability,
				Operation:  "open",
				Err:        status.Failure,
			}
		}
	}

	return nil
}

func supportedOperations(capability Capability, backend any) []string {
	if reporter, ok := backend.(interface {
		SupportedOperations() []string
	}); ok {
		return slices.Clone(reporter.SupportedOperations())
	}
	return capabilityOperations(capability)
}

func backendDiagnostics(backend any) []string {
	if reporter, ok := backend.(interface{ Diagnostics() []string }); ok {
		return slices.Clone(reporter.Diagnostics())
	}
	return nil
}

// cacheResolvedBundleOperations binds immutable backend support to the
// per-capability facades before the session becomes visible to callers.
func (s *sessionCore) cacheResolvedBundleOperations() {
	for _, bundle := range []*bundleBase{
		&s.screen.bundleBase,
		&s.inputAdmission,
		&s.windows.bundleBase,
		&s.outputAdmission,
		&s.clipboardAdmission,
	} {
		bundle.cacheOperations(s.capabilities[bundle.capability].Operations)
	}
	// The testing constructor predates the optional observation facade.
	if s.inputObservation != nil {
		s.observationAdmission.cacheOperations(s.capabilities[CapabilityInputObservation].Operations)
	}
}

func (s *sessionCore) bundleBase(capability Capability) bundleBase {
	return bundleBase{
		session:    s,
		capability: capability,
	}
}

func (s *sessionCore) isClosed() bool {
	if s == nil {
		return true
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	return s.closed || s.ctx == nil
}

func (s *sessionCore) ensureOpen() error {
	if s.isClosed() {
		return ErrSessionClosed
	}
	return nil
}

func (s *sessionCore) openCapability(capability Capability) (any, error) {
	return s.openCapabilityContext(context.Background(), capability)
}

func (s *sessionCore) openCapabilityContext(ctx context.Context, capability Capability) (any, error) { //nolint:contextcheck // capability setup derives the effective startup policy.
	switch capability {
	case CapabilityScreen:
		return openCapabilityBackend(
			ctx, s.Timeouts().Startup, capability,
			func(ctx context.Context) (screen.Screenshotter, error) { return openScreen(ctx, s.env) },
			func(backend screen.Screenshotter) { configureScreenBackendTimeout(backend, s.Timeouts().Medium) },
			func(backend screen.Screenshotter) { s.screen.install(backend) },
		)
	case CapabilityInput:
		var maxX, maxY int32
		if s.config.Resolution != (image.Point{}) {
			maxX, maxY = int32(s.config.Resolution.X), int32(s.config.Resolution.Y)
		}
		return openCapabilityBackend(
			ctx, s.Timeouts().Startup, capability,
			func(ctx context.Context) (input.Inputter, error) {
				if s.portalInput != nil {
					return openRemoteDesktopInput(ctx, s.env, *s.portalInput)
				}
				return openInput(ctx, s.env, maxX, maxY)
			},
			nil,
			func(backend input.Inputter) { s.input.Install(backend) },
		)
	case CapabilityInputObservation:
		return openCapabilityBackend(
			ctx, s.Timeouts().Startup, capability,
			func(ctx context.Context) (input.Observer, error) { return input.OpenObserver(ctx, s.env) },
			nil,
			func(backend input.Observer) { s.inputObservation.Install(backend) },
		)
	case CapabilityWindows:
		return openCapabilityBackend(
			ctx, s.Timeouts().Startup, capability,
			func(ctx context.Context) (window.Manager, error) { return openWindow(ctx, s.env) },
			nil,
			func(backend window.Manager) { s.windows.install(backend) },
		)
	case CapabilityOutputs:
		return openCapabilityBackend(
			ctx, s.Timeouts().Startup, capability,
			func(ctx context.Context) (output.Lister, error) { return openOutput(ctx, s.env) },
			nil,
			func(backend output.Lister) { s.outputs.Install(backend) },
		)
	case CapabilityClipboard:
		return openCapabilityBackend(
			ctx, s.Timeouts().Startup, capability,
			func(ctx context.Context) (clipboard.Clipboard, error) { return openClipboard(ctx, s.env) },
			nil,
			func(backend clipboard.Clipboard) { s.clipboard.Install(backend) },
		)
	case CapabilityAccessibility:
		return openCapabilityBackend(
			ctx, s.Timeouts().Startup, capability,
			func(ctx context.Context) (accessibility.Backend, error) { return openAccessibility(ctx, s.env) },
			nil,
			func(backend accessibility.Backend) { s.accessibility.installBackend(backend) },
		)
	default:
		return nil, fmt.Errorf("unknown capability %q", capability)
	}
}

// Close releases all backends and tears down any managed session infrastructure.
func (s *sessionCore) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.closeErr = s.close()
		if s.closeDone != nil {
			close(s.closeDone)
		}
	})
	if s.closeDone != nil {
		<-s.closeDone
	}
	return s.closeErr
}

func (s *sessionCore) close() error {
	s.lifecycleMu.Lock()
	s.closed = true
	apps := make([]*applicationState, len(s.apps))
	copy(apps, s.apps)
	s.lifecycleMu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}

	var errs []error
	for i := len(apps) - 1; i >= 0; i-- {
		if err := s.stopApplication(apps[i]); err != nil {
			errs = append(errs, err)
		}
	}
	if s.screen != nil {
		errs = append(errs, s.screen.close())
	}
	if s.input != nil {
		errs = append(errs, s.input.Close())
	}
	if s.inputObservation != nil {
		errs = append(errs, s.inputObservation.Close())
	}
	if s.windows != nil {
		errs = append(errs, s.windows.close())
	}
	if s.outputs != nil {
		errs = append(errs, s.outputs.Close())
	}
	if s.clipboard != nil {
		errs = append(errs, s.clipboard.Close())
	}
	if s.accessibility != nil {
		errs = append(errs, s.accessibility.close())
	}

	if s.infra != nil {
		errs = append(errs, s.infra.stop())
	}

	return errors.Join(errs...)
}

// Has reports whether the session provides the given capability.
func (s *sessionCore) Has(cap Capability) bool {
	if s == nil || s.isClosed() {
		return false
	}
	s.capabilitiesMu.RLock()
	status, ok := s.capabilities[cap]
	s.capabilitiesMu.RUnlock()
	return ok && status.Available
}

// Capability returns the immutable resolution status for cap.
func (s *sessionCore) Capability(cap Capability) CapabilityStatus {
	if s == nil {
		return CapabilityStatus{
			Capability: cap,
			Failure:    ErrNilSession,
		}
	}
	s.capabilitiesMu.RLock()
	status, ok := s.capabilities[cap]
	s.capabilitiesMu.RUnlock()
	if !ok {
		return CapabilityStatus{Capability: cap}
	}
	return status.clone()
}

// Capabilities returns every capability's immutable resolution status.
func (s *sessionCore) Capabilities() []CapabilityStatus {
	statuses := make([]CapabilityStatus, 0, len(allCapabilities))
	for _, capability := range allCapabilities {
		statuses = append(statuses, s.Capability(capability))
	}
	return statuses
}

// Timeouts returns the effective timing policy used by this session. The
// returned value is a copy and can be safely modified by the caller.
func (s *sessionCore) Timeouts() TimeoutPolicy {
	if s == nil {
		return DefaultTimeoutPolicy
	}
	return s.config.Timeouts.WithDefaults()
}

// Target returns the exact immutable desktop target.
func (s *sessionCore) Target() DesktopTarget {
	if s == nil {
		return DesktopTarget{}
	}
	return s.target.clone()
}

// Env returns a copy of the process environment that routes child processes to
// the Session's target.
func (s *sessionCore) Env() []string {
	if s == nil {
		return []string{}
	}
	return s.env.EnvList()
}

// Paste writes text through the clipboard when available and otherwise types
// it directly.
func (s *sessionCore) Paste(ctx context.Context, text string) (result error) {
	if s == nil {
		return ErrNilSession
	}
	if ctx == nil {
		return fmt.Errorf("perfuncted: paste: %w: nil context", ErrInvalidArgument)
	}
	if err := s.ensureOpen(); err != nil {
		return err
	}
	// A canceled paste must never dispatch desktop input to a backend that
	// may not independently enforce the caller's cancellation.
	if err := ctx.Err(); err != nil {
		return err
	}
	// A backend can acknowledge dispatch after cancellation. Never publish
	// that late acknowledgement as a successful paste.
	defer func() {
		if err := ctx.Err(); err != nil {
			result = err
		}
	}()
	if s.Has(CapabilityClipboard) {
		return s.pasteWithInputContext(ctx, text, s.input)
	}
	if s.input == nil {
		return &CapabilityError{
			Capability: CapabilityInput,
			Operation:  "paste",
			Err:        ErrUnavailable,
		}
	}
	return s.input.TypeLiteral(ctx, text)
}

// XDG returns the resolved XDG runtime directory for the session.
func (s *sessionCore) XDG() string {
	if s == nil {
		return ""
	}
	return s.env.Get("XDG_RUNTIME_DIR")
}

// DBusAddress returns the session D-Bus bus address.
func (s *sessionCore) DBusAddress() string {
	if s == nil {
		return ""
	}
	return s.env.Get("DBUS_SESSION_BUS_ADDRESS")
}

// WaylandDisplay returns the session Wayland display name.
func (s *sessionCore) WaylandDisplay() string {
	if s == nil {
		return ""
	}
	return s.env.Get("WAYLAND_DISPLAY")
}

// X11Display returns the session X11 display string.
func (s *sessionCore) X11Display() string {
	if s == nil {
		return ""
	}
	return s.env.Display()
}

//nolint:contextcheck // capability setup derives the effective startup policy.
func openCapabilityBackend[T any](
	parent context.Context,
	timeout time.Duration,
	capability Capability,
	open func(context.Context) (T, error),
	configure func(T),
	install func(T),
) (T, error) {
	// Capability setup inherits caller cancellation and also has a bounded
	// startup policy so direct-call backend fallbacks cannot extend Open forever.
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	// An expired startup must never open an unnecessary desktop connection.
	if err := ctx.Err(); err != nil {
		var zero T
		return zero, err
	}

	backend, err := open(ctx)
	// Providers may ignore cancellation and return a successful backend after
	// permission to start has expired. Preserve the caller's authority and
	// close any partially acquired resource rather than installing it.
	if ctxErr := ctx.Err(); ctxErr != nil {
		err = ctxErr
	}
	backend, err = validateBackend(capability, backend, err)
	if err == nil {
		if configure != nil {
			configure(backend)
		}
		// Configuration may finish at the same time startup is canceled.
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = ctxErr
		} else {
			install(backend)
		}
	}
	closeFailedBackend(backend, err)
	// Cancellation closes the partially acquired resource above. Do not
	// return a now-closed backend handle to callers as a usable result.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		var zero T
		return zero, err
	}
	return backend, err
}

func configureScreenBackendTimeout(backend any, timeout time.Duration) {
	if backend == nil {
		return
	}
	if configurable, ok := backend.(interface{ SetCaptureTimeout(time.Duration) }); ok {
		configurable.SetCaptureTimeout(timeout)
	}
}

func validateBackend[T any](capability Capability, backend T, err error) (T, error) {
	if err == nil && util.IsNil(backend) {
		err = nilBackendError(capability)
	}
	return backend, err
}

func nilBackendError(capability Capability) error {
	return fmt.Errorf("perfuncted: %s backend returned nil: %w", capability, ErrUnavailable)
}

func closeFailedBackend(backend any, openErr error) {
	if openErr == nil || util.IsNil(backend) {
		return
	}
	if closer, ok := backend.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
}

func (s *sessionCore) supportsCapabilityOperation(c Capability, operation string) bool {
	s.capabilitiesMu.RLock()
	defer s.capabilitiesMu.RUnlock()
	return s.capabilities[c].Supports(operation)
}

// Session is the lifecycle authority for a privately owned desktop session.
// Copying the wrapper aliases the same core; Close is shared by all copies.
type Session struct{ core *sessionCore }

func (s *Session) coreState() *sessionCore {
	if s == nil {
		return nil
	}
	if s.core == nil {
		return &sessionCore{}
	}
	return s.core
}

func wrapSession(s *sessionCore) *Session {
	if s == nil {
		return nil
	}
	return &Session{core: s}
}

// Open resolves the requested backends and optionally starts an isolated desktop.
func Open(ctx context.Context, opts ...Option) (*Session, error) {
	s, err := openSession(ctx, opts...)
	return wrapSession(s), err
}

// Close releases all backends and tears down any managed session infrastructure.
func (s *Session) Close() error {
	return s.coreState().Close()
}

// Has reports whether the session provides the given capability.
func (s *Session) Has(cap Capability) bool {
	return s.coreState().Has(cap)
}

// Capability returns the immutable resolution status for cap.
func (s *Session) Capability(cap Capability) CapabilityStatus {
	return s.coreState().Capability(cap)
}

// Capabilities returns every capability's immutable resolution status.
func (s *Session) Capabilities() []CapabilityStatus {
	return s.coreState().Capabilities()
}

// Timeouts returns the effective timing policy used by this session. The
// returned value is a copy and can be safely modified by the caller.
func (s *Session) Timeouts() TimeoutPolicy {
	return s.coreState().Timeouts()
}

// Target returns the exact immutable desktop target.
func (s *Session) Target() DesktopTarget {
	return s.coreState().Target()
}

// Env returns a copy of the process environment that routes child processes to
// the Session's target.
func (s *Session) Env() []string {
	return s.coreState().Env()
}

// Paste writes text through the clipboard when available and otherwise types
// it directly.
func (s *Session) Paste(ctx context.Context, text string) (result error) {
	return s.coreState().Paste(ctx, text)
}

// XDG returns the resolved XDG runtime directory for the session.
func (s *Session) XDG() string {
	return s.coreState().XDG()
}

// DBusAddress returns the session D-Bus bus address.
func (s *Session) DBusAddress() string {
	return s.coreState().DBusAddress()
}

// WaylandDisplay returns the session Wayland display name.
func (s *Session) WaylandDisplay() string {
	return s.coreState().WaylandDisplay()
}

// X11Display returns the session X11 display string.
func (s *Session) X11Display() string {
	return s.coreState().X11Display()
}

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

// Input and screen share the same private composition adapter.

func (a bundleAdmission) InputContext(ctx context.Context, extra time.Duration) (context.Context, context.CancelFunc) {
	return a.base.backendContext(ctx, extra+a.base.session.Timeouts().Short)
}
func (a bundleAdmission) Tracef(component, format string, args ...any) {
	a.base.traceAction(component, format, args...)
}
func (a bundleAdmission) Mechanism() string {
	if a.base == nil || a.base.session == nil {
		return ""
	}
	return a.base.session.Capability(CapabilityInput).Backend
}
func newInputState(base *bundleBase, backend input.Inputter) *inputState {
	return sessioninput.New(bundleAdmission{base}, backend)
}

type inputObservationState = sessioninput.ObservationOwner
type observationHandle = sessioninput.ObservationHandle

func (a bundleAdmission) AfterClose(cancel func()) func() bool {
	return context.AfterFunc(a.base.session.ctx, cancel)
}
func newObservationState(base *bundleBase, backend input.Observer) *inputObservationState {
	return sessioninput.NewObservation(bundleAdmission{base}, backend)
}

type screenHandle = sessionscreen.Handle
type screenState struct {
	screenHandle
	owner *sessionscreen.Owner
	bundleBase
}

func newScreenState(base bundleBase, backend screen.Screenshotter) *screenState {
	s := &screenState{bundleBase: base}
	s.install(backend)
	return s
}
func (s *screenState) install(backend screen.Screenshotter) {
	if s.owner == nil {
		s.owner = sessionscreen.New(bundleAdmission{&s.bundleBase}, backend)
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

// ScreenBundle is a non-owning session-bound screen handle.
type ScreenBundle struct {
	screenHandle
	state *screenState
}

// InputBundle is a non-owning session-bound injection handle.
type InputBundle struct{ inputHandle }

// InputObservationBundle is a separately authorized observation handle.
type InputObservationBundle struct{ observationHandle }

// WindowBundle is a non-owning session-bound discovery/control handle.
type WindowBundle struct {
	windowHandle
	state *windowBundleState
}

// OutputBundle is a non-owning session-bound output discovery handle.
type OutputBundle struct{ state *outputState }

// ClipboardBundle is a non-owning session-bound clipboard handle.
type ClipboardBundle struct{ state *clipboardState }

// AccessibilityBundle is a non-owning session-bound semantic automation handle.
type AccessibilityBundle struct {
	accessibilityHandle
	state *accessibilityState
}

// Screen returns the screen capability handle.
func (s *Session) Screen() ScreenBundle {
	if s == nil || s.core == nil {
		return ScreenBundle{}
	}
	if s.core.screen == nil {
		return ScreenBundle{}
	}
	return ScreenBundle{s.core.screen.screenHandle, s.core.screen}
}

// Input returns the keyboard and pointer injection handle.
func (s *Session) Input() InputBundle {
	if s == nil || s.core == nil {
		return InputBundle{}
	}
	return InputBundle{s.core.input.Handle()}
}

// InputObservation returns the independent input observation handle.
func (s *Session) InputObservation() InputObservationBundle {
	if s == nil || s.core == nil {
		return InputObservationBundle{}
	}
	return InputObservationBundle{s.core.inputObservation.Handle()}
}

// Windows returns the window discovery and control handle.
func (s *Session) Windows() WindowBundle {
	if s == nil || s.core == nil {
		return WindowBundle{}
	}
	if s.core.windows == nil {
		return WindowBundle{}
	}
	return WindowBundle{s.core.windows.windowHandle, s.core.windows}
}

// Outputs returns the output discovery handle.
func (s *Session) Outputs() OutputBundle {
	if s == nil || s.core == nil {
		return OutputBundle{}
	}
	return OutputBundle{s.core.outputs}
}

// Clipboard returns the clipboard handle.
func (s *Session) Clipboard() ClipboardBundle {
	if s == nil || s.core == nil {
		return ClipboardBundle{}
	}
	return ClipboardBundle{s.core.clipboard}
}

// Accessibility returns the semantic accessibility handle.
func (s *Session) Accessibility() AccessibilityBundle {
	if s == nil || s.core == nil {
		return AccessibilityBundle{}
	}
	if s.core.accessibility == nil {
		return AccessibilityBundle{}
	}
	return AccessibilityBundle{s.core.accessibility.accessibilityHandle, s.core.accessibility}
}

// List returns the displays visible to the active output backend.
func (b OutputBundle) List(ctx context.Context) ([]output.Info, error) { return b.state.List(ctx) }

// Get returns the current clipboard text.
func (b ClipboardBundle) Get(ctx context.Context) (string, error) { return b.state.Get(ctx) }

// Set replaces the clipboard text.
func (b ClipboardBundle) Set(ctx context.Context, text string) error { return b.state.Set(ctx, text) }
