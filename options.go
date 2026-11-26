package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/v2/input"
	capabilityops "github.com/nskaggs/perfuncted/v2/internal/capability"
	"image"
	"io"
	"log/slog"
	"slices"
	"time"
)

var (
	// ErrUnsupported indicates that the selected backend does not implement an
	// operation. It is safe to inspect with errors.Is.
	ErrUnsupported = capabilityops.ErrUnsupported
	// ErrUnavailable indicates that a requested capability or backend could not
	// be opened for the current session.
	ErrUnavailable = capabilityops.ErrUnavailable
	// ErrInvalidArgument indicates invalid caller input.
	ErrInvalidArgument = capabilityops.ErrInvalidArgument
	// ErrOperationFailed identifies an operation that was supported but failed
	// while executing. The underlying backend error remains wrapped.
	ErrOperationFailed = errors.New("operation failed")
	// ErrNilSession is returned when operating on a nil Session.
	ErrNilSession = capabilityops.ErrNilSession
	// ErrSessionClosed is returned when starting work on a closed Session.
	ErrSessionClosed = capabilityops.ErrSessionClosed
	// ErrManagedScopeChanged means the managed window or application selected
	// for a locator changed identity during an operation.
	ErrManagedScopeChanged = errors.New("managed accessibility scope changed")
	// ErrAccessibilityTargetChanged means a target pinned by an accessibility
	// wait disappeared or was replaced before its requested state was observed.
	ErrAccessibilityTargetChanged = errors.New("accessible target identity changed")
)

// Capability identifies a category of desktop automation operations.
type Capability string

const (
	// CapabilityScreen identifies screen capture operations.
	CapabilityScreen Capability = "screen"
	// CapabilityInput identifies keyboard and pointer input operations.
	CapabilityInput Capability = "input"
	// CapabilityInputObservation is independent of input injection.
	CapabilityInputObservation Capability = "input-observation"
	// CapabilityWindows identifies window discovery and control operations.
	CapabilityWindows Capability = "windows"
	// CapabilityOutputs identifies display-output listing operations.
	CapabilityOutputs Capability = "outputs"
	// CapabilityClipboard identifies clipboard get and set operations.
	CapabilityClipboard Capability = "clipboard"
	// CapabilityAccessibility identifies AT-SPI semantic observation and typed automation.
	CapabilityAccessibility Capability = "accessibility"
)

var allCapabilities = []Capability{
	CapabilityScreen,
	CapabilityInput,
	CapabilityInputObservation,
	CapabilityWindows,
	CapabilityOutputs,
	CapabilityClipboard,
	CapabilityAccessibility,
}

func validCapability(cap Capability) bool {
	return slices.Contains(allCapabilities, cap)
}

func capabilityOperations(cap Capability) []string {
	return capabilityops.Operations(string(cap))
}

// CapabilityError reports why an operation cannot use a capability.
type CapabilityError struct {
	// Capability identifies the unavailable capability.
	Capability Capability
	// Operation identifies the requested operation, when known.
	Operation string
	// Err is the underlying capability error.
	Err error
}

// OperationError reports a failure from an operation that the active backend
// advertised as supported. The backend cause is available through errors.Is,
// errors.As, and Unwrap.
type OperationError struct {
	// Capability identifies the capability that failed.
	Capability Capability
	// Operation identifies the operation that failed.
	Operation string
	// Err is the underlying backend error.
	Err error
}

func (e *OperationError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s capability %s: %v", e.Capability, e.Operation, e.Err)
}

