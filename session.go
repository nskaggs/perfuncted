package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/v2/accessibility"
	"github.com/nskaggs/perfuncted/v2/clipboard"
	"github.com/nskaggs/perfuncted/v2/input"
	"github.com/nskaggs/perfuncted/v2/internal/env"
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
	tracer          *actionTracer
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
		s.tracer = newActionTracer(cfg.traceOut, cfg.logger, cfg.traceDelay)
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
