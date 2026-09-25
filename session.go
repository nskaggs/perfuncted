package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"image"
	"slices"
	"sync"
	"time"

	"github.com/nskaggs/perfuncted/accessibility"
	"github.com/nskaggs/perfuncted/clipboard"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/screen"
	"github.com/nskaggs/perfuncted/window"
)

// Session is the central orchestrator of perfuncted. It owns all backends and
// manages the desktop session lifecycle.
type Session struct {
	// Screen exposes screen-capture operations for this session.
	Screen *ScreenBundle
	// Input exposes keyboard and pointer operations for this session.
	Input *InputBundle
	// Windows exposes window discovery and control for this session.
	Windows *WindowBundle
	// Outputs exposes display-output discovery for this session.
	Outputs *OutputBundle
	// Clipboard exposes clipboard access for this session.
	Clipboard *ClipboardBundle
	// Accessibility exposes AT-SPI semantic queries and typed automation for this session.
	Accessibility *AccessibilityBundle

	config          SessionConfig
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
	apps        []*Application

	closeOnce sync.Once
	closeDone chan struct{}
	closeErr  error

	hubOnce sync.Once
	hubMu   sync.RWMutex
	hub     *invalidationHub
}

// Open creates a new Session, resolving backends and optionally starting
// an isolated desktop session.
func Open(ctx context.Context, opts ...Option) (*Session, error) {
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

	s := &Session{
		config:          cfg.target.config,
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

	return s, nil
}

func (s *Session) resolveRuntime(ctx context.Context, target targetSelection, wantAccessibility bool) error {
	switch target.kind {
	case TargetHost:
		s.env = env.Current()
	case TargetExplicit:
		s.env = env.FromEnviron(target.target.Env())
	case TargetHeadless:
		CleanupStaleSessions(24 * time.Hour)
		infra, err := s.startSession(ctx, sessionModeHeadless, target.config, wantAccessibility)
		if err != nil {
			return err
		}
		s.infra = infra
		s.env = env.Current().WithSession(infra.xdgDir, infra.wlDisplay, infra.dbusAddr).WithAccessibilityBus(infra.atspiAddr)
	case TargetNested:
		CleanupStaleSessions(24 * time.Hour)
		infra, err := s.startSession(ctx, sessionModeNested, target.config, wantAccessibility)
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

func (s *Session) initializeCapabilities(ctx context.Context, cfg openConfig) error {
	s.Screen = &ScreenBundle{bundleBase: s.bundleBase(CapabilityScreen)}
	s.Input = &InputBundle{bundleBase: s.bundleBase(CapabilityInput)}
	s.Windows = &WindowBundle{
		bundleBase: s.bundleBase(CapabilityWindows),
	}
	s.Outputs = &OutputBundle{bundleBase: s.bundleBase(CapabilityOutputs)}
	s.Clipboard = &ClipboardBundle{bundleBase: s.bundleBase(CapabilityClipboard)}
	s.Accessibility = &AccessibilityBundle{bundleBase: s.bundleBase(CapabilityAccessibility)}

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
		backend, err := s.openCapabilityContext(ctx, capability)
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

func (s *Session) bundleBase(capability Capability) bundleBase {
	return bundleBase{
		session:    s,
		capability: capability,
	}
}

func (s *Session) isClosed() bool {
	if s == nil {
		return true
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	return s.closed || s.ctx == nil
}

func (s *Session) ensureOpen() error {
	if s.isClosed() {
		return ErrSessionClosed
	}
	return nil
}

func (s *Session) openCapability(capability Capability) (any, error) {
	return s.openCapabilityContext(context.Background(), capability)
}

func (s *Session) openCapabilityContext(ctx context.Context, capability Capability) (any, error) { //nolint:contextcheck // capability setup derives the effective startup policy.
	switch capability {
	case CapabilityScreen:
		return openCapabilityBackend(
			ctx, s.Timeouts().Startup, capability,
			func(ctx context.Context) (screen.Screenshotter, error) { return openScreen(ctx, s.env) },
			func(backend screen.Screenshotter) { configureScreenBackendTimeout(backend, s.Timeouts().Medium) },
			func(backend screen.Screenshotter) { s.Screen.backend = backend },
		)
	case CapabilityInput:
		var maxX, maxY int32
		if s.config.Resolution != (image.Point{}) {
			maxX, maxY = int32(s.config.Resolution.X), int32(s.config.Resolution.Y)
		}
		return openCapabilityBackend(
			ctx, s.Timeouts().Startup, capability,
			func(ctx context.Context) (input.Inputter, error) { return openInput(ctx, s.env, maxX, maxY) },
			nil,
			func(backend input.Inputter) { s.Input.backend = backend },
		)
	case CapabilityWindows:
		return openCapabilityBackend(
			ctx, s.Timeouts().Startup, capability,
			func(ctx context.Context) (window.Manager, error) { return openWindow(ctx, s.env) },
			nil,
			func(backend window.Manager) { s.Windows.backend = backend },
		)
	case CapabilityOutputs:
		return openCapabilityBackend(
			ctx, s.Timeouts().Startup, capability,
			func(ctx context.Context) (output.Lister, error) { return openOutput(ctx, s.env) },
			nil,
			func(backend output.Lister) { s.Outputs.backend = backend },
		)
	case CapabilityClipboard:
		return openCapabilityBackend(
			ctx, s.Timeouts().Startup, capability,
			func(ctx context.Context) (clipboard.Clipboard, error) { return openClipboard(ctx, s.env) },
			nil,
			func(backend clipboard.Clipboard) { s.Clipboard.backend = backend },
		)
	case CapabilityAccessibility:
		return openCapabilityBackend(
			ctx, s.Timeouts().Startup, capability,
			func(ctx context.Context) (accessibility.Backend, error) { return openAccessibility(ctx, s.env) },
			nil,
			func(backend accessibility.Backend) { s.Accessibility.backend = backend },
		)
	default:
		return nil, fmt.Errorf("unknown capability %q", capability)
	}
}

// Close releases all backends and tears down any managed session infrastructure.
func (s *Session) Close() error {
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

func (s *Session) close() error {
	s.lifecycleMu.Lock()
	s.closed = true
	apps := make([]*Application, len(s.apps))
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
	if s.Screen != nil {
		errs = append(errs, s.Screen.close())
	}
	if s.Input != nil {
		errs = append(errs, s.Input.close())
	}
	if s.Windows != nil {
		errs = append(errs, s.Windows.close())
	}
	if s.Outputs != nil {
		errs = append(errs, s.Outputs.close())
	}
	if s.Clipboard != nil {
		errs = append(errs, s.Clipboard.close())
	}
	if s.Accessibility != nil {
		errs = append(errs, s.Accessibility.close())
	}

	if s.infra != nil {
		s.infra.stop()
	}

	return errors.Join(errs...)
}

// Has reports whether the session provides the given capability.
func (s *Session) Has(cap Capability) bool {
	if s == nil || s.isClosed() {
		return false
	}
	s.capabilitiesMu.RLock()
	status, ok := s.capabilities[cap]
	s.capabilitiesMu.RUnlock()
	return ok && status.Available
}

// Capability returns the immutable resolution status for cap.
func (s *Session) Capability(cap Capability) CapabilityStatus {
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
func (s *Session) Capabilities() []CapabilityStatus {
	statuses := make([]CapabilityStatus, 0, len(allCapabilities))
	for _, capability := range allCapabilities {
		statuses = append(statuses, s.Capability(capability))
	}
	return statuses
}

// Timeouts returns the effective timing policy used by this session. The
// returned value is a copy and can be safely modified by the caller.
func (s *Session) Timeouts() TimeoutPolicy {
	if s == nil {
		return DefaultTimeoutPolicy
	}
	return s.config.Timeouts.WithDefaults()
}

// Target returns the exact immutable desktop target.
func (s *Session) Target() DesktopTarget {
	if s == nil {
		return DesktopTarget{}
	}
	return s.target.clone()
}

// Env returns a copy of the process environment that routes child processes to
// the Session's target.
func (s *Session) Env() []string {
	if s == nil {
		return []string{}
	}
	return s.env.EnvList()
}

// Paste writes text through the clipboard when available and otherwise types
// it directly.
func (s *Session) Paste(ctx context.Context, text string) error {
	if s == nil {
		return ErrNilSession
	}
	if ctx == nil {
		return fmt.Errorf("perfuncted: paste: %w: nil context", ErrInvalidArgument)
	}
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if s.Has(CapabilityClipboard) {
		return s.Clipboard.pasteWithInputContext(ctx, text, s.Input)
	}
	if s.Input == nil {
		return &CapabilityError{
			Capability: CapabilityInput,
			Operation:  "paste",
			Err:        ErrUnavailable,
		}
	}
	return s.Input.TypeLiteral(ctx, text)
}

// XDG returns the resolved XDG runtime directory for the session.
func (s *Session) XDG() string {
	if s == nil {
		return ""
	}
	return s.env.Get("XDG_RUNTIME_DIR")
}

// DBusAddress returns the session D-Bus bus address.
func (s *Session) DBusAddress() string {
	return s.env.Get("DBUS_SESSION_BUS_ADDRESS")
}

// WaylandDisplay returns the session Wayland display name.
func (s *Session) WaylandDisplay() string {
	return s.env.Get("WAYLAND_DISPLAY")
}

// X11Display returns the session X11 display string.
func (s *Session) X11Display() string {
	return s.env.Display()
}