func (e *OperationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Is reports whether the operation belongs to target's stable error category.
func (e *OperationError) Is(target error) bool {
	if e == nil || target != ErrOperationFailed {
		return false
	}
	for _, category := range []error{
		ErrUnsupported,
		ErrUnavailable,
		ErrInvalidArgument,
		ErrNilSession,
		ErrSessionClosed,
	} {
		if errors.Is(e.Err, category) {
			return false
		}
	}
	return true
}

func (e *CapabilityError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Operation == "" {
		return fmt.Sprintf("%s capability: %v", e.Capability, e.Err)
	}
	return fmt.Sprintf("%s capability %s: %v", e.Capability, e.Operation, e.Err)
}

func (e *CapabilityError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Is makes capability errors match stable category sentinels while retaining
// the wrapped backend cause through Unwrap.
func (e *CapabilityError) Is(target error) bool {
	if e == nil {
		return false
	}
	if target == ErrUnsupported {
		return errors.Is(e.Err, ErrUnsupported)
	}
	if target == ErrUnavailable {
		return errors.Is(e.Err, ErrUnavailable)
	}
	return false
}

// CapabilityStatus describes how one capability was resolved for a Session.
// Operations and Diagnostics are immutable snapshots of the selected backend
// contract and bounded runtime health information.
type CapabilityStatus struct {
	// Capability identifies the capability.
	Capability Capability
	// Requested reports whether the caller requested the capability.
	Requested bool
	// Required reports whether failure to open the capability fails Open.
	Required bool
	// Available reports whether a backend was opened successfully.
	Available bool
	// Backend names the selected backend when one was opened.
	Backend string
	// Failure contains the opening error when the capability is unavailable.
	Failure error
	// Operations lists the operations advertised by the selected backend.
	Operations []string
	// Diagnostics contains bounded backend health and cost details for logs and
	// failure reports. It is not a control-flow or feature-detection API.
	Diagnostics []string
}

// Supports reports whether the resolved backend advertises operation.
func (s CapabilityStatus) Supports(operation string) bool {
	return slices.Contains(s.Operations, operation)
}

func (s CapabilityStatus) clone() CapabilityStatus {
	s.Operations = slices.Clone(s.Operations)
	s.Diagnostics = slices.Clone(s.Diagnostics)
	return s
}

// TargetKind describes where a Session routes desktop operations.
type TargetKind string

const (
	// TargetHost routes operations to the current desktop session.
	TargetHost TargetKind = "host"
	// TargetHeadless routes operations to a session created without a display.
	TargetHeadless TargetKind = "headless"
	// TargetNested routes operations to a compositor nested in the host session.
	TargetNested TargetKind = "nested"
	// TargetExplicit routes operations to the supplied environment.
	TargetExplicit TargetKind = "explicit"
)

// DesktopTarget is an immutable desktop routing snapshot.
type DesktopTarget struct {
	kind TargetKind
	env  []string
}

// EnvironmentTarget creates an explicit target from a process environment.
// The environment is copied immediately.
func EnvironmentTarget(environment []string) DesktopTarget {
	return DesktopTarget{
		kind: TargetExplicit,
		env:  slices.Clone(environment),
	}
}

// Kind returns the target kind.
func (t DesktopTarget) Kind() TargetKind {
	if t.kind == "" {
		return TargetHost
	}
	return t.kind
}

// Env returns a copy of the target's process environment.
func (t DesktopTarget) Env() []string {
	return slices.Clone(t.env)
}

func (t DesktopTarget) clone() DesktopTarget {
	t.env = slices.Clone(t.env)
	return t
}

// SessionConfig controls infrastructure created by WithHeadless or
// WithNested.
type SessionConfig struct {
	// Resolution is the requested managed-session size.
	Resolution image.Point
	// SwayConfigPath selects the Sway configuration file.
	SwayConfigPath string
	// LogDir is a parent directory for managed-session logs when set. Each
	// session receives a unique 0700 child directory containing 0600 files.
	LogDir string
	// ApplicationGracePeriod controls how long managed applications receive to stop.
	ApplicationGracePeriod time.Duration
	// Timeouts controls managed desktop startup and operation waits. Zero fields
	// inherit DefaultTimeoutPolicy.
	Timeouts TimeoutPolicy
}

type targetSelection struct {
	kind   TargetKind
	target DesktopTarget
	config SessionConfig
}

type openConfig struct {
	target               targetSelection
	selected             bool
	startupObserver      StartupPhaseObserver
	startupContext       context.Context //nolint:containedctx // Opening-only option; not retained by the live session.
	capabilitiesSelected bool
	implicitCapabilities bool

	required map[Capability]struct{}
	optional map[Capability]struct{}

	trace       bool
	traceDelay  time.Duration
	traceOut    io.Writer
	logger      *slog.Logger
	portalInput *input.RemoteDesktopOptions
}

// Option configures Open.
type Option func(*openConfig) error

// WithStartupContext controls opening only. Cancellation after a successful
// Open does not close the owned session.
func WithStartupContext(ctx context.Context) Option {
	return func(cfg *openConfig) error {
		if ctx == nil {
			return fmt.Errorf("perfuncted: open: %w: nil startup context", ErrInvalidArgument)
		}
		cfg.startupContext = ctx
		return nil
	}
}

// WithStartupPhaseObserver reports bounded Open stages using fixed phase and
// state values. The observer must append sanitized data and return promptly.
func WithStartupPhaseObserver(observer StartupPhaseObserver) Option {
	return func(cfg *openConfig) error {
		cfg.startupObserver = observer
		return nil
	}
}

// WithTarget routes the Session to an existing desktop target.
func WithTarget(target DesktopTarget) Option {
	return func(cfg *openConfig) error {
		if cfg.selected {
			return fmt.Errorf("perfuncted: %w: desktop target options are mutually exclusive", ErrInvalidArgument)
		}
		cfg.selected = true
		cfg.target = targetSelection{
			kind:   target.Kind(),
			target: target.clone(),
		}
		return nil
	}
}

// WithHeadless starts and owns an isolated headless Sway desktop.
func WithHeadless(configs ...SessionConfig) Option {
	return selectManagedTargetConfig(TargetHeadless, configs)
}

// WithNested starts and owns an isolated Sway desktop nested in the host
// Wayland session.
func WithNested(configs ...SessionConfig) Option {
	return selectManagedTargetConfig(TargetNested, configs)
}

func selectManagedTargetConfig(kind TargetKind, configs []SessionConfig) Option {
	if len(configs) > 1 {
		return func(*openConfig) error {
			return fmt.Errorf("perfuncted: %w: expected at most one session config", ErrInvalidArgument)
		}
	}
	var cfg SessionConfig
	if len(configs) == 1 {
		cfg = configs[0]
	}
	return selectManagedTarget(kind, cfg)
}

// WithRemoteDesktopInput selects the user-authorized XDG RemoteDesktop portal
// and EIS path as the session's required input backend. It never falls back to
// compositor injection or uinput if portal setup is unavailable or denied.
func WithRemoteDesktopInput(options input.RemoteDesktopOptions) Option {
	return func(cfg *openConfig) error {
		if cfg.portalInput != nil {
			return fmt.Errorf("perfuncted: %w: RemoteDesktop input options may be set once", ErrInvalidArgument)
		}
		if _, optional := cfg.optional[CapabilityInput]; optional {
			return fmt.Errorf("perfuncted: %w: portal input cannot be optional", ErrInvalidArgument)
		}
		copy := options
		cfg.portalInput = &copy
		cfg.required[CapabilityInput] = struct{}{}
		cfg.capabilitiesSelected = true
		return nil
	}
}

func selectManagedTarget(kind TargetKind, sessionConfig SessionConfig) Option {
	return func(cfg *openConfig) error {
		if cfg.selected {
			return fmt.Errorf("perfuncted: %w: desktop target options are mutually exclusive", ErrInvalidArgument)
		}
		cfg.selected = true
		cfg.target = targetSelection{
			kind:   kind,
			config: sessionConfig,
		}
		return nil
	}
}

// Require requests capabilities that must open successfully.
func Require(capabilities ...Capability) Option {
	return requestCapabilities(true, capabilities)
}

// Optional requests capabilities whose failures remain inspectable through
// Capability and Capabilities.
func Optional(capabilities ...Capability) Option {
	return requestCapabilities(false, capabilities)
}

func requestCapabilities(required bool, capabilities []Capability) Option {
	return func(cfg *openConfig) error {
		cfg.capabilitiesSelected = true
		for _, cap := range capabilities {
			if !validCapability(cap) {
				return fmt.Errorf("perfuncted: %w: unknown capability %q", ErrInvalidArgument, cap)
			}
			if required {
				if _, ok := cfg.optional[cap]; ok {
					return fmt.Errorf("perfuncted: %w: capability %q cannot be both required and optional", ErrInvalidArgument, cap)
				}
				cfg.required[cap] = struct{}{}
				continue
			}
			if _, ok := cfg.required[cap]; ok {
				return fmt.Errorf("perfuncted: %w: capability %q cannot be both required and optional", ErrInvalidArgument, cap)
			}
			cfg.optional[cap] = struct{}{}
		}
		return nil
	}
}

// WithTrace enables action tracing.
func WithTrace(enabled bool) Option {
	return func(cfg *openConfig) error {
		cfg.trace = enabled
		return nil
	}
}

// WithTraceDelay inserts a delay after traced actions.
func WithTraceDelay(delay time.Duration) Option {
	return func(cfg *openConfig) error {
		cfg.traceDelay = delay
		return nil
	}
}

// WithTraceOut selects the action trace writer.
func WithTraceOut(writer io.Writer) Option {
	return func(cfg *openConfig) error {
		cfg.traceOut = writer
		return nil
	}
}

// WithLogger selects the action trace logger.
func WithLogger(logger *slog.Logger) Option {
	return func(cfg *openConfig) error {
		cfg.logger = logger
		return nil
	}
}

// TimeoutPolicy is the small shared timing policy for managed desktop
// startup and operations. Callers can override the policy as one unit through
// SessionConfig; derived waits must use these values instead of introducing a
// second host-specific timeout.
type TimeoutPolicy struct {
	Short      time.Duration `json:"short"`
	Medium     time.Duration `json:"medium"`
	Long       time.Duration `json:"long"`
	Startup    time.Duration `json:"startup"`
	Diagnostic time.Duration `json:"diagnostic"`
	Poll       time.Duration `json:"poll"`
}

// DefaultTimeoutPolicy is the ordinary library policy. Parent contexts still
// cap every operation, and callers can pass a larger deployment policy through
// SessionConfig when the host is known to be slow.
var DefaultTimeoutPolicy = TimeoutPolicy{
	Short:      5 * time.Second,
	Medium:     30 * time.Second,
	Long:       90 * time.Second,
	Startup:    10 * time.Minute,
	Diagnostic: 5 * time.Second,
	Poll:       150 * time.Millisecond,
}

// WithDefaults fills an incomplete policy from DefaultTimeoutPolicy.
func (p TimeoutPolicy) WithDefaults() TimeoutPolicy {
	d := DefaultTimeoutPolicy
	if p.Short <= 0 {
		p.Short = d.Short
	}
	if p.Medium <= 0 {
		p.Medium = d.Medium
	}
	if p.Long <= 0 {
		p.Long = d.Long
	}
	if p.Startup <= 0 {
		p.Startup = d.Startup
	}
	if p.Diagnostic <= 0 {
		p.Diagnostic = d.Diagnostic
	}
	if p.Poll <= 0 {
		p.Poll = d.Poll
	}
	return p
}

func startupWaitAttempts(policy TimeoutPolicy) int {
	policy = policy.WithDefaults()
	// Ceiling division via quotient and remainder avoids overflowing a
	// valid positive time.Duration when Startup is near its maximum.
	attempts := int(policy.Startup / policy.Poll)
	if policy.Startup%policy.Poll != 0 {
		attempts++
	}
	return attempts
}
