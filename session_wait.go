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

const (
	// waitEvaluateFailureLimit bounds consecutive condition-evaluation errors
	// before a wait gives up. Conditions query live window-manager state, and on
	// slow or loaded hosts a single query can exceed its internal deadline (for
	// example sway IPC under CPU contention while another session tears down).
	// Such transient failures must not abort a long window wait, so they are
	// retried; sustained failure still fails with the last error attached.
	waitEvaluateFailureLimit = 30

	// waitEvaluateStreakBudget bounds how long one run of consecutive failures may
	// stay unanswered. The count alone is not a bound in the dimension that
	// matters: a wait driven by window or accessibility events evaluates far more
	// often than one driven by its poll interval, so the same count spans very
	// different durations, and a caller with a long deadline would keep waiting
	// on a source that has stopped answering.
	waitEvaluateStreakBudget = 2 * time.Second

	// waitEvaluateTotalLimit bounds failures across the whole wait. A backend
	// that alternates between answering and failing resets the consecutive count
	// every time it answers, so without a separate total it can be waited out
	// indefinitely while the caller is told nothing.
	waitEvaluateTotalLimit = 120
)

// waitEvaluateFailures tracks condition-evaluation failures so a wait can tell
// "the condition is not satisfied yet" apart from "the state cannot be read".
type waitEvaluateFailures struct {
	consecutive int
	total       int
	since       time.Time
	now         func() time.Time
}

func (f *waitEvaluateFailures) clock() time.Time {
	if f.now != nil {
		return f.now()
	}
	return time.Now()
}

// record accounts for one failed evaluation and reports whether the wait has
// tolerated failures long enough that continuing hides an unreadable state.
func (f *waitEvaluateFailures) record() (exhausted bool, consecutive, total int) {
	now := f.clock()
	if f.consecutive == 0 {
		f.since = now
	}
	f.consecutive++
	f.total++
	exhausted = f.consecutive >= waitEvaluateFailureLimit ||
		f.total >= waitEvaluateTotalLimit ||
		now.Sub(f.since) >= waitEvaluateStreakBudget
	return exhausted, f.consecutive, f.total
}

// reset records that the state was read successfully, ending the current run of
// failures. The wait-wide total deliberately survives, because a source that
// keeps failing between successful reads is still mostly unreadable.
func (f *waitEvaluateFailures) reset() {
	f.consecutive = 0
	f.since = time.Time{}
}

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

// AccessibilityNodeExists succeeds when a fresh, structurally complete AT-SPI
// query returns at least one matching node. An incomplete query is an
// evaluation error: Session.Wait retries it as transient up to the consecutive
// evaluation failure limit, then returns the last error.
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
// every requested state in a fresh, structurally complete query. An incomplete
// query is retried by Session.Wait as a transient evaluation error up to the
// consecutive evaluation failure limit. AT-SPI events only wake Session.Wait
// and are never treated as authoritative.
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
			ctx, cancel := session.Windows.backendContext(ctx, session.Timeouts().Medium)
			defer cancel()
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

// WaitEvidence describes the bounded wakeups observed while a condition was
// evaluated. It is diagnostic evidence only; each wakeup still causes a fresh
// authoritative condition evaluation.
type WaitEvidence struct {
	Evaluations            int                  `json:"evaluations"`
	Wakeups                int                  `json:"wakeups"`
	PollWakeups            int                  `json:"pollWakeups"`
	WindowWakeups          int                  `json:"windowWakeups"`
	AccessibilityWakeups   int                  `json:"accessibilityWakeups"`
	ApplicationWakeups     int                  `json:"applicationWakeups"`
	LastWakeSource         string               `json:"lastWakeSource,omitempty"`
	LastAccessibilityEvent *accessibility.Event `json:"lastAccessibilityEvent,omitempty"`
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
	_, err := s.WaitWithEvidence(ctx, condition, options...)
	return err
}

// WaitWithEvidence blocks until condition succeeds and returns bounded
// diagnostic evidence about the event-backed and polling wakeups observed.
// Events only wake the wait; the condition remains the authority.
func (s *Session) WaitWithEvidence(
	ctx context.Context,
	condition Condition,
	options ...WaitOption,
) (WaitEvidence, error) {
	if s == nil {
		return WaitEvidence{}, ErrNilSession
	}
	if ctx == nil {
		return WaitEvidence{}, fmt.Errorf("perfuncted: wait: %w: nil context", ErrInvalidArgument)
	}
	if condition == nil {
		return WaitEvidence{}, fmt.Errorf("perfuncted: wait: %w: nil condition", ErrInvalidArgument)
	}
	config := waitConfig{interval: s.Timeouts().Poll}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(&config); err != nil {
			return WaitEvidence{}, err
		}
	}

	return s.waitWithEvidence(ctx, condition, config)
}

