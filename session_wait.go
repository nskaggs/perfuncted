package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nskaggs/perfuncted/accessibility"
	"github.com/nskaggs/perfuncted/internal/util"
	"github.com/nskaggs/perfuncted/window"
)

const defaultWaitInterval = 100 * time.Millisecond

// waitEvaluateFailureLimit bounds consecutive condition-evaluation errors
// before a wait gives up. Conditions query live window-manager state, and on
// slow or loaded hosts a single query can exceed its internal deadline (for
// example sway IPC under CPU contention while another session tears down).
// Such transient failures must not abort a long window wait, so they are
// retried; sustained failure still fails with the last error attached.
const waitEvaluateFailureLimit = 30

// isPermanentWaitError reports whether an evaluation error means the wait can
// never be satisfied and must abort immediately instead of being retried.
func isPermanentWaitError(err error) bool {
	return errors.Is(err, ErrSessionClosed) || errors.Is(err, ErrApplicationExited)
}

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

// AccessibilityNodeExists succeeds when a bounded AT-SPI query returns at
// least one matching node. It is polling-safe and refreshes the backend cache
// through the normal generation boundary.
func AccessibilityNodeExists(root accessibility.NodeID, query accessibility.Query, opts accessibility.SnapshotOptions) Condition {
	return sessionCondition("accessibility node exists", func(ctx context.Context, session *Session) (bool, error) {
		nodes, err := session.Accessibility.Find(ctx, root, query, opts)
		return len(nodes) > 0, err
	})
}

// AccessibilityFocused succeeds when AT-SPI reports a focused object.
func AccessibilityFocused(opts accessibility.SnapshotOptions) Condition {
	return sessionCondition("accessibility focused", func(ctx context.Context, session *Session) (bool, error) {
		_, err := session.Accessibility.Focused(ctx, opts)
		if errors.Is(err, accessibility.ErrNotFound) {
			return false, nil
		}
		return err == nil, err
	})
}

// AccessibilityStateContains succeeds when a matching accessible node has
// every requested state. The condition always performs a fresh bounded query;
// AT-SPI events only wake Session.Wait and are never treated as authoritative.
func AccessibilityStateContains(root accessibility.NodeID, query accessibility.Query, states []string, opts accessibility.SnapshotOptions) Condition {
	query.States = append([]string(nil), states...)
	return sessionCondition("accessibility state contains", func(ctx context.Context, session *Session) (bool, error) {
		nodes, err := session.Accessibility.Find(ctx, root, query, opts)
		return len(nodes) > 0, err
	})
}

// AccessibilityTextContains succeeds when an accessible node exposes the
// requested text. It is a convenience over AccessibilityNodeExists and keeps
// text matching bounded by SnapshotOptions.
func AccessibilityTextContains(root accessibility.NodeID, text string, opts accessibility.SnapshotOptions) Condition {
	return AccessibilityNodeExists(root, accessibility.Query{Text: text}, opts)
}

// WindowClosed succeeds when the stable window handle no longer exists.
func WindowClosed(target *Window) Condition {
	return sessionCondition(
		"window closed",
		func(ctx context.Context, session *Session) (bool, error) {
			if target == nil || target.id.session == nil {
				return false, ErrNilSession
			}
			if target.id.session != session {
				return false, fmt.Errorf(
					"perfuncted: window belongs to another session: %w",
					ErrInvalidArgument,
				)
			}
			if err := session.Windows.checkAvailable("info"); err != nil {
				return false, err
			}
			backend, ok := session.Windows.backend.(interface {
				InfoByID(context.Context, string) (window.Info, error)
			})
			if !ok {
				return false, session.Windows.operationError(
					"info",
					errors.Join(ErrUnsupported, window.ErrNotSupported),
				)
			}
			_, err := backend.InfoByID(
				ctx,
				target.id.native,
			)
			if errors.Is(err, ErrWindowNotFound) {
				return true, nil
			}
			return false, session.Windows.operationError("info", err)
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
			return fmt.Errorf("perfuncted: %w: wait interval must be positive", ErrInvalidArgument)
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
		return fmt.Errorf("perfuncted: wait: %w: nil context", ErrInvalidArgument)
	}
	if condition == nil {
		return fmt.Errorf("perfuncted: wait: %w: nil condition", ErrInvalidArgument)
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

	return s.wait(ctx, condition, config)
}

func (s *Session) wait(
	ctx context.Context,
	condition Condition,
	config waitConfig,
) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	ticker := time.NewTicker(config.interval)
	defer ticker.Stop()
	evaluationFailures := 0
	for {
		if err := s.waitState(ctx); err != nil {
			return err
		}
		wake := s.waitChanges()
		ok, err := condition.evaluate(ctx, s)
		if err != nil {
			if stateErr := s.waitState(ctx); stateErr != nil {
				return stateErr
			}
			if isPermanentWaitError(err) {
				return fmt.Errorf("wait %s: %w", condition.describe(), err)
			}
			evaluationFailures++
			if evaluationFailures >= waitEvaluateFailureLimit {
				return fmt.Errorf("wait %s: %d consecutive evaluation errors: %w", condition.describe(), evaluationFailures, err)
			}
			// Transient query failure: keep polling until the caller's
			// deadline; the condition may still be satisfied later.
		} else {
			evaluationFailures = 0
			if ok {
				return nil
			}
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

func (s *Session) waitState(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.ensureOpen()
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

func (s *Session) waitChanges() <-chan struct{} { //nolint:gocyclo // one hub owns independent window and accessibility sources.
	s.hubOnce.Do(func() {
		hub := newInvalidationHub()
		s.hubMu.Lock()
		s.hub = hub
		s.hubMu.Unlock()
		if s.Windows != nil && !util.IsNil(s.Windows.backend) {
			if source, ok := s.Windows.backend.(windowChangeSource); ok {
				if changes := source.WindowChanges(); changes != nil {
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
				}
			}
		}
		if s.Accessibility != nil && !util.IsNil(s.Accessibility.backend) {
			if source, ok := s.Accessibility.backend.(accessibility.EventSource); ok {
				if events, err := source.Events(s.ctx, accessibility.EventOptions{Buffer: 128}); err == nil && events != nil {
					go func() {
						for {
							select {
							case <-s.ctx.Done():
								return
							case _, ok := <-events:
								if !ok {
									return
								}
								hub.notify()
							}
						}
					}()
				}
			}
		}
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
