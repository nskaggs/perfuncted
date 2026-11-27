package input

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nskaggs/perfuncted/v2/internal/env"
	"sync"
	"time"
)

// Observation is independent from the input-injection Inputter interface.
// No backend may silently substitute injected input for observed events.
var (
	ErrObservationUnsupported   = errors.New("input observation: unsupported")
	ErrObservationDenied        = errors.New("input observation: permission denied")
	ErrObservationConflict      = errors.New("input observation: binding conflict")
	ErrObservationLoss          = errors.New("input observation: event loss")
	ErrObservationRevoked       = errors.New("input observation: authorization revoked")
	ErrObservationUnavailable   = errors.New("input observation: unavailable")
	ErrInvalidObservationFilter = errors.New("input observation: invalid filter")
)

// ObservationFilter selects one named shortcut or mouse button and transitions.
type ObservationFilter struct {
	Combo        string
	Button       int
	Transition   string
	ReportRepeat bool
}

// ObservationEvent identifies a matching transition without input provenance.
type ObservationEvent struct {
	Kind       string    `json:"kind"`
	ID         string    `json:"id"`
	Transition string    `json:"transition"`
	Time       time.Time `json:"time"`
	Source     string    `json:"source"`
	Repeat     bool      `json:"repeat,omitempty"`
	// Neither XI2 nor this API attests physical versus synthetic input.
}

// ObservationSubscription is a bounded, cancellable event stream.
type ObservationSubscription interface {
	Events() <-chan ObservationEvent
	Err() error
	Close() error
}

// Observer is a separate read-only input observation backend.
type Observer interface {
	Listen(context.Context, ObservationFilter) (ObservationSubscription, error)
	SupportedOperations() []string
	Close() error
}

const observationQueueSize = 64

type observationSubscription struct {
	mu      sync.Mutex
	events  chan ObservationEvent
	done    chan struct{}
	err     error
	closeFn func()
	closed  bool
}

func newObservationSubscription(closeFn func()) *observationSubscription {
	return &observationSubscription{events: make(chan ObservationEvent, observationQueueSize), done: make(chan struct{}), closeFn: closeFn}
}
func (s *observationSubscription) Events() <-chan ObservationEvent { return s.events }

// Done closes when the subscription terminates, independently of event consumption.
func (s *observationSubscription) Done() <-chan struct{} { return s.done }
func (s *observationSubscription) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}
func (s *observationSubscription) push(ev ObservationEvent) bool {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false
	}
	select {
	case s.events <- ev:
		s.mu.Unlock()
		return true
	default:
		s.terminateLocked(ErrObservationLoss)
		cleanup := s.closeFn
		s.mu.Unlock()
		if cleanup != nil {
			cleanup()
		}
		return false
	}
}
func (s *observationSubscription) terminateLocked(err error) {
	if s.closed {
		return
	}
	s.closed = true
	s.err = err
	close(s.events)
	close(s.done)
}
func (s *observationSubscription) finish(err error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.terminateLocked(err)
	cleanup := s.closeFn
	s.mu.Unlock()
	if cleanup != nil {
		cleanup()
	}
}
func (s *observationSubscription) Close() error { s.finish(nil); return nil }

//nolint:gocyclo // validate full named-chord grammar before OS registration
func normalizeObservationFilter(filter ObservationFilter) (ObservationFilter, error) {
	filter.Combo = strings.TrimSpace(filter.Combo)
	if (filter.Combo == "") == (filter.Button == 0) {
		return filter, fmt.Errorf("%w: choose exactly one of combo or button", ErrInvalidObservationFilter)
	}
	if filter.Button != 0 && (filter.Button < 1 || filter.Button > 3) {
		return filter, fmt.Errorf("%w: button must be 1, 2, or 3", ErrInvalidObservationFilter)
	}
	if filter.Transition == "" {
		filter.Transition = "press"
	}
	switch filter.Transition {
	case "press", "release", "both":
	default:
		return filter, fmt.Errorf("%w: unsupported transition %q", ErrInvalidObservationFilter, filter.Transition)
	}
	if filter.Button != 0 {
		return filter, nil
	}
	parts := strings.Split(filter.Combo, "+")
	seen := make(map[string]bool, len(parts))
	for i, part := range parts {
		part = strings.ToUpper(strings.TrimSpace(part))
		switch part {
		case "CTRL", "CONTROL":
			part = "Ctrl"
		case "ALT":
			part = "Alt"
		case "SHIFT":
			part = "Shift"
		case "SUPER", "META":
			part = "Super"
		default:
			switch {
			case len(part) == 1 && ((part[0] >= 'A' && part[0] <= 'Z') || (part[0] >= '0' && part[0] <= '9')):
				// Valid unmodified letter or digit.
			case strings.HasPrefix(part, "F") && len(part) <= 3 && len(part) > 1:
				var n int
				if _, err := fmt.Sscanf(part, "F%d", &n); err != nil || n < 1 || n > 12 || fmt.Sprintf("F%d", n) != part {
					return filter, fmt.Errorf("%w: unsupported key %q", ErrInvalidObservationFilter, part)
				}
			default:
				return filter, fmt.Errorf("%w: unsupported key %q", ErrInvalidObservationFilter, part)
			}
		}
		if seen[part] {
			return filter, fmt.Errorf("%w: duplicated key %q", ErrInvalidObservationFilter, part)
		}
		if i < len(parts)-1 && part != "Ctrl" && part != "Alt" && part != "Shift" && part != "Super" {
			return filter, fmt.Errorf("%w: modifiers must precede the key", ErrInvalidObservationFilter)
		}
		if i == len(parts)-1 && (part == "Ctrl" || part == "Alt" || part == "Shift" || part == "Super") {
			return filter, fmt.Errorf("%w: missing nonmodifier key", ErrInvalidObservationFilter)
		}
		seen[part] = true
		parts[i] = part
	}
	filter.Combo = strings.Join(parts, "+")
	return filter, nil
}

// OpenObserver resolves a separate, read-only observation backend. A Wayland
// session is never treated as native X11 merely because DISPLAY is populated
// by XWayland. No privileged device or injection fallback is allowed.
func OpenObserver(ctx context.Context, rt env.Runtime) (Observer, error) {
	if ctx == nil {
		return nil, ErrInvalidObservationFilter
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Treat mixed or ambiguous sessions as unsupported. The actual X server
	// is independently checked for XWAYLAND before accepting it.
	if strings.ToLower(rt.Get("XDG_SESSION_TYPE")) != "x11" ||
		rt.Get("WAYLAND_DISPLAY") != "" ||
		rt.Get("WAYLAND_SOCKET") != "" ||
		rt.SocketPath() != "" ||
		rt.Display() == "" {
		return nil, ErrObservationUnsupported
	}
	return openX11Observer(ctx, rt.Display())
}

// ValidateObservationFilter performs the same strict side-effect-free
// validation used by both observer backends.
func ValidateObservationFilter(filter ObservationFilter) error {
	_, err := normalizeObservationFilter(filter)
	return err
}