func (s *Session) waitWithEvidence(
	ctx context.Context,
	condition Condition,
	config waitConfig,
) (WaitEvidence, error) { //nolint:gocyclo // evaluation, wake, and bounded failure paths are intentionally explicit.
	var evidence WaitEvidence
	if err := s.ensureOpen(); err != nil {
		return evidence, err
	}
	ticker := time.NewTicker(config.interval)
	defer ticker.Stop()
	var evaluationFailures waitEvaluateFailures
	for {
		if err := s.waitState(ctx); err != nil {
			return evidence, err
		}
		epoch := s.waitChanges()
		evidence.Evaluations++
		ok, err := s.evaluateWaitCondition(ctx, condition, &evaluationFailures)
		if err != nil {
			return evidence, err
		}
		if ok {
			return evidence, nil
		}
		if condition.pollingOnly() {
			epoch = nil
		}
		select {
		case <-ctx.Done():
			return evidence, ctx.Err()
		case <-s.ctx.Done():
			return evidence, ErrSessionClosed
		case <-waitEpochDone(epoch):
			if epoch != nil {
				evidence.recordWaitChange(epoch.change)
			}
		case <-ticker.C:
			evidence.PollWakeups++
		}
	}
}

func (s *Session) evaluateWaitCondition(ctx context.Context, condition Condition, failures *waitEvaluateFailures) (bool, error) {
	ok, err := condition.evaluate(ctx, s)
	if err == nil {
		failures.reset()
		return ok, nil
	}
	if stateErr := s.waitState(ctx); stateErr != nil {
		return false, stateErr
	}
	if isPermanentWaitError(err) {
		return false, fmt.Errorf("wait %s: %w", condition.describe(), err)
	}
	exhausted, consecutive, total := failures.record()
	if exhausted {
		return false, fmt.Errorf("wait %s: %d consecutive and %d total evaluation errors: %w",
			condition.describe(), consecutive, total, err)
	}
	// Transient query failure: keep polling until the caller's deadline; the
	// condition may still be satisfied later.
	return false, nil
}

func (e *WaitEvidence) recordWaitChange(change waitChange) {
	e.Wakeups++
	e.LastWakeSource = change.source
	switch change.source {
	case "window":
		e.WindowWakeups++
	case "accessibility":
		e.AccessibilityWakeups++
		if change.event != nil {
			event := *change.event
			e.LastAccessibilityEvent = &event
		}
	case "application":
		e.ApplicationWakeups++
	}
}

func (s *Session) waitState(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.ensureOpen()
}

type invalidationHub struct {
	mu    sync.Mutex
	epoch *waitEpoch
}

// waitEpoch is immutable once notify closes done. Each waiter retains the
// evidence belonging to the exact broadcast epoch that woke it; a later
// notification cannot overwrite it before the waiter records the wake.
type waitEpoch struct {
	done   chan struct{}
	change waitChange
}

func newInvalidationHub() *invalidationHub {
	return &invalidationHub{epoch: &waitEpoch{done: make(chan struct{})}}
}

func (h *invalidationHub) subscribe() *waitEpoch {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.epoch
}

func (h *invalidationHub) notify(change waitChange) {
	h.mu.Lock()
	epoch := h.epoch
	epoch.change = cloneWaitChange(change)
	close(epoch.done)
	h.epoch = &waitEpoch{done: make(chan struct{})}
	h.mu.Unlock()
}

func cloneWaitChange(change waitChange) waitChange {
	if change.event != nil {
		event := *change.event
		change.event = &event
	}
	return change
}

type waitChange struct {
	source string
	event  *accessibility.Event
}

type windowChangeSource interface {
	WindowChanges() <-chan struct{}
}

func (s *Session) waitChanges() *waitEpoch { //nolint:gocyclo // one hub owns independent window and accessibility sources.
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
								hub.notify(waitChange{source: "window"})
							}
						}
					}()
				}
			}
		}
		if s.Accessibility != nil && !util.IsNil(s.Accessibility.backend) {
			var openEvents func(context.Context, accessibility.EventOptions) (<-chan accessibility.Event, error)
			if owner, ok := s.Accessibility.backend.(*accessibilityBackendOwner); ok {
				openEvents = owner.eventsAcrossReopens
			} else if source, ok := s.Accessibility.backend.(accessibility.EventSource); ok {
				openEvents = source.Events
			}
			if openEvents != nil {
				go func() {
					events, err := openEvents(s.ctx, accessibility.EventOptions{Buffer: 128})
					if err != nil || events == nil {
						return
					}
					for {
						select {
						case <-s.ctx.Done():
							return
						case event, ok := <-events:
							if !ok {
								return
							}
							hub.notify(waitChange{source: "accessibility", event: &event})
						}
					}
				}()
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
	hub.notify(waitChange{source: "application"})
}

func waitEpochDone(epoch *waitEpoch) <-chan struct{} {
	if epoch == nil {
		return nil
	}
	return epoch.done
}
