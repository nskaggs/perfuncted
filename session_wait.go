package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"image"
	"sync"
	"time"

	"github.com/nskaggs/perfuncted/window"
)

const defaultWaitInterval = 100 * time.Millisecond

// Condition is an authoritative state check used by Session.Wait.
type Condition interface {
	evaluate(context.Context, *Session) (bool, error)
	describe() string
	pollingOnly() bool
}

type conditionFunc struct {
	name     string
	check    func(context.Context, *Session) (bool, error)
	pollOnly bool
}

func (c conditionFunc) evaluate(
	ctx context.Context,
	session *Session,
) (bool, error) {
	return c.check(ctx, session)
}

func (c conditionFunc) describe() string {
	return c.name
}

func (c conditionFunc) pollingOnly() bool {
	return c.pollOnly
}

// Predicate creates a polling-only condition for arbitrary caller state.
func Predicate(
	name string,
	check func(context.Context) (bool, error),
) Condition {
	return conditionFunc{
		name: name,
		check: func(ctx context.Context, _ *Session) (bool, error) {
			if check == nil {
				return false, errors.New("perfuncted: nil wait predicate")
			}
			return check(ctx)
		},
		pollOnly: true,
	}
}

func sessionCondition(
	name string,
	check func(context.Context, *Session) (bool, error),
) Condition {
	return conditionFunc{
		name:  name,
		check: check,
	}
}

type compositeCondition struct {
	name       string
	conditions []Condition
	mode       string
}

func (c compositeCondition) describe() string {
	return c.name
}

func (c compositeCondition) pollingOnly() bool {
	for _, child := range c.conditions {
		if child != nil && child.pollingOnly() {
			return true
		}
	}
	return false
}

