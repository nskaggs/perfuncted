package perfuncted

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"
)

// ErrNotAvailable indicates a capability was requested but no backend was provided.
var ErrNotAvailable = errors.New("not available")

// ErrNilSession is returned when operating on a nil Session.
var ErrNilSession = errors.New("session is nil")

// Capability identifies a category of backends that a session may provide.
type Capability int

const (
	CapabilityScreen Capability = iota
	CapabilityInput
	CapabilityWindows
	CapabilityOutputs
	CapabilityClipboard
)

func (c Capability) String() string {
	switch c {
	case CapabilityScreen:
		return "screen"
	case CapabilityInput:
		return "input"
	case CapabilityWindows:
		return "windows"
	case CapabilityOutputs:
		return "outputs"
	case CapabilityClipboard:
		return "clipboard"
	}
	return "unknown"
}

// CapabilityError is returned when a session operation requires a capability
// that was not provided.
type CapabilityError struct {
	Cap Capability
	Err error
}

func (e *CapabilityError) Error() string {
	return fmt.Sprintf("%s: %v", e.Cap, e.Err)
}

func (e *CapabilityError) Unwrap() error { return e.Err }

// DesktopTarget selects the session startup mode.
type DesktopTarget int

const (
	TargetHost     DesktopTarget = iota // use the current desktop
	TargetHeadless                      // launch a new headless session
	TargetNested                        // launch a nested session
)

// SessionConfig holds resolved options for creating a Session.
type SessionConfig struct {
	Desktop    DesktopTarget
	Idle       bool
	Trace      bool
	TraceDelay time.Duration
	TraceOut   io.Writer
	Logger     *slog.Logger
}

// Option configures how Open resolves backends and launches sessions.
type Option func(*SessionConfig)

func WithDesktop(t DesktopTarget) Option {
	return func(c *SessionConfig) { c.Desktop = t }
}

func WithIdle(b bool) Option {
	return func(c *SessionConfig) { c.Idle = b }
}

func WithTrace(b bool) Option {
	return func(c *SessionConfig) { c.Trace = b }
}

func WithTraceDelay(d time.Duration) Option {
	return func(c *SessionConfig) { c.TraceDelay = d }
}

func WithTraceOut(w io.Writer) Option {
	return func(c *SessionConfig) { c.TraceOut = w }
}

func WithLogger(l *slog.Logger) Option {
	return func(c *SessionConfig) { c.Logger = l }
}
