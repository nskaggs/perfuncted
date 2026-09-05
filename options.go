package perfuncted

import (
	"errors"
	"fmt"
	"image"
	"io"
	"log/slog"
	"slices"
	"time"

	capabilityops "github.com/nskaggs/perfuncted/internal/capability"
)

var (
	// ErrUnsupported indicates that the selected backend does not implement an
	// operation. It is safe to inspect with errors.Is.
	ErrUnsupported = errors.New("operation unsupported")
	// ErrUnavailable indicates that a requested capability or backend could not
	// be opened for the current session.
	ErrUnavailable = errors.New("backend unavailable")
	// ErrInvalidArgument indicates invalid caller input.
	ErrInvalidArgument = errors.New("invalid argument")
	// ErrOperationFailed identifies an operation that was supported but failed
	// while executing. The underlying backend error remains wrapped.
	ErrOperationFailed = errors.New("operation failed")
	// ErrNilSession is returned when operating on a nil Session.
	ErrNilSession = errors.New("session is nil")
	// ErrSessionClosed is returned when starting work on a closed Session.
	ErrSessionClosed = errors.New("session is closed")
)

// Capability identifies a category of desktop automation operations.
type Capability string

const (
	// CapabilityScreen identifies screen capture operations.
	CapabilityScreen Capability = "screen"
	// CapabilityInput identifies keyboard and pointer input operations.
	CapabilityInput Capability = "input"
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
// Operations is an immutable snapshot of the operations exposed by the
// selected backend.
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
}

// Supports reports whether the resolved backend advertises operation.
func (s CapabilityStatus) Supports(operation string) bool {
	return slices.Contains(s.Operations, operation)
}

func (s CapabilityStatus) clone() CapabilityStatus {
	s.Operations = slices.Clone(s.Operations)
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
}

type targetSelection struct {
	kind   TargetKind
	target DesktopTarget
	config SessionConfig
}

type openConfig struct {
	target   targetSelection
	selected bool

	required map[Capability]struct{}
	optional map[Capability]struct{}

	trace      bool
	traceDelay time.Duration
	traceOut   io.Writer
	logger     *slog.Logger
}

// Option configures Open.
type Option func(*openConfig) error

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
func WithHeadless(sessionConfig SessionConfig) Option {
	return selectManagedTarget(TargetHeadless, sessionConfig)
}

// WithNested starts and owns an isolated Sway desktop nested in the host
// Wayland session.
func WithNested(sessionConfig SessionConfig) Option {
	return selectManagedTarget(TargetNested, sessionConfig)
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