func (c compositeCondition) evaluate(
	ctx context.Context,
	session *Session,
) (bool, error) {
	switch c.mode {
	case "all":
		for _, child := range c.conditions {
			if child == nil {
				return false, errors.New("perfuncted: nil child condition")
			}
			ok, err := child.evaluate(ctx, session)
			if err != nil || !ok {
				return false, err
			}
		}
		return true, nil
	case "any":
		for _, child := range c.conditions {
			if child == nil {
				return false, errors.New("perfuncted: nil child condition")
			}
			ok, err := child.evaluate(ctx, session)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil
	default:
		return false, fmt.Errorf("perfuncted: unknown composite mode %q", c.mode)
	}
}

// All succeeds when every child condition succeeds.
func All(conditions ...Condition) Condition {
	return compositeCondition{
		name:       "all",
		conditions: conditions,
		mode:       "all",
	}
}

// Any succeeds when at least one child condition succeeds.
func Any(conditions ...Condition) Condition {
	return compositeCondition{
		name:       "any",
		conditions: conditions,
		mode:       "any",
	}
}

type notCondition struct {
	child Condition
}

func (c notCondition) describe() string {
	if c.child == nil {
		return "not <nil>"
	}
	return "not " + c.child.describe()
}

func (c notCondition) pollingOnly() bool {
	return c.child != nil && c.child.pollingOnly()
}

func (c notCondition) evaluate(
	ctx context.Context,
	session *Session,
) (bool, error) {
	if c.child == nil {
		return false, errors.New("perfuncted: nil child condition")
	}
	ok, err := c.child.evaluate(ctx, session)
	return !ok, err
}

// Not succeeds when child does not.
func Not(child Condition) Condition {
	return notCondition{child: child}
}

// WindowExists succeeds when match identifies at least one window.
func WindowExists(match WindowMatch) Condition {
	return sessionCondition(
		"window exists "+match.String(),
		func(ctx context.Context, session *Session) (bool, error) {
			windows, err := session.Windows.List(ctx, match)
			return len(windows) > 0, err
		},
	)
}

// WindowState is an alias for WindowExists that emphasizes state fields in
// WindowMatch.
func WindowState(match WindowMatch) Condition {
	return WindowExists(match)
}

// WindowClosed succeeds when the stable window handle no longer exists.
func WindowClosed(target *Window) Condition {
	return sessionCondition(
		"window closed",
		func(ctx context.Context, session *Session) (bool, error) {
			if target == nil || target.session == nil {
				return false, ErrNilSession
			}
			if target.session != session || target.id.session != session {
				return false, errors.New(
					"perfuncted: window belongs to another session",
				)
			}
			backend, ok := session.Windows.Manager.(interface {
				InfoByID(context.Context, string) (window.Info, error)
			})
			if !ok {
				return false, window.ErrNotSupported
			}
			_, err := backend.InfoByID(
				ctx,
				target.id.native,
			)
			if errors.Is(err, ErrWindowNotFound) {
				return true, nil
			}
			return false, err
		},
	)
}

// ApplicationExited succeeds after app has been reaped.
func ApplicationExited(app *Application) Condition {
	return sessionCondition(
		"application exited",
		func(_ context.Context, session *Session) (bool, error) {
			if app == nil || app.session != session {
				return false, errors.New(
					"perfuncted: application belongs to another session",
				)
			}
			select {
			case <-app.done:
				return true, nil
			default:
				return false, nil
			}
		},
	)
}

// ScreenChanged succeeds when rect's hash differs from initial.
func ScreenChanged(rect image.Rectangle, initial uint32) Condition {
	return sessionCondition(
		"screen changed",
		func(ctx context.Context, session *Session) (bool, error) {
			hash, err := session.Screen.grabHash(ctx, rect)
			return hash != initial, err
		},
	)
}

// ScreenStable succeeds after consecutiveSamples identical observations.
func ScreenStable(
	rect image.Rectangle,
	consecutiveSamples int,
) Condition {
	var (
		mu       sync.Mutex
		lastHash uint32
		haveHash bool
		stable   int
	)
	return sessionCondition(
		"screen stable",
		func(ctx context.Context, session *Session) (bool, error) {
			hash, err := session.Screen.grabHash(ctx, rect)
			if err != nil {
				return false, err
			}
			mu.Lock()
			defer mu.Unlock()
			if !haveHash || hash != lastHash {
				lastHash = hash
				haveHash = true
				stable = 1
			} else {
				stable++
			}
			return stable >= max(1, consecutiveSamples), nil
		},
	)
}

// WaitOption configures fallback polling.
type WaitOption func(*waitConfig) error

type waitConfig struct {
	interval time.Duration
}

// WaitEvery changes only the fallback polling cadence.
func WaitEvery(interval time.Duration) WaitOption {
	return func(config *waitConfig) error {
		if interval <= 0 {
			return errors.New("perfuncted: wait interval must be positive")
		}
		config.interval = interval
		return nil
	}
}

// Wait blocks until condition succeeds. Timeout and cancellation come only
// from ctx; backend failures are returned immediately.
func (s *Session) Wait(
	ctx context.Context,
	condition Condition,
	options ...WaitOption,
) error {
	if s == nil {
		return ErrNilSession
	}
	if ctx == nil {
		return errors.New("perfuncted: wait: nil context")
	}
	if condition == nil {
		return errors.New("perfuncted: wait: nil condition")
	}
	config := waitConfig{interval: defaultWaitInterval}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(&config); err != nil {
			return err
		}
	}

	ticker := time.NewTicker(config.interval)
	defer ticker.Stop()
	for {
		wake := s.waitChanges()
		ok, err := condition.evaluate(ctx, s)
		if err != nil {
			return fmt.Errorf("wait %s: %w", condition.describe(), err)
		}
		if ok {
			return nil
		}
		if condition.pollingOnly() {
			wake = nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.ctx.Done():
			return ErrSessionClosed
		case <-wake:
		case <-ticker.C:
		}
	}
}

type invalidationHub struct {
	mu sync.Mutex
	ch chan struct{}
}

func newInvalidationHub() *invalidationHub {
	return &invalidationHub{ch: make(chan struct{})}
}

func (h *invalidationHub) subscribe() <-chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ch
}

func (h *invalidationHub) notify() {
	h.mu.Lock()
	close(h.ch)
	h.ch = make(chan struct{})
	h.mu.Unlock()
}

type windowChangeSource interface {
	WindowChanges() <-chan struct{}
}

func (s *Session) waitChanges() <-chan struct{} {
	s.hubOnce.Do(func() {
		hub := newInvalidationHub()
		s.hubMu.Lock()
		s.hub = hub
		s.hubMu.Unlock()
		if s.Windows == nil || s.Windows.Manager == nil {
			return
		}
		source, ok := s.Windows.Manager.(windowChangeSource)
		if !ok {
			return
		}
		changes := source.WindowChanges()
		if changes == nil {
			return
		}
		go func() {
			for {
				select {
				case <-s.ctx.Done():
					return
				case _, ok := <-changes:
					if !ok {
						return
					}
					hub.notify()
				}
			}
		}()
	})
	s.hubMu.RLock()
	hub := s.hub
	s.hubMu.RUnlock()
	if hub == nil {
		return nil
	}
	return hub.subscribe()
}

func (s *Session) notifyWaiters() {
	if s == nil {
		return
	}
	s.hubMu.RLock()
	hub := s.hub
	s.hubMu.RUnlock()
	if hub == nil {
		return
	}
	hub.notify()
}
